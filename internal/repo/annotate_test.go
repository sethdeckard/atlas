package repo_test

import (
	"testing"
	"time"

	"github.com/sethdeckard/atlas/internal/repo"
)

func TestAnnotateDerived_WorktreeCount(t *testing.T) {
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	primary := time.Date(2026, 4, 30, 0, 0, 0, 0, time.UTC)

	repos := []repo.Repo{
		// Solo repo.
		{Path: "/p/solo", CommonGitDir: "/p/solo/.git", LastCommitAt: &primary},
		// Primary + 2 linked worktrees: all share CommonGitDir.
		{Path: "/p/atlas", CommonGitDir: "/p/atlas/.git", LastCommitAt: &primary},
		{Path: "/p/atlas-feat", CommonGitDir: "/p/atlas/.git", LastCommitAt: &primary},
		{Path: "/p/atlas-fix", CommonGitDir: "/p/atlas/.git", LastCommitAt: &primary},
		// Another solo.
		{Path: "/p/atria", CommonGitDir: "/p/atria/.git", LastCommitAt: &primary},
	}
	repo.AnnotateDerived(repos, 90, now)

	want := map[string]int{
		"/p/solo":        1,
		"/p/atlas":       3,
		"/p/atlas-feat":  3,
		"/p/atlas-fix":   3,
		"/p/atria":       1,
	}
	for _, r := range repos {
		if got := r.WorktreeCount; got != want[r.Path] {
			t.Errorf("%s: WorktreeCount = %d; want %d", r.Path, got, want[r.Path])
		}
	}
}

func TestAnnotateDerived_StaleBoundary(t *testing.T) {
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	day := 24 * time.Hour
	cutoffMinus1 := now.Add(-89 * day) // staleDays=90: not yet stale
	cutoffPlus1 := now.Add(-91 * day)  // 1 day past stale → Stale=true

	repos := []repo.Repo{
		{Path: "/p/empty", LastCommitAt: nil},
		{Path: "/p/recent", LastCommitAt: &cutoffMinus1},
		{Path: "/p/old", LastCommitAt: &cutoffPlus1},
	}
	repo.AnnotateDerived(repos, 90, now)

	want := map[string]bool{
		"/p/empty":  false, // nil never stale (it's just empty)
		"/p/recent": false,
		"/p/old":    true,
	}
	for _, r := range repos {
		if got := r.Stale; got != want[r.Path] {
			t.Errorf("%s: Stale = %v; want %v", r.Path, got, want[r.Path])
		}
	}
}

func TestAnnotateDerived_ActivityTierAlignsWithStale(t *testing.T) {
	// A repo crossing the staleDays threshold flips Stale=true and
	// ActivityTier from "active" to "cold" simultaneously, so the two
	// signals never contradict.
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	day := 24 * time.Hour
	cutoffMinus1 := now.Add(-89 * day)
	cutoffPlus1 := now.Add(-91 * day)
	repos := []repo.Repo{
		{Path: "/p/active", LastCommitAt: &cutoffMinus1},
		{Path: "/p/cold", LastCommitAt: &cutoffPlus1},
	}
	repo.AnnotateDerived(repos, 90, now)

	if repos[0].ActivityTier != "active" || repos[0].Stale {
		t.Errorf("active expected: tier=%q stale=%v", repos[0].ActivityTier, repos[0].Stale)
	}
	if repos[1].ActivityTier != "cold" || !repos[1].Stale {
		t.Errorf("cold+stale expected: tier=%q stale=%v", repos[1].ActivityTier, repos[1].Stale)
	}
}
