package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sethdeckard/atlas/internal/cli"
	"github.com/sethdeckard/atlas/internal/gitfixture"
)

func init() {
	// Make relative-time formatting deterministic in tests.
	cli.SetNowFunc(func() time.Time {
		// 30 days after FixedTime so single-commit fixtures show "30d ago".
		return gitfixture.FixedTime.Add(30 * 24 * time.Hour)
	})
}

func TestList_TableFormat(t *testing.T) {
	tree, _ := buildTreeA(t)
	defer cleanupCacheEnv(t)

	cmd := cli.NewListCommand()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{tree, "--format=table", "--sort=repo", "--reverse=false"})
	cmd.SetContext(context.Background())

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	got := out.String()
	for _, name := range []string{"alpha", "beta", "gamma"} {
		if !strings.Contains(got, name) {
			t.Errorf("expected table to contain %q; got:\n%s", name, got)
		}
	}
	if !strings.Contains(got, "repo") || !strings.Contains(got, "branch") {
		t.Errorf("expected header row; got:\n%s", got)
	}
}

func TestList_NameFormat(t *testing.T) {
	tree, repos := buildTreeA(t)
	defer cleanupCacheEnv(t)

	cmd := cli.NewListCommand()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{tree, "--format=name", "--sort=repo"})
	cmd.SetContext(context.Background())

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != len(repos) {
		t.Errorf("expected %d lines; got %d (%v)", len(repos), len(lines), lines)
	}
	for _, line := range lines {
		if !strings.HasPrefix(line, tree) {
			t.Errorf("expected line under %s; got %q", tree, line)
		}
	}
}

func TestList_JSONFormat(t *testing.T) {
	tree, _ := buildTreeA(t)
	defer cleanupCacheEnv(t)

	cmd := cli.NewListCommand()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{tree, "--format=json", "--sort=repo"})
	cmd.SetContext(context.Background())

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	var rows []map[string]any
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out.String())
	}
	if len(rows) != 3 {
		t.Errorf("expected 3 rows; got %d", len(rows))
	}
}

func TestList_DirtyFilter(t *testing.T) {
	tree, _ := buildTreeA(t)
	defer cleanupCacheEnv(t)

	cmd := cli.NewListCommand()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{tree, "--format=name", "--dirty"})
	cmd.SetContext(context.Background())

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 1 {
		t.Errorf("expected exactly 1 dirty repo; got %v", lines)
	}
	if !strings.HasSuffix(lines[0], "beta") {
		t.Errorf("expected dirty repo to be 'beta'; got %q", lines[0])
	}
}

// TestList_DetectsWorktreeEditOnWarmRun guards against a bug where a plain
// worktree edit (no add/commit) goes unnoticed on a warm list run because the
// stat-based mtime fingerprints (HEAD/index/config) don't change. The fix is
// a per-repo `git status` refresh during validation.
func TestList_DetectsWorktreeEditOnWarmRun(t *testing.T) {
	defer cleanupCacheEnv(t)

	root := t.TempDir()
	clean := filepath.Join(root, "clean")
	mustMkdir(t, clean)
	gitInit(t, clean)
	mustCommit(t, clean, "a.txt", "original")

	// First list — populates cache; clean repo, no dirty entries.
	first := runListCmd(t, root, "--format=name", "--dirty")
	if strings.TrimSpace(first) != "" {
		t.Fatalf("expected no dirty repos initially; got %q", first)
	}

	// Modify a tracked file without staging it. This bumps the file's mtime
	// but does not bump .git/HEAD, .git/index, or .git/config.
	if err := os.WriteFile(filepath.Join(clean, "a.txt"), []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Second list (warm) — should now reflect the dirty state.
	second := runListCmd(t, root, "--format=name", "--dirty")
	got := strings.TrimSpace(second)
	if got != clean {
		t.Errorf("expected %q to be reported as dirty on warm run; got %q", clean, got)
	}
}

// TestList_ReverseInvertsConfiguredOrder guards against a regression where
// --reverse was OR'd with the configured descending order, making it a no-op
// when the default order was already desc.
func TestList_ReverseInvertsConfiguredOrder(t *testing.T) {
	defer cleanupCacheEnv(t)

	root := t.TempDir()
	older := filepath.Join(root, "old")
	mustMkdir(t, older)
	gitInit(t, older)
	mustCommitAt(t, older, "x.txt", "x", gitfixture.FixedTime)

	newer := filepath.Join(root, "new")
	mustMkdir(t, newer)
	gitInit(t, newer)
	mustCommitAt(t, newer, "y.txt", "y", gitfixture.FixedTime.Add(48*time.Hour))

	// Default sort = last_commit_at desc → newer first.
	defaultOrder := strings.TrimSpace(runListCmd(t, root, "--format=name"))
	wantDefault := newer + "\n" + older
	if defaultOrder != wantDefault {
		t.Fatalf("default order: got %q; want %q", defaultOrder, wantDefault)
	}

	// --reverse should invert → older first.
	reversed := strings.TrimSpace(runListCmd(t, root, "--format=name", "--reverse"))
	wantReversed := older + "\n" + newer
	if reversed != wantReversed {
		t.Fatalf("reversed order: got %q; want %q", reversed, wantReversed)
	}
}

func runListCmd(t *testing.T, args ...string) string {
	t.Helper()
	cmd := cli.NewListCommand()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs(args)
	cmd.SetContext(context.Background())
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute %v: %v", args, err)
	}
	return out.String()
}

func mustCommitAt(t *testing.T, dir, name, content string, when time.Time) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	gitC(t, dir, "add", name)
	stamp := when.UTC().Format(time.RFC3339)
	gitCEnv(t, dir, []string{
		"GIT_AUTHOR_DATE=" + stamp,
		"GIT_COMMITTER_DATE=" + stamp,
	}, "commit", "-m", "init")
}

// TestList_LanguageFilter verifies the M4 --language flag narrows to
// repos whose detected manifests include the requested language. The
// match is case-insensitive.
func TestList_LanguageFilter(t *testing.T) {
	root := t.TempDir()
	defer cleanupCacheEnv(t)

	goRepo := filepath.Join(root, "go-svc")
	mustMkdir(t, goRepo)
	gitInit(t, goRepo)
	mustCommit(t, goRepo, "go.mod", "module x\ngo 1.22\n")

	rubyRepo := filepath.Join(root, "ruby-svc")
	mustMkdir(t, rubyRepo)
	gitInit(t, rubyRepo)
	mustCommit(t, rubyRepo, "Gemfile", "source 'https://rubygems.org'\n")

	cmd := cli.NewListCommand()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{root, "--format=name", "--language=go"})
	cmd.SetContext(context.Background())
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "go-svc") {
		t.Errorf("expected go-svc in output; got:\n%s", got)
	}
	if strings.Contains(got, "ruby-svc") {
		t.Errorf("ruby-svc should be filtered out; got:\n%s", got)
	}

	// Case-insensitive: GO matches `go`.
	cmd = cli.NewListCommand()
	out.Reset()
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{root, "--format=name", "--language=GO"})
	cmd.SetContext(context.Background())
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute (caps): %v", err)
	}
	if !strings.Contains(out.String(), "go-svc") {
		t.Errorf("--language=GO should still match; got:\n%s", out.String())
	}
}

// TestList_CachedSurfacesPersistedDerivedSignals guards the contract
// that --cached can render last-known derived signals (Languages,
// StashCount, BranchCount, CommitsLast30d) without running the warm
// status pass. The first invocation is a normal warm read that
// populates the cache; the second is --cached and must still satisfy
// --language=go off the persisted Languages slice.
func TestList_CachedSurfacesPersistedDerivedSignals(t *testing.T) {
	root := t.TempDir()
	defer cleanupCacheEnv(t)

	goRepo := filepath.Join(root, "go-svc")
	mustMkdir(t, goRepo)
	gitInit(t, goRepo)
	mustCommit(t, goRepo, "go.mod", "module x\ngo 1.22\n")

	rubyRepo := filepath.Join(root, "ruby-svc")
	mustMkdir(t, rubyRepo)
	gitInit(t, rubyRepo)
	mustCommit(t, rubyRepo, "Gemfile", "source 'https://rubygems.org'\n")

	// Warm run: populates cache with Languages set per repo.
	warm := cli.NewListCommand()
	warm.SetOut(&bytes.Buffer{})
	warm.SetErr(&bytes.Buffer{})
	warm.SetArgs([]string{root, "--format=name"})
	warm.SetContext(context.Background())
	if err := warm.Execute(); err != nil {
		t.Fatalf("warm: %v", err)
	}

	// --cached run with --language=go: must come back with the Go repo
	// only. If --cached zeroed the persisted Languages slice this would
	// return zero results.
	cached := cli.NewListCommand()
	out := &bytes.Buffer{}
	cached.SetOut(out)
	cached.SetErr(&bytes.Buffer{})
	cached.SetArgs([]string{root, "--format=name", "--cached", "--language=go"})
	cached.SetContext(context.Background())
	if err := cached.Execute(); err != nil {
		t.Fatalf("cached: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "go-svc") {
		t.Errorf("--cached should still surface go-svc via persisted Languages; got:\n%s", got)
	}
	if strings.Contains(got, "ruby-svc") {
		t.Errorf("ruby-svc should be filtered out; got:\n%s", got)
	}
}

// buildTreeA builds three repos under a tmp tree: alpha (clean), beta
// (dirty), gamma (clean). Returns the root and the list of repo paths.
func buildTreeA(t *testing.T) (string, []string) {
	t.Helper()
	root := t.TempDir()

	alpha := filepath.Join(root, "alpha")
	mustMkdir(t, alpha)
	gitInit(t, alpha)
	mustCommit(t, alpha, "a.txt", "hello")

	beta := filepath.Join(root, "beta")
	mustMkdir(t, beta)
	gitInit(t, beta)
	mustCommit(t, beta, "b.txt", "hello")
	// Make beta dirty by modifying the tracked file.
	if err := os.WriteFile(filepath.Join(beta, "b.txt"), []byte("dirty"), 0o644); err != nil {
		t.Fatal(err)
	}

	gamma := filepath.Join(root, "gamma")
	mustMkdir(t, gamma)
	gitInit(t, gamma)
	mustCommit(t, gamma, "c.txt", "hello")

	return root, []string{alpha, beta, gamma}
}

// cleanupCacheEnv gives the calling test a private XDG cache + config
// dir for the duration of the test, isolated from every other test in
// the package. Earlier this helper only restored env vars after the
// test ran, which meant every test shared the package-level
// XDG_CACHE_HOME from TestMain — a future test could read another
// test's leftover cache.json. Per-test isolation is the correct
// contract the helper's name implied.
func cleanupCacheEnv(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	prevCache := os.Getenv("XDG_CACHE_HOME")
	prevConfig := os.Getenv("XDG_CONFIG_HOME")
	if err := os.Setenv("XDG_CACHE_HOME", dir); err != nil {
		t.Fatalf("set XDG_CACHE_HOME: %v", err)
	}
	if err := os.Setenv("XDG_CONFIG_HOME", dir); err != nil {
		t.Fatalf("set XDG_CONFIG_HOME: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Setenv("XDG_CACHE_HOME", prevCache)
		_ = os.Setenv("XDG_CONFIG_HOME", prevConfig)
	})
}

func TestMain(m *testing.M) {
	// Default XDG dirs for any test that forgets to call
	// cleanupCacheEnv. Per-test isolation is preferred (and what the
	// helper provides); this just keeps a stray test from polluting
	// the user's real ~/.cache or ~/.config when run via `go test`
	// directly.
	tmp, err := os.MkdirTemp("", "atlas-cli-tests-")
	if err != nil {
		panic(err)
	}
	_ = os.Setenv("XDG_CACHE_HOME", tmp)
	_ = os.Setenv("XDG_CONFIG_HOME", tmp)
	code := m.Run()
	_ = os.RemoveAll(tmp)
	os.Exit(code)
}

// ---- helpers shared via test-only file functions ----

func mustMkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
}

func mustCommit(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	gitC(t, dir, "add", name)
	commitTime := gitfixture.FixedTime.UTC().Format(time.RFC3339)
	gitCEnv(t, dir, []string{
		"GIT_AUTHOR_DATE=" + commitTime,
		"GIT_COMMITTER_DATE=" + commitTime,
	}, "commit", "-m", "init")
}

func gitInit(t *testing.T, dir string) {
	t.Helper()
	gitC(t, dir, "init", "--initial-branch=main")
	gitC(t, dir, "config", "user.name", "Atlas Test")
	gitC(t, dir, "config", "user.email", "atlas-test@example.com")
}

func gitC(t *testing.T, dir string, args ...string) {
	t.Helper()
	gitCEnv(t, dir, nil, args...)
}

func gitCEnv(t *testing.T, dir string, extraEnv []string, args ...string) {
	t.Helper()
	all := append([]string{"-C", dir}, args...)
	cmd := exec.Command("git", all...)
	cmd.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"LC_ALL=C",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
	)
	cmd.Env = append(cmd.Env, extraEnv...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}
