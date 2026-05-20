package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/sethdeckard/atlas/internal/repo"
)

func mkDetailRepo(name string) *repo.Repo {
	when := time.Date(2026, 4, 30, 9, 21, 0, 0, time.UTC)
	return &repo.Repo{
		Name:         name,
		Path:         "/home/u/projects/go/" + name,
		RelPath:      "~/projects/go/" + name,
		Kind:         repo.KindRepo,
		Branch:       "main",
		HeadSHA:      "abc1234",
		LastCommitAt: &when,
		OriginURL:    "git@github.com:s/" + name + ".git",
		DefaultBranch: "main",
		BranchCount:  3,
		StashCount:   1,
		Languages:    []string{"go"},
		ActivityTier: "recent",
		CommitsLast30d: 8,
		AheadOrigin:  2,
		BehindOrigin: 0,
	}
}

func TestRenderDetail_FullFields(t *testing.T) {
	// renderDetail home-contracts r.Path via config.ContractHome, which
	// reads $HOME at call time. Pin HOME so the assertion on
	// "~/projects/go/atlas" doesn't depend on the CI runner's home dir.
	t.Setenv("HOME", "/home/u")
	r := mkDetailRepo("atlas")
	r.Dirty = true
	out := renderDetail(r, recentCommitsState{loaded: true, lines: []string{"first commit", "second commit"}}, nil, 60, newStyles(""))
	for _, want := range []string{
		"atlas",
		"~/projects/go/atlas",
		"Highlights",
		"dirty",
		"2 commits ahead",
		"1 stash",
		"Kind",
		"Branch",
		"main (↑2 ↓0)",
		"Origin",
		"git@github.com:s/atlas.git",
		"Default",
		"Last",
		"2026-04-30 09:21",
		"Activity",
		"recent (8 commits/30d)",
		"Languages",
		"go",
		"Branches",
		"3",
		"Stashes",
		"1",
		"Recent commits",
		"first commit",
		"second commit",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in detail render:\n%s", want, out)
		}
	}
}

func TestRenderDetail_NoOriginOmitted(t *testing.T) {
	r := mkDetailRepo("atlas")
	r.OriginURL = ""
	out := renderDetail(r, recentCommitsState{}, nil, 60, newStyles(""))
	if strings.Contains(out, "Origin") && strings.Contains(out, "—") {
		// Either we omitted the row or rendered "—" — both acceptable
		// behaviors for a missing field. But the rendered "Origin" row
		// must not show an empty value.
		t.Errorf("Origin row with empty value should be omitted; got:\n%s", out)
	}
}

func TestRenderDetail_NoCommitsPlaceholder(t *testing.T) {
	r := mkDetailRepo("atlas")
	r.LastCommitAt = nil
	// Loaded with an empty slice = "no commits to show".
	out := renderDetail(r, recentCommitsState{loaded: true, lines: []string{}}, nil, 60, newStyles(""))
	if !strings.Contains(out, "(no commits)") {
		t.Errorf("expected (no commits) placeholder for loaded-empty; got:\n%s", out)
	}
}

func TestRenderDetail_LoadingPlaceholder(t *testing.T) {
	r := mkDetailRepo("atlas")
	// Zero state = never requested → render as loading so the pane
	// doesn't blank out between selection-change and the first tick.
	out := renderDetail(r, recentCommitsState{}, nil, 60, newStyles(""))
	if !strings.Contains(out, "(loading…)") {
		t.Errorf("expected (loading…) placeholder for zero state; got:\n%s", out)
	}
	// Explicit loading flag also renders as loading.
	out = renderDetail(r, recentCommitsState{loading: true}, nil, 60, newStyles(""))
	if !strings.Contains(out, "(loading…)") {
		t.Errorf("expected (loading…) placeholder for loading=true; got:\n%s", out)
	}
}

// TestRenderDetail_SanitizesControlChars confirms that repo-controlled
// strings (name, branch, origin, commit subjects) carrying terminal
// escape sequences come out of renderDetail with the controls stripped.
// Untrusted commit subjects in particular are the realistic vector: a
// user clones a repo whose history contains a crafted OSC 8 hyperlink
// or a window-title escape, and the detail pane would otherwise render
// it raw.
func TestRenderDetail_SanitizesControlChars(t *testing.T) {
	t.Setenv("HOME", "/home/u")
	r := mkDetailRepo("atlas")
	r.Name = "atlas\x1b]0;HIJACK\x07"
	r.Branch = "main\x1b[31m"
	r.OriginURL = "git@github.com:s/atlas\x07.git"
	r.Languages = []string{"go\x1b[1m"}

	hostile := "subject\x1b]8;;http://evil/\x1b\\spoof\x1b]8;;\x1b\\"
	out := renderDetail(r, recentCommitsState{
		loaded: true,
		lines:  []string{hostile},
	}, nil, 80, newStyles(""))

	for _, banned := range []string{"\x1b", "\x07", "\x00"} {
		if strings.Contains(out, banned) {
			t.Errorf("detail pane still contains control char %q; got:\n%q", banned, out)
		}
	}
	// The visible text around the escapes survives so the user still
	// sees what's there.
	for _, want := range []string{"atlas", "main", "spoof", "subject"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected visible substring %q to survive sanitization:\n%s", want, out)
		}
	}
}

func TestRenderDetail_ErrorState(t *testing.T) {
	r := mkDetailRepo("atlas")
	out := renderDetail(r, recentCommitsState{loaded: true, err: errBoom{}}, nil, 60, newStyles(""))
	if !strings.Contains(out, "(commits unavailable)") {
		t.Errorf("expected (commits unavailable) for err state; got:\n%s", out)
	}
}

type errBoom struct{}

func (errBoom) Error() string { return "boom" }

func TestRenderDetail_NilRepo(t *testing.T) {
	out := renderDetail(nil, recentCommitsState{}, nil, 60, newStyles(""))
	if !strings.Contains(out, "(no selection)") {
		t.Errorf("nil repo should render no-selection placeholder; got:\n%s", out)
	}
}

func TestRenderDetail_CleanRepoOmitsHighlights(t *testing.T) {
	// A clean, no-divergence, no-stash repo with origin set has no
	// Highlights — the line should be elided to keep the pane uncluttered.
	r := mkDetailRepo("atlas")
	r.Dirty = false
	r.AheadOrigin = 0
	r.BehindOrigin = 0
	r.StashCount = 0
	out := renderDetail(r, recentCommitsState{loaded: true, lines: []string{"a"}}, nil, 60, newStyles(""))
	if strings.Contains(out, "Highlights") {
		t.Errorf("clean repo should not show Highlights line; got:\n%s", out)
	}
}
