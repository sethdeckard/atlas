// Package gitfixture builds tiny real Git repositories under t.TempDir() for
// tests. Pinned author/committer dates and a stable env make commit SHAs
// deterministic across runs.
package gitfixture

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// FixedTime is the timestamp used for every commit unless overridden via
// WithCommitTime. RFC3339 in UTC.
var FixedTime = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

// Options collects the configurable knobs for Repo. Build via the With*
// helpers, not literals.
type Options struct {
	Branch       string
	Origin       string
	Commits      int
	Dirty        bool
	UntrackedOnly bool
	Detached     bool
	Empty        bool
	Bare         bool
	WorktreeOf   string // path of an existing repo to add a linked worktree to
	WorktreeName string // name for the linked worktree dir under parent's parent
	CommitTime   time.Time
}

// Option mutates Options. Used by Repo as variadic arg.
type Option func(*Options)

func WithBranch(name string) Option       { return func(o *Options) { o.Branch = name } }
func WithOrigin(url string) Option        { return func(o *Options) { o.Origin = url } }
func WithCommits(n int) Option            { return func(o *Options) { o.Commits = n } }
func Dirty() Option                       { return func(o *Options) { o.Dirty = true } }
func UntrackedOnly() Option               { return func(o *Options) { o.UntrackedOnly = true } }
func Detached() Option                    { return func(o *Options) { o.Detached = true } }
func Empty() Option                       { return func(o *Options) { o.Empty = true } }
func Bare() Option                        { return func(o *Options) { o.Bare = true } }
func WithCommitTime(t time.Time) Option   { return func(o *Options) { o.CommitTime = t } }
func WithWorktreeName(name string) Option { return func(o *Options) { o.WorktreeName = name } }
func WorktreeOf(parent string) Option     { return func(o *Options) { o.WorktreeOf = parent } }

// Repo returns the absolute path to a freshly built fixture repo. The repo is
// created under t.TempDir(), so cleanup is automatic.
//
// Defaults: branch "main", one commit, no dirty state, no origin.
func Repo(t *testing.T, opts ...Option) string {
	t.Helper()
	o := Options{
		Branch:     "main",
		Commits:    1,
		CommitTime: FixedTime,
	}
	for _, opt := range opts {
		opt(&o)
	}

	dir := t.TempDir()

	if o.WorktreeOf != "" {
		path := addWorktree(t, o.WorktreeOf, dir, &o)
		return path
	}

	if o.Bare {
		mustGit(t, dir, "", "init", "--bare", "--initial-branch="+o.Branch)
		if o.Origin != "" {
			mustGit(t, dir, "", "config", "remote.origin.url", o.Origin)
		}
		return dir
	}

	mustGit(t, dir, "", "init", "--initial-branch="+o.Branch)
	mustGit(t, dir, "", "config", "user.name", "Atlas Test")
	mustGit(t, dir, "", "config", "user.email", "atlas-test@example.com")
	mustGit(t, dir, "", "config", "commit.gpgsign", "false")

	if o.Origin != "" {
		mustGit(t, dir, "", "remote", "add", "origin", o.Origin)
	}

	if o.Empty {
		// No commits.
		return dir
	}

	for i := 0; i < o.Commits; i++ {
		filename := filepath.Join(dir, fmt.Sprintf("file-%d.txt", i+1))
		if err := os.WriteFile(filename, []byte(fmt.Sprintf("content %d\n", i+1)), 0o644); err != nil {
			t.Fatalf("gitfixture: write %s: %v", filename, err)
		}
		mustGit(t, dir, "", "add", filename)
		commitTime := o.CommitTime.Add(time.Duration(i) * time.Hour).UTC().Format(time.RFC3339)
		extraEnv := []string{
			"GIT_AUTHOR_DATE=" + commitTime,
			"GIT_COMMITTER_DATE=" + commitTime,
		}
		mustGitEnv(t, dir, extraEnv, "commit", "-m", fmt.Sprintf("commit %d", i+1))
	}

	if o.Detached {
		out, err := runGit(dir, nil, "rev-parse", "HEAD")
		if err != nil {
			t.Fatalf("gitfixture: rev-parse HEAD: %v", err)
		}
		sha := trim(out)
		mustGit(t, dir, "", "checkout", "--detach", sha)
	}

	if o.Dirty || o.UntrackedOnly {
		extraName := "extra.txt"
		if o.UntrackedOnly {
			// Untracked files only — write but don't add.
			if err := os.WriteFile(filepath.Join(dir, extraName), []byte("untracked\n"), 0o644); err != nil {
				t.Fatalf("gitfixture: write untracked: %v", err)
			}
		} else {
			// Modify a tracked file.
			tracked := filepath.Join(dir, "file-1.txt")
			if err := os.WriteFile(tracked, []byte("dirty\n"), 0o644); err != nil {
				t.Fatalf("gitfixture: dirty write: %v", err)
			}
		}
	}

	return dir
}

func addWorktree(t *testing.T, parent, scratchDir string, o *Options) string {
	t.Helper()
	name := o.WorktreeName
	if name == "" {
		name = "wt-" + filepath.Base(scratchDir)
	}
	// Worktrees get a branch named after themselves so the parent's default
	// branch (typically "main") doesn't collide.
	path := filepath.Join(scratchDir, name)
	mustGit(t, parent, "", "worktree", "add", "-b", name, path)
	return path
}

func mustGit(t *testing.T, dir string, _ string, args ...string) {
	t.Helper()
	if _, err := runGit(dir, nil, args...); err != nil {
		t.Fatalf("gitfixture: git -C %s %v: %v", dir, args, err)
	}
}

func mustGitEnv(t *testing.T, dir string, extraEnv []string, args ...string) {
	t.Helper()
	if _, err := runGit(dir, extraEnv, args...); err != nil {
		t.Fatalf("gitfixture: git -C %s %v: %v", dir, args, err)
	}
}

func runGit(dir string, extraEnv []string, args ...string) (string, error) {
	cmdArgs := append([]string{"-C", dir}, args...)
	cmd := exec.Command("git", cmdArgs...)
	cmd.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"LC_ALL=C",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
	)
	cmd.Env = append(cmd.Env, extraEnv...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return string(out), fmt.Errorf("%w: %s", err, string(out))
		}
		return string(out), err
	}
	return string(out), nil
}

func trim(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r' || s[len(s)-1] == ' ') {
		s = s[:len(s)-1]
	}
	return s
}
