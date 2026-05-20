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
		"/p/solo":       1,
		"/p/atlas":      3,
		"/p/atlas-feat": 3,
		"/p/atlas-fix":  3,
		"/p/atria":      1,
	}
	for _, r := range repos {
		if got := r.WorktreeCount; got != want[r.Path] {
			t.Errorf("%s: WorktreeCount = %d; want %d", r.Path, got, want[r.Path])
		}
	}
}

func TestAnnotateDerived_LaggingWorktree(t *testing.T) {
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	day := 24 * time.Hour
	fresh := now.Add(-3 * day) // project's freshest checkout
	mid := now.Add(-10 * day)  // 7d behind fresh → within 90d, NOT lagging
	old := now.Add(-200 * day) // 197d behind fresh → lagging
	soloT := now.Add(-2 * day)

	repos := []repo.Repo{
		// Project A: fresh primary + an in-window worktree + a
		// forgotten one + an empty (never-committed) one.
		{Path: "/p/A", CommonGitDir: "/p/A/.git", PrimaryWorktreePath: "/p/A", LastCommitAt: &fresh},
		{Path: "/p/A-feat", CommonGitDir: "/p/A/.git", PrimaryWorktreePath: "/p/A", LastCommitAt: &mid},
		{Path: "/p/A-old", CommonGitDir: "/p/A/.git", PrimaryWorktreePath: "/p/A", LastCommitAt: &old},
		{Path: "/p/A-empty", CommonGitDir: "/p/A/.git", PrimaryWorktreePath: "/p/A", LastCommitAt: nil},
		// Project B: every worktree fresh — no false lag.
		{Path: "/p/B", CommonGitDir: "/p/B/.git", PrimaryWorktreePath: "/p/B", LastCommitAt: &fresh},
		{Path: "/p/B-feat", CommonGitDir: "/p/B/.git", PrimaryWorktreePath: "/p/B", LastCommitAt: &fresh},
		// Solo repo — relative lag never applies.
		{Path: "/p/solo", CommonGitDir: "/p/solo/.git", LastCommitAt: &soloT},
	}
	repo.AnnotateDerived(repos, 90, now)
	byPath := make(map[string]repo.Repo, len(repos))
	for _, r := range repos {
		byPath[r.Path] = r
	}

	wantLag := map[string]bool{
		"/p/A":       false, // the freshest — never lags
		"/p/A-feat":  false, // within stale window of fresh
		"/p/A-old":   true,  // 197d behind the project's freshest
		"/p/A-empty": true,  // no commits while a sibling has them
		"/p/B":       false,
		"/p/B-feat":  false,
		"/p/solo":    false, // solo: relative lag is meaningless
	}
	for p, want := range wantLag {
		if got := byPath[p].LaggingWorktree; got != want {
			t.Errorf("%s: LaggingWorktree = %v; want %v", p, got, want)
		}
	}

	if !byPath["/p/A"].PrimaryWorktree {
		t.Errorf("/p/A should be PrimaryWorktree")
	}
	if byPath["/p/A-feat"].PrimaryWorktree {
		t.Errorf("/p/A-feat is a linked worktree, not primary")
	}
	if byPath["/p/solo"].PrimaryWorktree {
		t.Errorf("solo repo should not be flagged PrimaryWorktree")
	}

	// The rollup lands on the primary because A-old / A-empty lag.
	if !byPath["/p/A"].WorktreeHasLaggingChild {
		t.Errorf("/p/A should have WorktreeHasLaggingChild (A-old/A-empty lag)")
	}
	if byPath["/p/B"].WorktreeHasLaggingChild {
		t.Errorf("/p/B has no lagging children; rollup should be false")
	}
	// Non-primary rows never carry the rollup.
	if byPath["/p/A-old"].WorktreeHasLaggingChild {
		t.Errorf("rollup must only be set on the primary row")
	}
}

func TestAnnotateDerived_UniformlyOldProjectIsNotRolledUp(t *testing.T) {
	// A project where every worktree is absolute-stale but no one is
	// behind the others: the project is just cold. The rollup must
	// NOT light up — appending ⊘ to the anchor would mislead the user
	// into thinking they have a forgotten checkout. The children's
	// own rows still show ▲ via the table's flagString.
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	day := 24 * time.Hour
	old := now.Add(-200 * day)
	older := now.Add(-205 * day) // 5d behind freshest → < 60d, not lagging

	repos := []repo.Repo{
		{Path: "/p/C", CommonGitDir: "/p/C/.git", PrimaryWorktreePath: "/p/C", LastCommitAt: &old},
		{Path: "/p/C-feat", CommonGitDir: "/p/C/.git", PrimaryWorktreePath: "/p/C", LastCommitAt: &older},
	}
	repo.AnnotateDerived(repos, 60, now)

	for _, r := range repos {
		if r.LaggingWorktree {
			t.Errorf("%s: LaggingWorktree = true; uniformly-old siblings should not lag relative to each other", r.Path)
		}
	}
	for _, r := range repos {
		if r.WorktreeHasLaggingChild {
			t.Errorf("%s: WorktreeHasLaggingChild = true; absolute-stale-only children must not trigger the rollup", r.Path)
		}
		if !r.Stale {
			t.Errorf("%s: should still be absolute-stale (▲)", r.Path)
		}
	}
}

func TestAnnotateDerived_NoPrimaryStillComputesLag(t *testing.T) {
	// Primary checkout is out of scope: no member's Path equals its
	// PrimaryWorktreePath, so PrimaryWorktree stays false for all —
	// but relative lag is still judged against the visible freshest.
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	day := 24 * time.Hour
	fresh := now.Add(-1 * day)
	old := now.Add(-150 * day)

	repos := []repo.Repo{
		{Path: "/scope/d-a", CommonGitDir: "/elsewhere/d/.git", PrimaryWorktreePath: "/elsewhere/d", LastCommitAt: &fresh},
		{Path: "/scope/d-b", CommonGitDir: "/elsewhere/d/.git", PrimaryWorktreePath: "/elsewhere/d", LastCommitAt: &old},
	}
	repo.AnnotateDerived(repos, 90, now)

	for _, r := range repos {
		if r.PrimaryWorktree {
			t.Errorf("%s: PrimaryWorktree should be false (primary out of scope)", r.Path)
		}
	}
	if repos[0].LaggingWorktree {
		t.Errorf("d-a is the freshest visible; should not lag")
	}
	if !repos[1].LaggingWorktree {
		t.Errorf("d-b is 150d behind the visible freshest; should lag")
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
