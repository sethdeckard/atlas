// Package git wraps a small set of git CLI invocations and the path
// gymnastics needed to handle worktrees and bare repos uniformly.
//
// The expected call shape is: ResolvePaths(path) once per repo, then pass the
// resulting Paths to the helpers (Head, Status, LastCommit, OriginURL,
// DefaultBranch, ResolveUpstream, BehindAhead, StashCount, BranchCount,
// CommitsLast30d). Helpers never reach for ".git" themselves — Paths makes
// the "where is the gitdir / commondir / worktree" question explicit.
//
// Invariant: every helper here is local-only. atlas never runs `git fetch`,
// `git pull`, `git push`, or `git clone`. Behind/ahead counts reflect the
// user's most recent local fetch state; if they want fresher numbers they
// run `git fetch` themselves and atlas picks up the new mtime via
// UpstreamRefMtime / PackedRefsMtime cache fingerprints on the next launch.
package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Paths is the resolved layout of a single repository.
//
// For a normal repo at /p, GitDir == CommonDir == /p/.git and
// WorktreePath == PrimaryWorktreePath == /p.
//
// For a linked worktree, GitDir is the per-worktree gitdir under the primary
// repo's .git/worktrees/<name>/, while CommonDir is the primary's .git
// (where refs/, objects/, and config live). PrimaryWorktreePath is the parent
// of CommonDir when CommonDir ends in ".git".
//
// For a bare repo, GitDir == CommonDir == path itself, WorktreePath is empty,
// and Bare is true.
type Paths struct {
	WorktreePath        string
	GitDir              string
	CommonDir           string
	PrimaryWorktreePath string
	Bare                bool
}

// ResolvePaths inspects path and returns the Paths layout. It returns an
// error when path is not recognizable as any of: normal repo, linked
// worktree, or bare repo.
func ResolvePaths(path string) (Paths, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return Paths{}, fmt.Errorf("abs path: %w", err)
	}

	dotGit := filepath.Join(abs, ".git")
	info, statErr := os.Lstat(dotGit)

	switch {
	case statErr == nil && info.IsDir():
		return Paths{
			WorktreePath:        abs,
			GitDir:              dotGit,
			CommonDir:           dotGit,
			PrimaryWorktreePath: abs,
		}, nil

	case statErr == nil && info.Mode().IsRegular():
		return resolveWorktree(abs, dotGit)

	case errors.Is(statErr, os.ErrNotExist):
		// Maybe a bare repo — check for HEAD/objects/refs at path itself.
		if isBareRepo(abs) {
			return Paths{
				GitDir:    abs,
				CommonDir: abs,
				Bare:      true,
			}, nil
		}
		return Paths{}, fmt.Errorf("not a git repository: %s", abs)

	case statErr != nil:
		return Paths{}, fmt.Errorf("stat .git: %w", statErr)
	}

	return Paths{}, fmt.Errorf("unrecognized .git entry at %s", dotGit)
}

func resolveWorktree(worktreePath, dotGitFile string) (Paths, error) {
	contents, err := os.ReadFile(dotGitFile)
	if err != nil {
		return Paths{}, fmt.Errorf("read .git file: %w", err)
	}
	const prefix = "gitdir:"
	line := strings.TrimSpace(string(contents))
	if !strings.HasPrefix(line, prefix) {
		return Paths{}, fmt.Errorf(".git file missing 'gitdir:' prefix")
	}
	gitDirRaw := strings.TrimSpace(strings.TrimPrefix(line, prefix))
	gitDir := gitDirRaw
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(worktreePath, gitDir)
	}
	gitDir = filepath.Clean(gitDir)

	// Try to resolve the common dir via <gitdir>/commondir; default to gitdir.
	commonDir := gitDir
	if data, err := os.ReadFile(filepath.Join(gitDir, "commondir")); err == nil {
		raw := strings.TrimSpace(string(data))
		if raw != "" {
			cd := raw
			if !filepath.IsAbs(cd) {
				cd = filepath.Join(gitDir, cd)
			}
			cd, err := filepath.Abs(filepath.Clean(cd))
			if err == nil {
				commonDir = cd
			}
		}
	}

	primary := ""
	if filepath.Base(commonDir) == ".git" {
		primary = filepath.Dir(commonDir)
	}

	return Paths{
		WorktreePath:        worktreePath,
		GitDir:              gitDir,
		CommonDir:           commonDir,
		PrimaryWorktreePath: primary,
	}, nil
}

func isBareRepo(path string) bool {
	for _, name := range []string{"HEAD", "objects", "refs"} {
		if _, err := os.Stat(filepath.Join(path, name)); err != nil {
			return false
		}
	}
	return true
}

// Head returns the current branch (when on one), the abbreviated SHA, and
// whether HEAD is detached. Empty repos (no commits) return zero values and a
// nil error.
func Head(ctx context.Context, p Paths) (branch, sha string, detached bool, err error) {
	headFile := filepath.Join(p.GitDir, "HEAD")
	contents, readErr := os.ReadFile(headFile)
	if readErr != nil {
		// Fall back to symbolic-ref via shellout.
		out, runErr := runGit(ctx, "", []string{"--git-dir", p.GitDir, "symbolic-ref", "HEAD"})
		if runErr != nil {
			return "", "", false, fmt.Errorf("read HEAD: %w", readErr)
		}
		ref := strings.TrimSpace(out)
		return strings.TrimPrefix(ref, "refs/heads/"), "", false, nil
	}
	line := strings.TrimSpace(string(contents))
	if strings.HasPrefix(line, "ref: refs/heads/") {
		branch = strings.TrimPrefix(line, "ref: refs/heads/")
		// Resolve the SHA from refs/heads/<branch> or packed-refs. Empty repo
		// (unborn branch) → no SHA, no error.
		sha, _ = resolveBranchSHA(p.CommonDir, p.GitDir, branch)
		return branch, sha, false, nil
	}
	// Detached HEAD — line is the full SHA.
	if len(line) >= 7 {
		return "", line[:7], true, nil
	}
	return "", line, true, nil
}

func resolveBranchSHA(commonDir, gitDir, branch string) (string, error) {
	candidates := []string{
		filepath.Join(commonDir, "refs", "heads", branch),
		filepath.Join(gitDir, "refs", "heads", branch),
	}
	for _, c := range candidates {
		if data, err := os.ReadFile(c); err == nil {
			full := strings.TrimSpace(string(data))
			if len(full) >= 7 {
				return full[:7], nil
			}
			return full, nil
		}
	}
	// Try packed-refs.
	packed, err := os.ReadFile(filepath.Join(commonDir, "packed-refs"))
	if err != nil {
		return "", err
	}
	target := "refs/heads/" + branch
	for _, ln := range strings.Split(string(packed), "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" || strings.HasPrefix(ln, "#") || strings.HasPrefix(ln, "^") {
			continue
		}
		fields := strings.Fields(ln)
		if len(fields) == 2 && fields[1] == target {
			full := fields[0]
			if len(full) >= 7 {
				return full[:7], nil
			}
			return full, nil
		}
	}
	return "", os.ErrNotExist
}

// Status reports whether the worktree is dirty and, if dirty, whether every
// changed entry is an untracked file. Bare repos always return clean.
func Status(ctx context.Context, p Paths) (dirty, untrackedOnly bool, err error) {
	if p.Bare {
		return false, false, nil
	}
	out, err := runGit(ctx, p.WorktreePath, []string{"status", "--porcelain=v1", "-z"})
	if err != nil {
		return false, false, err
	}
	if out == "" {
		return false, false, nil
	}
	dirty = true
	untrackedOnly = true
	// porcelain v1 with -z is NUL-terminated. Each record's first two bytes
	// are status codes; "??" means untracked.
	records := strings.Split(out, "\x00")
	for _, rec := range records {
		if rec == "" {
			continue
		}
		if len(rec) < 2 || rec[:2] != "??" {
			untrackedOnly = false
			break
		}
	}
	return dirty, untrackedOnly, nil
}

// LastCommit returns the timestamp of HEAD's most recent commit. Empty repos
// return (nil, nil).
func LastCommit(ctx context.Context, p Paths) (*time.Time, error) {
	out, err := runGitFor(ctx, p, []string{"log", "-1", "--format=%ct"})
	if err != nil {
		// `git log` on an empty repo exits non-zero with a recognizable
		// stderr — treat as "no commits yet".
		if isEmptyRepoErr(err) {
			return nil, nil
		}
		return nil, err
	}
	out = strings.TrimSpace(out)
	if out == "" {
		return nil, nil
	}
	secs, parseErr := strconv.ParseInt(out, 10, 64)
	if parseErr != nil {
		return nil, fmt.Errorf("parse log -1 timestamp %q: %w", out, parseErr)
	}
	t := time.Unix(secs, 0).UTC()
	return &t, nil
}

func isEmptyRepoErr(err error) bool {
	var ge *gitError
	if !errors.As(err, &ge) {
		return false
	}
	stderr := strings.ToLower(ge.stderr)
	return strings.Contains(stderr, "does not have any commits") ||
		strings.Contains(stderr, "bad default revision") ||
		strings.Contains(stderr, "ambiguous argument 'head'")
}

// OriginURL returns the configured origin URL (empty when no origin remote is
// configured). Reads via --git-dir against CommonDir so worktrees and bare
// repos return the same value as the primary.
func OriginURL(ctx context.Context, p Paths) (string, error) {
	out, err := runGit(ctx, "", []string{"--git-dir", p.CommonDir, "config", "--get", "remote.origin.url"})
	if err != nil {
		// `git config --get` exits 1 when the key is missing; treat as no
		// origin rather than a failure.
		var ge *gitError
		if errors.As(err, &ge) && ge.exitCode == 1 && ge.stderr == "" {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// DefaultBranch returns the branch name pointed to by
// refs/remotes/origin/HEAD. Local-only — never makes a network call.
// Returns "" when origin/HEAD is not set locally.
func DefaultBranch(_ context.Context, p Paths) (string, error) {
	headPath := filepath.Join(p.CommonDir, "refs", "remotes", "origin", "HEAD")
	data, err := os.ReadFile(headPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", fmt.Errorf("read origin/HEAD: %w", err)
	}
	line := strings.TrimSpace(string(data))
	const prefix = "ref: refs/remotes/origin/"
	if strings.HasPrefix(line, prefix) {
		return strings.TrimPrefix(line, prefix), nil
	}
	// Fallback: packed-refs or unexpected shape.
	return "", nil
}

// ResolveUpstream returns the symbolic full-name path of HEAD's upstream,
// e.g. "refs/remotes/origin/main", relative to CommonDir. Returns ("", nil)
// when no upstream is configured (the most common case for local-only
// branches). Bare repos and empty repos return ("", nil) as well.
//
// The returned path can be stat'd directly under CommonDir for cache
// fingerprinting (see UpstreamRefMtime). Stored on Repo at read time so
// cache.Validate doesn't need to re-shellout to know what to fingerprint.
func ResolveUpstream(ctx context.Context, p Paths) (string, error) {
	out, err := runGitFor(ctx, p, []string{"rev-parse", "--symbolic-full-name", "@{u}"})
	if err != nil {
		// rev-parse @{u} exits non-zero in several "this branch has no
		// usable upstream" states: no upstream configured, an unborn
		// branch on a fresh repo, a detached HEAD, an upstream pointing
		// at a deleted ref, etc. atlas treats them all as "no upstream"
		// — a single benign signal — rather than surfacing an error
		// every user with a local-only branch would hit. Anything truly
		// unexpected (network, malformed git installation) still flows
		// through as an err; the recognized stderr patterns are the
		// cases that legitimately mean "this isn't tracking anything."
		var ge *gitError
		if errors.As(err, &ge) {
			lower := strings.ToLower(ge.stderr)
			switch {
			case strings.Contains(lower, "no upstream"),
				strings.Contains(lower, "no such branch"),
				strings.Contains(lower, "does not point"),
				strings.Contains(lower, "unknown revision"),
				strings.Contains(lower, "ambiguous argument"),
				strings.Contains(lower, "head does not point"),
				strings.Contains(lower, "no such ref"):
				return "", nil
			}
		}
		return "", err
	}
	ref := strings.TrimSpace(out)
	// Defensive: only accept refs/-prefixed values; anything else can't be
	// stat'd under CommonDir and would point at the wrong file.
	if !strings.HasPrefix(ref, "refs/") {
		return "", nil
	}
	return ref, nil
}

// BehindAhead returns the number of commits HEAD is behind and ahead of its
// upstream. Returns (-1, -1, nil) when no upstream is configured, when the
// repo has no commits yet, or for bare repos. Local-only — never fetches.
//
// Callers that have already resolved the upstream (e.g. repo.Read, which
// stores it on the Repo for fingerprinting) should use BehindAheadFor to
// avoid a redundant `git rev-parse @{u}` shellout.
func BehindAhead(ctx context.Context, p Paths) (behind, ahead int, err error) {
	if p.Bare {
		return -1, -1, nil
	}
	upstream, err := ResolveUpstream(ctx, p)
	if err != nil {
		return -1, -1, err
	}
	return BehindAheadFor(ctx, p, upstream)
}

// BehindAheadFor is BehindAhead given a pre-resolved upstream. An empty
// upstream (or bare repo) returns (-1, -1, nil) — the same "no
// upstream" signal callers already expect.
func BehindAheadFor(ctx context.Context, p Paths, upstream string) (behind, ahead int, err error) {
	if p.Bare || upstream == "" {
		return -1, -1, nil
	}
	// rev-list outputs "<behind>\t<ahead>" with --left-right --count when
	// asked as <upstream>...HEAD.
	out, err := runGit(ctx, p.WorktreePath,
		[]string{"rev-list", "--left-right", "--count", upstream + "...HEAD"})
	if err != nil {
		if isEmptyRepoErr(err) {
			return -1, -1, nil
		}
		return -1, -1, err
	}
	fields := strings.Fields(strings.TrimSpace(out))
	if len(fields) != 2 {
		return -1, -1, fmt.Errorf("rev-list: unexpected output %q", out)
	}
	b, err := strconv.Atoi(fields[0])
	if err != nil {
		return -1, -1, fmt.Errorf("rev-list parse behind: %w", err)
	}
	a, err := strconv.Atoi(fields[1])
	if err != nil {
		return -1, -1, fmt.Errorf("rev-list parse ahead: %w", err)
	}
	return b, a, nil
}

// StashCount returns the number of entries in `git stash list`. Bare repos
// return 0.
func StashCount(ctx context.Context, p Paths) (int, error) {
	if p.Bare {
		return 0, nil
	}
	out, err := runGit(ctx, p.WorktreePath, []string{"stash", "list"})
	if err != nil {
		return 0, err
	}
	out = strings.TrimRight(out, "\n")
	if out == "" {
		return 0, nil
	}
	return strings.Count(out, "\n") + 1, nil
}

// BranchCount returns the number of local branches. Works for bare repos
// (which still have refs/heads).
func BranchCount(ctx context.Context, p Paths) (int, error) {
	out, err := runGitFor(ctx, p, []string{"for-each-ref", "--format=%(refname)", "refs/heads/"})
	if err != nil {
		return 0, err
	}
	out = strings.TrimRight(out, "\n")
	if out == "" {
		return 0, nil
	}
	return strings.Count(out, "\n") + 1, nil
}

// CommitsLast30d returns the number of commits HEAD has seen in the last
// 30 days. Empty repos return 0.
func CommitsLast30d(ctx context.Context, p Paths) (int, error) {
	out, err := runGitFor(ctx, p, []string{"rev-list", "--count", "--since=30.days", "HEAD"})
	if err != nil {
		if isEmptyRepoErr(err) {
			return 0, nil
		}
		return 0, err
	}
	out = strings.TrimSpace(out)
	if out == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(out)
	if err != nil {
		return 0, fmt.Errorf("rev-list count parse %q: %w", out, err)
	}
	return n, nil
}

// RecentCommits returns up to n commit subjects starting from HEAD,
// most-recent first. The "no commits to show" case (empty repo, bare
// repo with no commits, n <= 0) returns an *empty but non-nil* slice
// and a nil error, so callers can distinguish "loaded with nothing"
// from "not loaded yet" (nil) without a separate signal. A non-nil
// error means the helper itself failed.
//
// Mirrors LastCommit's normal/worktree/bare branching: bare repos use
// --git-dir directly because they have no worktree to -C into.
func RecentCommits(ctx context.Context, p Paths, n int) ([]string, error) {
	if n <= 0 {
		return []string{}, nil
	}
	out, err := runGitFor(ctx, p, []string{"log", "-n", strconv.Itoa(n), "--format=%s"})
	if err != nil {
		if isEmptyRepoErr(err) {
			return []string{}, nil
		}
		return nil, err
	}
	out = strings.TrimRight(out, "\n")
	if out == "" {
		return []string{}, nil
	}
	return strings.Split(out, "\n"), nil
}

// gitError carries enough context to make exec failures actionable upstream.
type gitError struct {
	args     []string
	exitCode int
	stderr   string
	wrapped  error
}

func (g *gitError) Error() string {
	args := strings.Join(g.args, " ")
	if g.stderr == "" {
		return fmt.Sprintf("git %s: %v", args, g.wrapped)
	}
	return fmt.Sprintf("git %s: %v: %s", args, g.wrapped, strings.TrimSpace(g.stderr))
}

func (g *gitError) Unwrap() error { return g.wrapped }

const (
	gitTimeout    = 5 * time.Second
	gitRetryDelay = 25 * time.Millisecond
)

// runGit shells out to git with a bounded per-attempt timeout. It
// transparently retries once on a signal-killed exit (most often
// "signal: segmentation fault") because git ≥ 2.50 on macOS
// occasionally crashes mid-startup under heavy concurrent fork/exec
// from a Go program — a transient OS-level fluke that has nothing to
// do with the repo state.
//
// Retry is *only* attempted when the kill came from outside our
// timeout machinery. CommandContext also reaps the process via signal
// when its deadline fires, which would look identical to a segfault
// from the outside; attemptGit reports its own deadline-hit so runGit
// can skip the retry in that case. Likewise a cancelled parent ctx
// short-circuits the retry — we shouldn't burn another 5s if the
// caller has already given up.
func runGit(parentCtx context.Context, dir string, args []string) (string, error) {
	if parentCtx == nil {
		parentCtx = context.Background()
	}
	out, err, timedOut := attemptGit(parentCtx, dir, args)
	if !shouldRetryGit(parentCtx, err, timedOut) {
		return out, err
	}
	select {
	case <-parentCtx.Done():
		return out, err
	case <-time.After(gitRetryDelay):
	}
	out, err, _ = attemptGit(parentCtx, dir, args)
	return out, err
}

// runGitFor dispatches a git invocation against p by choosing between
// `-C <worktree>` (non-bare) and `--git-dir <gitdir>` (bare). It exists
// so the half-dozen helpers that take this shape don't each open-code
// the dispatch — and so a future change to the invariant lives in one
// place. Callers that need CommonDir (e.g. OriginURL reads project-wide
// config) or that have already gated on Bare and intentionally use only
// the worktree path should keep calling runGit directly.
func runGitFor(ctx context.Context, p Paths, args []string) (string, error) {
	if p.Bare {
		return runGit(ctx, "", append([]string{"--git-dir", p.GitDir}, args...))
	}
	return runGit(ctx, p.WorktreePath, args)
}

// shouldRetryGit is the retry gate. We retry exactly when:
//   - the previous attempt produced an error,
//   - that error is a signal-kill (segfault and friends),
//   - the per-attempt deadline did NOT fire (otherwise the kill came
//     from CommandContext reaping a hung git, not a transient crash),
//   - and the caller's ctx is still live.
func shouldRetryGit(parentCtx context.Context, err error, timedOut bool) bool {
	if err == nil {
		return false
	}
	if timedOut || parentCtx.Err() != nil {
		return false
	}
	return isTransientSignal(err)
}

// attemptGit runs one git invocation under a per-call timeout derived
// from parentCtx. The returned `timedOut` flag is true iff this
// attempt's deadline (gitTimeout) fired — used by runGit to suppress
// retry on what would otherwise look like a signal-kill error.
func attemptGit(parentCtx context.Context, dir string, args []string) (string, error, bool) {
	ctx, cancel := context.WithTimeout(parentCtx, gitTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", args...)
	if dir != "" {
		cmd.Args = append([]string{"git", "-C", dir}, args...)
	}
	cmd.Stdin = nil
	// Locale-stable output and no TTY-driven prompts.
	// GIT_OPTIONAL_LOCKS=0 keeps observational commands (notably
	// `git status`) from rewriting .git/index on read — preserves
	// the observability-only invariant.
	cmd.Env = append(os.Environ(),
		"LC_ALL=C",
		"GIT_TERMINAL_PROMPT=0",
		"GIT_OPTIONAL_LOCKS=0",
	)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	timedOut := errors.Is(ctx.Err(), context.DeadlineExceeded)
	if err != nil {
		exitCode := -1
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		}
		return stdout.String(), &gitError{
			args:     append([]string(nil), cmd.Args[1:]...),
			exitCode: exitCode,
			stderr:   stderr.String(),
			wrapped:  err,
		}, timedOut
	}
	return stdout.String(), nil, false
}

// isTransientSignal reports whether err looks like git was killed by a
// signal (segfault or similar) rather than exiting cleanly with a
// non-zero status. The caller still has to verify the kill wasn't
// caused by our own timeout/cancel — see runGit.
func isTransientSignal(err error) bool {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return false
	}
	if exitErr.ProcessState == nil {
		return false
	}
	status, ok := exitErr.ProcessState.Sys().(syscall.WaitStatus)
	if !ok {
		return false
	}
	return status.Signaled()
}
