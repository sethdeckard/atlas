package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/sethdeckard/atlas/internal/repo"
)

func init() {
	SetNowFunc(func() time.Time {
		return time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	})
}

func tableRepo(name, branch string, daysAgo int, dirty bool) repo.Repo {
	t := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(-daysAgo*24) * time.Hour)
	return repo.Repo{
		Name:         name,
		Path:         "/r/" + name,
		RelPath:      "~/projects/category/" + name,
		Kind:         repo.KindRepo,
		Branch:       branch,
		LastCommitAt: &t,
		Dirty:        dirty,
	}
}

// TestChooseColumns_FullWidthKeepsAll: when the terminal is wide enough,
// no columns are dropped. The full layout is repo + branch + last_commit
// + flags (repo already encodes top_dir/name so there is no separate
// top_dir column).
func TestChooseColumns_FullWidthKeepsAll(t *testing.T) {
	repos := []repo.Repo{tableRepo("alpha", "main", 1, false)}
	cols := chooseColumns(200, repos, "/r", "none")
	if len(cols) != 4 {
		t.Errorf("expected 4 columns at width 200; got %d (%v)", len(cols), columnKeys(cols))
	}
	if cols[0].title != "repo" {
		t.Errorf("first column title = %q; want repo", cols[0].title)
	}
}

// TestChooseColumns_DropsBranchOnNarrow: width below the full layout
// drops branch (repo stays — it's the most identifying column).
func TestChooseColumns_DropsBranchOnNarrow(t *testing.T) {
	repos := []repo.Repo{tableRepo("alpha", "main-with-a-long-name", 1, false)}
	cols := chooseColumns(20, repos, "/r", "none")
	keys := columnKeys(cols)
	for _, k := range keys {
		if k == "branch" {
			t.Errorf("expected branch dropped at narrow width; got %v", keys)
		}
	}
}

// TestRenderTable_DropsRowsOutsideViewport: only the windowed slice is
// rendered, even though selection still applies to the full slice.
func TestRenderTable_DropsRowsOutsideViewport(t *testing.T) {
	repos := []repo.Repo{
		tableRepo("alpha", "main", 1, false),
		tableRepo("bravo", "main", 2, false),
		tableRepo("charlie", "main", 3, false),
		tableRepo("delta", "main", 4, false),
	}
	s := newStyles("")
	// groupBy="none" so render rows == repo rows.
	out := renderTable(repos, "/r", "none", 2, 1, 2, 80, s)
	// Window: scrollOffset=1, viewportRows=2 → bravo + charlie.
	if !strings.Contains(out, "bravo") || !strings.Contains(out, "charlie") {
		t.Errorf("expected windowed rows in output; got:\n%s", out)
	}
	if strings.Contains(out, "alpha") || strings.Contains(out, "delta") {
		t.Errorf("expected outside-window rows hidden; got:\n%s", out)
	}
}

// columnKeys is a small introspection helper so the column-dropping tests
// don't need to depend on column rendering.
func columnKeys(cols []column) []string {
	out := make([]string, len(cols))
	for i, c := range cols {
		out[i] = c.key
	}
	return out
}
