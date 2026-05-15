package repo_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/sethdeckard/atlas/internal/cache"
	"github.com/sethdeckard/atlas/internal/gitfixture"
	"github.com/sethdeckard/atlas/internal/repo"
)

// These tests guard the M4 contract for bucket-2 fields (Languages,
// StashCount, BranchCount, CommitsLast30d): they're persisted as
// last-known values but the warm-launch status pass always recomputes
// them. The drift modes covered here are the ones the mtime-fingerprint
// set can't catch — manifest changes at the worktree root, nested ref
// creation under refs/heads/, wall-clock advancement past the 30-day
// window — so they verify that UpdateStatus picks up the change even
// when cache.Validate would consider the entry "fresh".

func TestWarmCache_LanguagesPickedUpWithoutGitChange(t *testing.T) {
	dir := gitfixture.Repo(t)
	r := repo.Read(context.Background(), dir)
	if got := r.Languages; len(got) != 0 {
		t.Fatalf("fresh repo: Languages = %v; want []", got)
	}

	// Drop a go.mod into the worktree root *without touching git*.
	// Validate must continue to mark the cached entry "fresh" (mtime
	// fingerprints unchanged), but the status pass should still pick
	// up the new manifest.
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := cache.New()
	c.Repos[r.Path] = r
	if stale, _ := cache.Validate(c, r.Path, []string{r.Path}); len(stale) != 0 {
		t.Fatalf("expected cache to remain fresh; got stale=%v", stale)
	}
	updated := repo.UpdateStatus(context.Background(), r)
	if len(updated.Languages) != 1 || updated.Languages[0] != "go" {
		t.Errorf("status pass should have detected go.mod; got Languages=%v", updated.Languages)
	}
}

func TestWarmCache_BranchCountCountsNestedRefs(t *testing.T) {
	dir := gitfixture.Repo(t)
	mustGit(t, dir, "branch", "old-feature")
	r := repo.Read(context.Background(), dir)
	if r.BranchCount != 2 { // main + old-feature
		t.Fatalf("expected 2 initial branches; got %d", r.BranchCount)
	}

	// Add a nested branch — refs/heads/feature/two. Many users name
	// branches this way; the parent refs/heads dir mtime doesn't
	// reliably capture the new file under feature/.
	mustGit(t, dir, "branch", "feature/two")
	updated := repo.UpdateStatus(context.Background(), r)
	if updated.BranchCount != 3 {
		t.Errorf("status pass should count nested refs; got BranchCount=%d", updated.BranchCount)
	}
}

func TestWarmCache_CommitsLast30dDropsAsClockAdvances(t *testing.T) {
	// A repo with one commit dated 29 days ago at cache time.
	// CommitsLast30d initially = 1.
	twentyNineDaysAgo := time.Now().Add(-29 * 24 * time.Hour)
	dir := gitfixture.Repo(t,
		gitfixture.WithCommits(1),
		gitfixture.WithCommitTime(twentyNineDaysAgo),
	)
	r := repo.Read(context.Background(), dir)
	if r.CommitsLast30d != 1 {
		t.Fatalf("read: expected 1 commit in last 30d; got %d", r.CommitsLast30d)
	}

	// Simulate "wall clock has advanced 2 days" by re-stamping the
	// commit's committer date to 31 days ago. mtimes on HEAD/index
	// might bump as a side-effect of amend, but the test's contract is
	// that the *status pass* recomputes CommitsLast30d either way: a
	// bucket-2 field is unconditionally refreshed by UpdateStatus
	// regardless of cache.Validate's verdict on the entry.
	old := time.Now().Add(-31 * 24 * time.Hour).UTC().Format(time.RFC3339)
	cmd := exec.Command("git", "-C", dir, "commit", "--amend", "--no-edit")
	cmd.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"LC_ALL=C",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_AUTHOR_DATE="+old,
		"GIT_COMMITTER_DATE="+old,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("amend: %v\n%s", err, out)
	}
	updated := repo.UpdateStatus(context.Background(), r)
	if updated.CommitsLast30d != 0 {
		t.Errorf("status pass should reflect updated 30-day window; got CommitsLast30d=%d", updated.CommitsLast30d)
	}
}

func mustGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"LC_ALL=C",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}
