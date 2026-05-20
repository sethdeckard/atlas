package repo_test

import (
	"slices"
	"testing"

	"github.com/sethdeckard/atlas/internal/repo"
)

func TestHighlights_CleanRepoIsEmpty(t *testing.T) {
	r := repo.Repo{
		Path:         "/p/clean",
		Kind:         repo.KindRepo,
		OriginURL:    "git@github.com:o/r.git",
		BehindOrigin: 0,
		AheadOrigin:  0,
	}
	got := repo.Highlights(r)
	if len(got) != 0 {
		t.Errorf("clean repo should have no highlights; got %v", got)
	}
}

func TestHighlights_NoUpstreamCleanIsNotInteresting(t *testing.T) {
	// BehindOrigin == -1 (no upstream) is *not* automatically interesting
	// on its own — only dirty/stash/stale/no-origin promote the row.
	r := repo.Repo{
		Path:         "/p/clean",
		Kind:         repo.KindRepo,
		OriginURL:    "git@github.com:o/r.git",
		BehindOrigin: -1,
		AheadOrigin:  -1,
	}
	got := repo.Highlights(r)
	if len(got) != 0 {
		t.Errorf("no-upstream + clean should still be empty; got %v", got)
	}
}

func TestHighlights_DirtyAndUntracked(t *testing.T) {
	dirty := repo.Repo{Path: "/p", Kind: repo.KindRepo, OriginURL: "x", Dirty: true}
	untrk := repo.Repo{Path: "/p", Kind: repo.KindRepo, OriginURL: "x", Dirty: true, UntrackedOnly: true}
	if got := repo.Highlights(dirty); !slices.Equal(got, []string{"dirty"}) {
		t.Errorf("dirty: got %v", got)
	}
	if got := repo.Highlights(untrk); !slices.Equal(got, []string{"untracked"}) {
		t.Errorf("untracked-only: got %v", got)
	}
}

func TestHighlights_AheadBehindStash(t *testing.T) {
	r := repo.Repo{
		Path:         "/p", Kind: repo.KindRepo, OriginURL: "x",
		AheadOrigin: 2, BehindOrigin: 3, StashCount: 1,
	}
	want := []string{"2 commits ahead", "3 commits behind", "1 stash"}
	if got := repo.Highlights(r); !slices.Equal(got, want) {
		t.Errorf("got %v; want %v", got, want)
	}
	r.StashCount = 4
	want[2] = "4 stashes"
	if got := repo.Highlights(r); !slices.Equal(got, want) {
		t.Errorf("plural: got %v; want %v", got, want)
	}
}

func TestHighlights_StaleAndLinkedWorktree(t *testing.T) {
	r := repo.Repo{
		Path: "/p", Kind: repo.KindRepo, OriginURL: "x",
		Stale: true, WorktreeCount: 3,
	}
	want := []string{"stale", "linked worktree"}
	if got := repo.Highlights(r); !slices.Equal(got, want) {
		t.Errorf("got %v; want %v", got, want)
	}
}

func TestHighlights_LaggingSuppressesStale(t *testing.T) {
	// A worktree with commits can only lag by also being absolutely
	// stale — emitting both labels would double-mark the same row.
	// "lagging worktree" is the stronger signal and stands alone.
	r := repo.Repo{
		Path: "/p", Kind: repo.KindRepo, OriginURL: "x",
		Stale: true, WorktreeCount: 3, LaggingWorktree: true,
	}
	got := repo.Highlights(r)
	for _, s := range got {
		if s == "stale" {
			t.Errorf(`"stale" should be suppressed when "lagging worktree" applies; got %v`, got)
		}
	}
	if !slices.Contains(got, "lagging worktree") {
		t.Errorf(`expected "lagging worktree"; got %v`, got)
	}
}

func TestHighlights_StaleWithoutLaggingStillEmits(t *testing.T) {
	// ▲-only (project-wide cold; no standout sibling) — the existing
	// behavior survives the suppression.
	r := repo.Repo{
		Path: "/p", Kind: repo.KindRepo, OriginURL: "x",
		Stale: true,
	}
	got := repo.Highlights(r)
	if !slices.Contains(got, "stale") {
		t.Errorf(`expected "stale" without a lagging signal; got %v`, got)
	}
}

func TestHighlights_NoOriginButBareIsExempt(t *testing.T) {
	// A normal repo with no origin → "no origin". A bare repo without
	// origin is not flagged (it's a hosting target, not a working repo).
	normal := repo.Repo{Path: "/p", Kind: repo.KindRepo, OriginURL: ""}
	bare := repo.Repo{Path: "/p", Kind: repo.KindBare, OriginURL: ""}
	if got := repo.Highlights(normal); !slices.Equal(got, []string{"no origin"}) {
		t.Errorf("normal no-origin: got %v", got)
	}
	if got := repo.Highlights(bare); len(got) != 0 {
		t.Errorf("bare no-origin should be empty; got %v", got)
	}
}

func TestHighlights_ProblemFirst(t *testing.T) {
	// A read failure surfaces as the first label so the user notices.
	r := repo.Repo{Path: "/p", Kind: repo.KindRepo, OriginURL: "x", Err: "boom", Dirty: true}
	got := repo.Highlights(r)
	if len(got) < 1 || got[0] != "problem" {
		t.Errorf("problem should lead; got %v", got)
	}
}
