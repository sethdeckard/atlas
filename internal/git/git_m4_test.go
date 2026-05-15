package git_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sethdeckard/atlas/internal/git"
	"github.com/sethdeckard/atlas/internal/gitfixture"
)

// resolveTest is a tiny helper that wraps git.ResolvePaths for tests.
func resolveTest(t *testing.T, dir string) git.Paths {
	t.Helper()
	p, err := git.ResolvePaths(dir)
	if err != nil {
		t.Fatalf("ResolvePaths: %v", err)
	}
	return p
}

// run shells `git` against a fixture in tests; mirrors the helper used by
// other test files.
func run(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
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

// setupRemoteAndLocal builds a bare "remote" plus a normal "local" whose
// origin tracks the bare. Returns (localPath, remotePath). Initial commit
// is in place on the local AND pushed so refs/remotes/origin/main exists.
func setupRemoteAndLocal(t *testing.T) (string, string) {
	t.Helper()
	parent := t.TempDir()
	remote := filepath.Join(parent, "origin.git")
	if err := os.MkdirAll(remote, 0o755); err != nil {
		t.Fatal(err)
	}
	run(t, remote, "init", "--bare", "--initial-branch=main")

	local := filepath.Join(parent, "local")
	if err := os.MkdirAll(local, 0o755); err != nil {
		t.Fatal(err)
	}
	run(t, local, "init", "--initial-branch=main")
	run(t, local, "config", "user.name", "Atlas Test")
	run(t, local, "config", "user.email", "atlas@test")
	if err := os.WriteFile(filepath.Join(local, "f"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, local, "add", "f")
	run(t, local, "commit", "-m", "init")
	run(t, local, "remote", "add", "origin", remote)
	run(t, local, "push", "-u", "origin", "main")
	return local, remote
}

func TestResolveUpstream_NoUpstream(t *testing.T) {
	dir := gitfixture.Repo(t)
	p := resolveTest(t, dir)
	got, err := git.ResolveUpstream(context.Background(), p)
	if err != nil {
		t.Fatalf("ResolveUpstream: %v", err)
	}
	if got != "" {
		t.Errorf("expected empty UpstreamRef; got %q", got)
	}
}

func TestResolveUpstream_HasUpstream(t *testing.T) {
	local, _ := setupRemoteAndLocal(t)
	p := resolveTest(t, local)
	got, err := git.ResolveUpstream(context.Background(), p)
	if err != nil {
		t.Fatalf("ResolveUpstream: %v", err)
	}
	want := "refs/remotes/origin/main"
	if got != want {
		t.Errorf("got %q; want %q", got, want)
	}
	// And the file we'd want to fingerprint must exist.
	if _, err := os.Stat(filepath.Join(p.CommonDir, got)); err != nil {
		t.Errorf("fingerprintable upstream-ref file should exist: %v", err)
	}
}

func TestBehindAhead_NoUpstream(t *testing.T) {
	dir := gitfixture.Repo(t)
	p := resolveTest(t, dir)
	behind, ahead, err := git.BehindAhead(context.Background(), p)
	if err != nil {
		t.Fatalf("BehindAhead: %v", err)
	}
	if behind != -1 || ahead != -1 {
		t.Errorf("got behind=%d ahead=%d; want -1/-1 for no-upstream", behind, ahead)
	}
}

func TestBehindAhead_AheadOnly(t *testing.T) {
	local, _ := setupRemoteAndLocal(t)
	if err := os.WriteFile(filepath.Join(local, "f"), []byte("b"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, local, "add", "f")
	run(t, local, "commit", "-m", "second")
	run(t, local, "config", "user.name", "Atlas Test")

	p := resolveTest(t, local)
	behind, ahead, err := git.BehindAhead(context.Background(), p)
	if err != nil {
		t.Fatalf("BehindAhead: %v", err)
	}
	if behind != 0 || ahead != 1 {
		t.Errorf("got behind=%d ahead=%d; want 0/1", behind, ahead)
	}
}

func TestBehindAheadFor_EmptyUpstreamShortCircuits(t *testing.T) {
	// Contract: BehindAheadFor with a pre-resolved empty upstream must
	// not shell out to rev-list — it returns the same (-1, -1, nil)
	// "no upstream" signal callers expect. Any non-nil err here would
	// imply we attempted the rev-list invocation.
	dir := gitfixture.Repo(t)
	p := resolveTest(t, dir)
	behind, ahead, err := git.BehindAheadFor(context.Background(), p, "")
	if err != nil {
		t.Fatalf("BehindAheadFor(empty): %v", err)
	}
	if behind != -1 || ahead != -1 {
		t.Errorf("got behind=%d ahead=%d; want -1/-1 for empty upstream", behind, ahead)
	}
}

func TestBehindAheadFor_BareShortCircuits(t *testing.T) {
	dir := gitfixture.Repo(t, gitfixture.Bare())
	p := resolveTest(t, dir)
	behind, ahead, err := git.BehindAheadFor(context.Background(), p, "refs/remotes/origin/main")
	if err != nil {
		t.Fatalf("BehindAheadFor(bare): %v", err)
	}
	if behind != -1 || ahead != -1 {
		t.Errorf("got behind=%d ahead=%d; want -1/-1 for bare", behind, ahead)
	}
}

func TestBehindAheadFor_WithResolvedUpstream(t *testing.T) {
	// End-to-end: ResolveUpstream once, then BehindAheadFor with the
	// resolved value. Mirrors the call shape repo.Read now uses.
	local, _ := setupRemoteAndLocal(t)
	if err := os.WriteFile(filepath.Join(local, "f"), []byte("b"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, local, "add", "f")
	run(t, local, "commit", "-m", "second")
	p := resolveTest(t, local)
	upstream, err := git.ResolveUpstream(context.Background(), p)
	if err != nil {
		t.Fatalf("ResolveUpstream: %v", err)
	}
	behind, ahead, err := git.BehindAheadFor(context.Background(), p, upstream)
	if err != nil {
		t.Fatalf("BehindAheadFor: %v", err)
	}
	if behind != 0 || ahead != 1 {
		t.Errorf("got behind=%d ahead=%d; want 0/1", behind, ahead)
	}
}

func TestStashCount(t *testing.T) {
	dir := gitfixture.Repo(t)
	p := resolveTest(t, dir)
	if n, err := git.StashCount(context.Background(), p); err != nil || n != 0 {
		t.Errorf("clean repo: got n=%d err=%v; want 0/nil", n, err)
	}
	// Create a stashable change.
	if err := os.WriteFile(filepath.Join(dir, "stashed.txt"), []byte("wip"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, dir, "add", "stashed.txt")
	run(t, dir, "stash", "push", "-m", "wip-1")
	if n, err := git.StashCount(context.Background(), p); err != nil || n != 1 {
		t.Errorf("after one stash: got n=%d err=%v; want 1/nil", n, err)
	}

	// Second stash.
	if err := os.WriteFile(filepath.Join(dir, "more.txt"), []byte("wip2"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, dir, "add", "more.txt")
	run(t, dir, "stash", "push", "-m", "wip-2")
	if n, err := git.StashCount(context.Background(), p); err != nil || n != 2 {
		t.Errorf("after two stashes: got n=%d err=%v; want 2/nil", n, err)
	}
}

func TestBranchCount(t *testing.T) {
	dir := gitfixture.Repo(t)
	p := resolveTest(t, dir)
	if n, err := git.BranchCount(context.Background(), p); err != nil || n != 1 {
		t.Errorf("default: got n=%d err=%v; want 1/nil", n, err)
	}
	run(t, dir, "branch", "feature-x")
	run(t, dir, "branch", "feature-y")
	if n, err := git.BranchCount(context.Background(), p); err != nil || n != 3 {
		t.Errorf("after two branches: got n=%d err=%v; want 3/nil", n, err)
	}
}

func TestCommitsLast30d_RecentCommits(t *testing.T) {
	// gitfixture defaults to a fixed date (Jan 1 2026) which is well
	// outside `--since=30.days` from real wall time. Override with a
	// timestamp that is one day old relative to now so the helper
	// reliably counts these commits.
	recent := time.Now().Add(-24 * time.Hour)
	dir := gitfixture.Repo(t, gitfixture.WithCommits(3), gitfixture.WithCommitTime(recent))
	p := resolveTest(t, dir)
	n, err := git.CommitsLast30d(context.Background(), p)
	if err != nil {
		t.Fatalf("CommitsLast30d: %v", err)
	}
	if n != 3 {
		t.Errorf("got %d commits in last 30d; want 3", n)
	}
}

func TestCommitsLast30d_OldCommitsNotCounted(t *testing.T) {
	// Pin commits to the gitfixture default (Jan 1 2026) — far older
	// than 30 days from current wall time → should count 0.
	dir := gitfixture.Repo(t, gitfixture.WithCommits(2))
	p := resolveTest(t, dir)
	n, err := git.CommitsLast30d(context.Background(), p)
	if err != nil {
		t.Fatalf("CommitsLast30d: %v", err)
	}
	if n != 0 {
		t.Errorf("old commits should not count; got %d", n)
	}
}

func TestCommitsLast30d_EmptyRepo(t *testing.T) {
	dir := gitfixture.Repo(t, gitfixture.Empty())
	p := resolveTest(t, dir)
	n, err := git.CommitsLast30d(context.Background(), p)
	if err != nil {
		t.Fatalf("CommitsLast30d: %v", err)
	}
	if n != 0 {
		t.Errorf("empty repo: got n=%d; want 0", n)
	}
}

func TestStashCount_BareRepo(t *testing.T) {
	dir := gitfixture.Repo(t, gitfixture.Bare())
	p := resolveTest(t, dir)
	if n, err := git.StashCount(context.Background(), p); err != nil || n != 0 {
		t.Errorf("bare: got n=%d err=%v; want 0/nil", n, err)
	}
	if behind, ahead, err := git.BehindAhead(context.Background(), p); err != nil || behind != -1 || ahead != -1 {
		t.Errorf("bare BehindAhead: got behind=%d ahead=%d err=%v; want -1/-1/nil", behind, ahead, err)
	}
}

func TestRecentCommits_NormalRepo(t *testing.T) {
	dir := gitfixture.Repo(t, gitfixture.WithCommits(3))
	p := resolveTest(t, dir)
	got, err := git.RecentCommits(context.Background(), p, 5)
	if err != nil {
		t.Fatalf("RecentCommits: %v", err)
	}
	if len(got) != 3 {
		t.Errorf("got %d commits; want 3", len(got))
	}
}

func TestRecentCommits_LimitsCount(t *testing.T) {
	dir := gitfixture.Repo(t, gitfixture.WithCommits(5))
	p := resolveTest(t, dir)
	got, err := git.RecentCommits(context.Background(), p, 2)
	if err != nil {
		t.Fatalf("RecentCommits: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("got %d commits; want 2", len(got))
	}
}

func TestRecentCommits_EmptyRepoReturnsEmptySlice(t *testing.T) {
	// Contract: "no commits to show" returns a non-nil empty slice,
	// not nil. That lets callers distinguish "loaded with nothing"
	// (empty slice) from "not loaded yet" (nil) without a separate
	// signal — the M5 detail pane relies on this distinction.
	dir := gitfixture.Repo(t, gitfixture.Empty())
	p := resolveTest(t, dir)
	got, err := git.RecentCommits(context.Background(), p, 5)
	if err != nil {
		t.Fatalf("expected nil err for empty repo; got %v", err)
	}
	if got == nil {
		t.Errorf("expected non-nil empty slice; got nil")
	}
	if len(got) != 0 {
		t.Errorf("expected empty slice; got %v", got)
	}
}

func TestRecentCommits_BareRepo(t *testing.T) {
	dir := gitfixture.Repo(t, gitfixture.Bare())
	p := resolveTest(t, dir)
	// Bare fixture has no commits — the helper must not error and
	// must return an empty (not nil) slice per the empty contract.
	got, err := git.RecentCommits(context.Background(), p, 3)
	if err != nil {
		t.Fatalf("bare RecentCommits: %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Errorf("bare-empty: expected non-nil empty slice; got %v", got)
	}
}

// Sanity: ensure `setupRemoteAndLocal` scaffolding actually populates a
// loose upstream ref file (not packed). This is what makes the
// UpstreamRefMtime fingerprint useful in cache tests.
func TestSetupRemoteAndLocal_LooseUpstreamRef(t *testing.T) {
	local, _ := setupRemoteAndLocal(t)
	p := resolveTest(t, local)
	loose := filepath.Join(p.CommonDir, "refs", "remotes", "origin", "main")
	data, err := os.ReadFile(loose)
	if err != nil {
		t.Fatalf("expected loose ref %s: %v", loose, err)
	}
	if len(strings.TrimSpace(string(data))) < 7 {
		t.Errorf("loose ref content looks empty: %q", data)
	}
}
