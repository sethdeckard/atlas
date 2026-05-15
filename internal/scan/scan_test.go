package scan_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"testing"

	"github.com/sethdeckard/atlas/internal/gitfixture"
	"github.com/sethdeckard/atlas/internal/scan"
)

func TestDiscover_FindsRepoAtRoot(t *testing.T) {
	dir := gitfixture.Repo(t)
	got, err := scan.Discover(context.Background(), dir, scan.Options{})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	want := []string{mustEvalSymlinks(t, dir)}
	gotResolved := mustEvalAll(t, got)
	if !equal(gotResolved, want) {
		t.Errorf("Discover = %v; want %v", gotResolved, want)
	}
}

func TestDiscover_NestedRepoOuterWins(t *testing.T) {
	parent := t.TempDir()
	outer := filepath.Join(parent, "outer")
	mustMkdir(t, outer)
	if err := os.WriteFile(filepath.Join(outer, "stub.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Build outer as a real repo by initializing in place.
	gitInit(t, outer)
	// Now nest a separate repo inside.
	inner := filepath.Join(outer, "vendored")
	mustMkdir(t, inner)
	gitInit(t, inner)

	got, err := scan.Discover(context.Background(), parent, scan.Options{})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	gotResolved := mustEvalAll(t, got)
	want := []string{mustEvalSymlinks(t, outer)}
	if !equal(gotResolved, want) {
		t.Errorf("expected only outer repo; got %v", gotResolved)
	}
}

func TestDiscover_HonorsSkipDirs(t *testing.T) {
	parent := t.TempDir()
	repo := filepath.Join(parent, "node_modules", "deep", "buried")
	mustMkdir(t, repo)
	gitInit(t, repo)

	got, _ := scan.Discover(context.Background(), parent, scan.Options{
		SkipBaseNames: map[string]struct{}{"node_modules": {}},
	})
	if len(got) != 0 {
		t.Errorf("expected no repos discovered (skipped); got %v", got)
	}
}

// TestDiscover_HonorsAbsPathSkips covers the home-anchored / absolute-path
// skip mechanism: a directory matched by SkipAbsPaths is skipped, but a
// like-named sibling at a different absolute path is not. This is what
// keeps `~/Pictures` from over-matching `<some-root>/Pictures/`.
func TestDiscover_HonorsAbsPathSkips(t *testing.T) {
	root := t.TempDir()
	skipped := filepath.Join(root, "Pictures")
	kept := filepath.Join(root, "elsewhere", "Pictures")
	for _, dir := range []string{skipped, kept} {
		mustMkdir(t, filepath.Join(dir, "repo"))
		gitInit(t, filepath.Join(dir, "repo"))
	}

	skippedAbs, _ := filepath.Abs(skipped)
	got, _ := scan.Discover(context.Background(), root, scan.Options{
		SkipAbsPaths: map[string]struct{}{skippedAbs: {}},
	})
	gotResolved := mustEvalAll(t, got)
	wantRepo := mustEvalSymlinks(t, filepath.Join(kept, "repo"))
	if len(gotResolved) != 1 || gotResolved[0] != wantRepo {
		t.Errorf("expected only %s discovered; got %v", wantRepo, gotResolved)
	}
}

func TestDiscover_HonorsMaxDepth(t *testing.T) {
	parent := t.TempDir()
	deep := filepath.Join(parent, "a", "b", "c", "d", "repo")
	mustMkdir(t, deep)
	gitInit(t, deep)

	// Depth 3 < 5, should not find.
	got, _ := scan.Discover(context.Background(), parent, scan.Options{MaxDepth: 3})
	if len(got) != 0 {
		t.Errorf("expected no repos at MaxDepth=3; got %v", got)
	}

	// Depth 6 ≥ 5, should find.
	got, _ = scan.Discover(context.Background(), parent, scan.Options{MaxDepth: 6})
	if len(got) != 1 {
		t.Errorf("expected 1 repo at MaxDepth=6; got %v", got)
	}
}

func TestDiscover_DoesNotFollowSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink behavior")
	}
	parent := t.TempDir()
	real := gitfixture.Repo(t)
	link := filepath.Join(parent, "link-to-repo")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("cannot create symlink: %v", err)
	}
	got, _ := scan.Discover(context.Background(), parent, scan.Options{})
	if len(got) != 0 {
		t.Errorf("expected no repos via symlink; got %v", got)
	}
}

func TestDiscover_DetectsBare(t *testing.T) {
	parent := t.TempDir()
	bareDir := filepath.Join(parent, "thing.git")
	mustMkdir(t, bareDir)
	gitInitBare(t, bareDir)

	got, err := scan.Discover(context.Background(), parent, scan.Options{})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 bare repo; got %v", got)
	}
	gotResolved := mustEvalSymlinks(t, got[0])
	wantResolved := mustEvalSymlinks(t, bareDir)
	if gotResolved != wantResolved {
		t.Errorf("bare repo path = %s; want %s", gotResolved, wantResolved)
	}
}

func TestDiscover_DetectsLinkedWorktree(t *testing.T) {
	parent := t.TempDir()
	primary := filepath.Join(parent, "primary")
	mustMkdir(t, primary)
	gitInit(t, primary)
	// Make at least one commit so worktree add can succeed.
	mustCommitFile(t, primary, "a.txt", "1")
	wt := filepath.Join(parent, "wt-feature")
	mustGitC(t, primary, "worktree", "add", "-b", "feature", wt)

	got, err := scan.Discover(context.Background(), parent, scan.Options{})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	gotResolved := mustEvalAll(t, got)
	want := []string{
		mustEvalSymlinks(t, primary),
		mustEvalSymlinks(t, wt),
	}
	sort.Strings(gotResolved)
	sort.Strings(want)
	if !equal(gotResolved, want) {
		t.Errorf("got %v; want %v", gotResolved, want)
	}
}

func TestDiscover_PermissionDeniedNoAbort(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission semantics")
	}
	parent := t.TempDir()
	good := filepath.Join(parent, "good")
	mustMkdir(t, good)
	gitInit(t, good)

	bad := filepath.Join(parent, "bad")
	mustMkdir(t, bad)
	if err := os.Chmod(bad, 0); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(bad, 0o755) })

	got, err := scan.Discover(context.Background(), parent, scan.Options{})
	if len(got) != 1 {
		t.Errorf("expected 1 repo found despite permission error; got %v", got)
	}
	if err == nil {
		t.Errorf("expected non-nil error to signal partial failure")
	}
	if err != nil && !errors.Is(err, os.ErrPermission) && !hasPermissionError(err) {
		// Walk errors might be wrapped; just confirm err != nil.
		t.Logf("walk error: %v", err)
	}
}

// TestDiscover_HonorsContextCancel guards the SIGINT-during-pipe path:
// even on a tree wide enough that a synchronous walk would visit hundreds
// of entries, an already-cancelled ctx must terminate Discover almost
// immediately and surface ctx.Err() in the returned error.
func TestDiscover_HonorsContextCancel(t *testing.T) {
	parent := t.TempDir()
	for i := 0; i < 25; i++ {
		for j := 0; j < 25; j++ {
			mustMkdir(t, filepath.Join(parent, fmt.Sprintf("d%02d", i), fmt.Sprintf("e%02d", j)))
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	paths, err := scan.Discover(ctx, parent, scan.Options{})
	if err == nil {
		t.Errorf("expected ctx error from cancelled Discover; got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled in returned error chain; got %v", err)
	}
	if len(paths) > 5 {
		t.Errorf("cancelled Discover should walk very little; got %d paths", len(paths))
	}
}

// ---- helpers ----

func mustMkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", p, err)
	}
}

func gitInit(t *testing.T, dir string) {
	t.Helper()
	mustGitC(t, dir, "init", "--initial-branch=main")
	mustGitC(t, dir, "config", "user.name", "Atlas Test")
	mustGitC(t, dir, "config", "user.email", "atlas-test@example.com")
}

func gitInitBare(t *testing.T, dir string) {
	t.Helper()
	mustGitC(t, dir, "init", "--bare", "--initial-branch=main")
}

func mustCommitFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	mustGitC(t, dir, "add", name)
	mustGitC(t, dir, "commit", "-m", "init")
}

func mustGitC(t *testing.T, dir string, args ...string) {
	t.Helper()
	allArgs := append([]string{"-C", dir}, args...)
	cmd := exec.Command("git", allArgs...)
	cmd.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"LC_ALL=C",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func mustEvalSymlinks(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		return p
	}
	return r
}

func mustEvalAll(t *testing.T, paths []string) []string {
	t.Helper()
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		out = append(out, mustEvalSymlinks(t, p))
	}
	return out
}

func hasPermissionError(err error) bool {
	if err == nil {
		return false
	}
	for _, sub := range unwrapAll(err) {
		if errors.Is(sub, os.ErrPermission) {
			return true
		}
	}
	return false
}

func unwrapAll(err error) []error {
	type unwrapper interface{ Unwrap() []error }
	if u, ok := err.(unwrapper); ok {
		return u.Unwrap()
	}
	if e := errors.Unwrap(err); e != nil {
		return []error{e}
	}
	return []error{err}
}
