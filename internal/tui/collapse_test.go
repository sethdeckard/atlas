package tui

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/sethdeckard/atlas/internal/cache"
	"github.com/sethdeckard/atlas/internal/config"
	"github.com/sethdeckard/atlas/internal/repo"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// annotatedFixture returns the shared worktree fixture sorted and
// annotated the way rebuildRepos hands it to collapseWorktrees.
func annotatedFixture(t *testing.T, sortBy string, desc bool) []repo.Repo {
	t.Helper()
	rs := worktreeFixture()
	repo.Sort(rs, sortBy, desc, "/projects")
	repo.AnnotateDerived(rs, 60, wtBase)
	return rs
}

// pathsOf is a readable assertion target for slice-shape tests.
func pathsOf(rs []repo.Repo) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.Path
	}
	return out
}

// The primary checkout anchors its project: two children fold away,
// the badge counts them, and the forgotten child's lag rolls up.
func TestCollapseWorktrees_FoldsIntoPrimary(t *testing.T) {
	rs := annotatedFixture(t, "last_commit_at", true)
	out, f := collapseWorktrees(rs, rs)

	if len(out) != 2 {
		t.Fatalf("expected 2 rows (P + solo); got %d: %v", len(out), pathsOf(out))
	}
	if got := f.count["/projects/go/P"]; got != 2 {
		t.Errorf("badge count = %d; want 2", got)
	}
	for _, child := range []string{"/projects/go/P-feat", "/projects/go/P-old"} {
		if f.into[child] != "/projects/go/P" {
			t.Errorf("into[%s] = %q; want the primary", child, f.into[child])
		}
	}
	// P-old is 220d behind a 2d-old primary, so it lags.
	if !f.lagging["/projects/go/P"] {
		t.Error("expected the folded-away lagging child to roll up onto the anchor")
	}
	if f.anyNested {
		t.Error("anyNested should be false when every cluster folded")
	}
}

// A single-worktree repo is not a cluster: no badge, no bookkeeping.
func TestCollapseWorktrees_SoloRepoUntouched(t *testing.T) {
	rs := annotatedFixture(t, "last_commit_at", true)
	out, f := collapseWorktrees(rs, rs)

	if idx := indexOfPath(out, "/projects/go/solo"); idx < 0 {
		t.Fatalf("solo repo missing from output: %v", pathsOf(out))
	}
	if n, ok := f.count["/projects/go/solo"]; ok {
		t.Errorf("solo repo carries a badge count of %d; want none", n)
	}
}

// Without a primary or bare row in scope, the anchor follows the active
// sort order. That makes the anchor sort-dependent, which is intended
// and pinned here.
func TestCollapseWorktrees_BareBackedAnchorFollowsSort(t *testing.T) {
	const cgd = "/x/proj.git"
	build := func() []repo.Repo {
		return []repo.Repo{
			{Name: "proj-a", Path: "/x/proj-a", Branch: "a",
				CommonGitDir: cgd, LastCommitAt: wtAgo(2)},
			{Name: "proj-b", Path: "/x/proj-b", Branch: "b",
				CommonGitDir: cgd, LastCommitAt: wtAgo(9)},
		}
	}
	for _, tc := range []struct {
		sortBy     string
		desc       bool
		wantAnchor string
	}{
		{"last_commit_at", true, "/x/proj-a"},  // newest first
		{"last_commit_at", false, "/x/proj-b"}, // oldest first
		{"repo", false, "/x/proj-a"},           // alphabetically first
		{"repo", true, "/x/proj-b"},            // alphabetically last
	} {
		rs := build()
		repo.Sort(rs, tc.sortBy, tc.desc, "/x")
		repo.AnnotateDerived(rs, 60, wtBase)
		for _, r := range rs {
			if r.PrimaryWorktree {
				t.Fatalf("fixture should have no primary; %s claims to be one", r.Path)
			}
		}
		out, f := collapseWorktrees(rs, rs)
		if len(out) != 1 {
			t.Fatalf("sort=%s desc=%v: expected 1 row; got %v", tc.sortBy, tc.desc, pathsOf(out))
		}
		if out[0].Path != tc.wantAnchor {
			t.Errorf("sort=%s desc=%v: anchor = %s; want %s",
				tc.sortBy, tc.desc, out[0].Path, tc.wantAnchor)
		}
		if f.count[tc.wantAnchor] != 1 {
			t.Errorf("sort=%s desc=%v: badge = %d; want 1",
				tc.sortBy, tc.desc, f.count[tc.wantAnchor])
		}
	}
}

// When the bare repo dir is itself in scope it wins the anchor election
// over any worktree, under either sort direction, because it carries the
// project's own name.
func TestCollapseWorktrees_PrefersBareRowAsAnchor(t *testing.T) {
	const cgd = "/x/proj.git"
	build := func() []repo.Repo {
		return []repo.Repo{
			{Name: "proj", Path: "/x/proj.git", Branch: "main", Kind: repo.KindBare,
				CommonGitDir: cgd, LastCommitAt: wtAgo(30)},
			{Name: "proj-a", Path: "/x/proj-a", Branch: "a",
				CommonGitDir: cgd, LastCommitAt: wtAgo(2)},
			{Name: "proj-b", Path: "/x/proj-b", Branch: "b",
				CommonGitDir: cgd, LastCommitAt: wtAgo(9)},
		}
	}
	for _, desc := range []bool{true, false} {
		rs := build()
		repo.Sort(rs, "last_commit_at", desc, "/x")
		repo.AnnotateDerived(rs, 60, wtBase)
		out, f := collapseWorktrees(rs, rs)
		if len(out) != 1 {
			t.Fatalf("desc=%v: expected 1 row; got %v", desc, pathsOf(out))
		}
		if out[0].Path != "/x/proj.git" {
			t.Errorf("desc=%v: anchor = %s; want the bare row", desc, out[0].Path)
		}
		if f.count["/x/proj.git"] != 2 {
			t.Errorf("desc=%v: badge = %d; want 2", desc, f.count["/x/proj.git"])
		}
	}
}

// A filter that hides the anchor must not fold its children behind some
// arbitrary sibling: every visible member stays a plain row.
func TestCollapseWorktrees_AnchorFilteredOutKeepsMembers(t *testing.T) {
	scoped := annotatedFixture(t, "last_commit_at", true)
	var visible []repo.Repo
	for _, r := range scoped {
		if r.Path == "/projects/go/P-feat" || r.Path == "/projects/go/P-old" {
			visible = append(visible, r)
		}
	}

	out, f := collapseWorktrees(visible, scoped)
	if len(out) != 2 {
		t.Fatalf("expected both children to survive; got %v", pathsOf(out))
	}
	if len(f.count) != 0 || len(f.into) != 0 {
		t.Errorf("expected no folding; count=%v into=%v", f.count, f.into)
	}
	if !f.anyNested {
		t.Error("anyNested should be true when a multi-member cluster stayed unfolded")
	}
}

// One surviving child is not a cluster worth folding, so it renders
// plain with no badge rather than as a one-member subtree.
func TestCollapseWorktrees_LoneSurvivorNoBadge(t *testing.T) {
	scoped := annotatedFixture(t, "last_commit_at", true)
	var visible []repo.Repo
	for _, r := range scoped {
		if r.Path == "/projects/go/P-feat" {
			visible = append(visible, r)
		}
	}

	out, f := collapseWorktrees(visible, scoped)
	if len(out) != 1 || out[0].Path != "/projects/go/P-feat" {
		t.Fatalf("expected the lone survivor; got %v", pathsOf(out))
	}
	if len(f.count) != 0 {
		t.Errorf("lone survivor carries a badge: %v", f.count)
	}
	if f.anyNested {
		t.Error("a single visible member is not a nested cluster")
	}
}

// Folding must not reorder: surviving anchors keep the position the
// active sort gave them.
func TestCollapseWorktrees_PreservesSortedOrder(t *testing.T) {
	for _, tc := range []struct {
		sortBy string
		desc   bool
	}{
		{"last_commit_at", true},
		{"last_commit_at", false},
		{"repo", false},
	} {
		rs := annotatedFixture(t, tc.sortBy, tc.desc)
		out, _ := collapseWorktrees(rs, rs)

		// Every output row must appear in the input in the same
		// relative order.
		prev := -1
		for _, r := range out {
			at := indexOfPath(rs, r.Path)
			if at <= prev {
				t.Errorf("sort=%s desc=%v: %s out of order (input idx %d after %d)",
					tc.sortBy, tc.desc, r.Path, at, prev)
			}
			prev = at
		}
	}
}

// ---- model / view ----

// pressW sends the collapse toggle and returns the updated model.
func pressW(m Model) Model {
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'w'}})
	return out.(Model)
}

func newWorktreeModel(t *testing.T) Model {
	t.Helper()
	m := newTestModel(t, worktreeFixture(), "/projects")
	m.width = 140
	m.height = 30
	m.scanning = false
	return m
}

// The fold is independent of grouping: w collapses in every mode, not
// just the `worktree` tree view.
func TestToggleCollapse_AllGroupModes(t *testing.T) {
	for _, mode := range []string{"activity", "top_dir", "language", "worktree", "none"} {
		t.Run(mode, func(t *testing.T) {
			m := newWorktreeModel(t)
			m.groupBy = mode
			m.rebuildRepos()

			m = pressW(m)
			if len(m.repos) != 2 {
				t.Fatalf("expected 2 rows (P + solo); got %d: %v", len(m.repos), pathsOf(m.repos))
			}
			view := m.View()
			if !strings.Contains(view, "(+2)") {
				t.Errorf("expected (+2) badge in view:\n%s", view)
			}
			for _, hidden := range []string{"P-feat", "P-old"} {
				if strings.Contains(view, hidden) {
					t.Errorf("folded child %s still rendered:\n%s", hidden, view)
				}
			}
		})
	}
}

// w is a toggle, not a one-way door.
func TestToggleCollapse_ExpandRestores(t *testing.T) {
	m := newWorktreeModel(t)
	m = pressW(m)
	m = pressW(m)

	if len(m.repos) != 4 {
		t.Fatalf("expected all 4 rows back; got %v", pathsOf(m.repos))
	}
	if view := m.View(); strings.Contains(view, "(+") {
		t.Errorf("expanded view should carry no badge:\n%s", view)
	}
}

// Collapsing while a child row is selected must land the cursor on the
// anchor that child folded into, not wherever index-clamping points.
func TestToggleCollapse_ReseatsSelectionOntoAnchor(t *testing.T) {
	m := newWorktreeModel(t)
	m.groupBy = "none"
	m.rebuildRepos()

	idx := indexOfPath(m.repos, "/projects/go/P-old")
	if idx < 0 {
		t.Fatalf("fixture missing P-old: %v", pathsOf(m.repos))
	}
	m.selected = idx
	m.selectedPath = "/projects/go/P-old"

	m = pressW(m)
	if m.selectedPath != "/projects/go/P" {
		t.Errorf("selectedPath = %q; want the anchor /projects/go/P", m.selectedPath)
	}
	if got := m.repos[m.selected].Path; got != "/projects/go/P" {
		t.Errorf("selected row = %s; want the anchor", got)
	}
}

// Structural invariant: whatever is selected after a fold must have a
// render row, in every grouping mode and from every starting position.
// A selection with no render row loses its highlight silently.
func TestToggleCollapse_SelectionAlwaysRendered(t *testing.T) {
	for _, mode := range []string{"activity", "top_dir", "language", "worktree", "none"} {
		for start := 0; start < 4; start++ {
			m := newWorktreeModel(t)
			m.groupBy = mode
			m.rebuildRepos()
			if start >= len(m.repos) {
				continue
			}
			m.selected = start
			m.selectedPath = m.repos[start].Path

			m = pressW(m)
			if row := renderRowOfRepo(m.repos, m.tableOpts(), m.selected); row < 0 {
				t.Errorf("mode=%s start=%d: selected row %d (%s) has no render row",
					mode, start, m.selected, m.selectedPath)
			}
		}
	}
}

// A folded-away child's ⊘ has to reach the anchor even when grouping is
// not `worktree`, because folding hides children in every mode.
func TestCollapsedView_RollsUpLaggingInNonWorktreeMode(t *testing.T) {
	m := newWorktreeModel(t)
	// Below the 100-col split the detail pane is hidden, so each line
	// is table only and a path match can't land on the pane's copy.
	m.width = 90
	m.groupBy = "activity"
	m.rebuildRepos()
	m = pressW(m)

	line := lineContaining(t, m.View(), "go/P ")
	if strings.Count(line, "⊘") != 1 {
		t.Errorf("anchor row should carry exactly one ⊘; got %d in:\n%s",
			strings.Count(line, "⊘"), line)
	}
}

// When the anchor lags on its own, flagString already put ⊘ on the row.
// This guards against rollsUpLagging adding a second ⊘.
//
// Three worktrees are required, not two. The rollup only fires when a
// *folded-away* sibling lags, and lag is measured against the project's
// freshest worktree, so a two-row project can never have both a lagging
// anchor and a lagging child: whichever row is freshest doesn't lag.
// Q-fresh keeps the project's clock, Q-old is the forgotten child that
// sets folds.lagging, and Q is an anchor that lags on its own.
func TestCollapsedView_AnchorLagsNoDoubleRollup(t *testing.T) {
	const cgd = "/projects/go/Q/.git"
	rs := []repo.Repo{
		{Name: "Q", Path: "/projects/go/Q", Branch: "main",
			CommonGitDir: cgd, PrimaryWorktreePath: "/projects/go/Q", LastCommitAt: wtAgo(200)},
		{Name: "Q-fresh", Path: "/projects/go/Q-fresh", Branch: "fresh",
			CommonGitDir: cgd, PrimaryWorktreePath: "/projects/go/Q", LastCommitAt: wtAgo(1)},
		{Name: "Q-old", Path: "/projects/go/Q-old", Branch: "old",
			CommonGitDir: cgd, PrimaryWorktreePath: "/projects/go/Q", LastCommitAt: wtAgo(220)},
	}
	for _, mode := range []string{"activity", "worktree"} {
		t.Run(mode, func(t *testing.T) {
			m := newTestModel(t, rs, "/projects")
			// Table-only width; see RollsUpLagging above.
			m.width = 90
			m.height = 30
			m.scanning = false
			m.groupBy = mode
			m.rebuildRepos()
			m = pressW(m)

			// Preconditions: without both of these the rollup branch
			// never executes and the ⊘ count below would be satisfied
			// by flagString alone, making the test vacuous.
			const anchor = "/projects/go/Q"
			if !m.folds.lagging[anchor] {
				t.Fatalf("fixture no longer rolls a lagging child up onto the anchor")
			}
			if idx := indexOfPath(m.repos, anchor); idx < 0 || !m.repos[idx].LaggingWorktree {
				t.Fatalf("fixture no longer has an anchor that lags on its own")
			}

			line := lineContaining(t, m.View(), "go/Q ")
			if strings.Count(line, "⊘") != 1 {
				t.Errorf("anchor should carry exactly one ⊘; got %d in:\n%s",
					strings.Count(line, "⊘"), line)
			}
		})
	}
}

// Collapsed clusters render one flush-left row, so the tree connectors
// and the synthetic bare-backed project header must both disappear.
func TestCollapsedView_NoConnectorsOrSyntheticHeader(t *testing.T) {
	m := newWorktreeModel(t)
	m.groupBy = "worktree"
	m.rebuildRepos()
	m = pressW(m)

	view := m.View()
	for _, connector := range []string{"├─", "└─"} {
		if strings.Contains(view, connector) {
			t.Errorf("collapsed view still draws %q:\n%s", connector, view)
		}
	}
}

// The badge counts rows actually folded away, which is a post-filter
// number. Repo.WorktreeCount is pre-filter and project-wide, so the two
// deliberately disagree once a filter is active.
func TestCollapsedBadge_IsPostFilterCount(t *testing.T) {
	const cgd = "/projects/go/P/.git"
	rs := []repo.Repo{
		{Name: "P", Path: "/projects/go/P", Branch: "main",
			CommonGitDir: cgd, PrimaryWorktreePath: "/projects/go/P", LastCommitAt: wtAgo(2)},
		{Name: "P-feat", Path: "/projects/go/P-feat", Branch: "feat",
			CommonGitDir: cgd, PrimaryWorktreePath: "/projects/go/P", LastCommitAt: wtAgo(5)},
		{Name: "zulu", Path: "/projects/go/zulu", Branch: "z",
			CommonGitDir: cgd, PrimaryWorktreePath: "/projects/go/P", LastCommitAt: wtAgo(7)},
	}
	m := newTestModel(t, rs, "/projects")
	m.width = 140
	m.height = 30
	m.scanning = false
	m.groupBy = "none"
	m.rebuildRepos()
	m = pressW(m)

	if view := m.View(); !strings.Contains(view, "(+2)") {
		t.Errorf("unfiltered badge should be (+2):\n%s", view)
	}

	// Filter to the two P rows; zulu drops out of the visible set.
	m.filterText = "go/P"
	m.rebuildRepos()
	view := m.View()
	if !strings.Contains(view, "(+1)") {
		t.Errorf("filtered badge should be (+1):\n%s", view)
	}
	if got := m.repos[m.selected].WorktreeCount; got != 3 {
		t.Errorf("WorktreeCount = %d; want 3 (pre-filter, project-wide)", got)
	}
}

// Matching children remain unfolded when their elected anchor does not
// match the filter. Here the filter matches only a child, so that child
// stays visible as a plain row.
func TestCollapse_FilterOnlyChildStaysVisible(t *testing.T) {
	m := newWorktreeModel(t)
	m.groupBy = "none"
	m = pressW(m)

	m.filterText = "feat"
	m.rebuildRepos()

	view := m.View()
	if !strings.Contains(view, "P-feat") {
		t.Errorf("the only matching row vanished:\n%s", view)
	}
	if strings.Contains(view, "(+") {
		t.Errorf("a lone survivor should carry no badge:\n%s", view)
	}
}

// With the anchor filtered out, both matching children stay as plain
// rows rather than folding behind an arbitrary sibling.
func TestCollapse_AnchorFilteredOutKeepsBothChildren(t *testing.T) {
	m := newWorktreeModel(t)
	m.groupBy = "none"
	m = pressW(m)

	m.filterText = "P-"
	m.rebuildRepos()

	view := m.View()
	for _, want := range []string{"P-feat", "P-old"} {
		if !strings.Contains(view, want) {
			t.Errorf("%s missing from view:\n%s", want, view)
		}
	}
	if strings.Contains(view, "(+") {
		t.Errorf("no cluster should have folded:\n%s", view)
	}
}

// The badge is part of the repo column, so chooseColumns has to measure
// it or the columns after it shift on badged rows.
func TestChooseColumns_BadgeWidensRepoColumn(t *testing.T) {
	rs := annotatedFixture(t, "last_commit_at", true)
	o := tableOpts{
		root:      "/projects",
		groupBy:   "none",
		collapsed: true,
		folds:     folds{count: map[string]int{"/projects/go/P": 2}},
	}
	cols := chooseColumns(200, rs, o)
	if cols[0].key != "path" {
		t.Fatalf("first column = %q; want path", cols[0].key)
	}
	want := runeLen("go/P (+2)")
	if cols[0].width < want {
		t.Errorf("repo column width = %d; too narrow for %q (%d)", cols[0].width, "go/P (+2)", want)
	}

	// The branch cell must start at the same offset on a badged row and
	// an unbadged one.
	badged := formatRow(cols, rowCells(cols, rs[indexOfPath(rs, "/projects/go/P")], o))
	plain := formatRow(cols, rowCells(cols, rs[indexOfPath(rs, "/projects/go/solo")], o))
	if idxA, idxB := strings.Index(badged, "main"), strings.Index(plain, "main"); idxA != idxB {
		t.Errorf("branch column misaligned: badged at %d, plain at %d\n%s\n%s",
			idxA, idxB, badged, plain)
	}
}

// Collapsed clusters draw no connectors, so the indent budget is only
// owed when a cluster survived unfolded.
func TestChooseColumns_CollapsedDropsWorktreeIndent(t *testing.T) {
	rs := annotatedFixture(t, "last_commit_at", true)
	base := tableOpts{root: "/projects", groupBy: "worktree"}

	expanded := chooseColumns(200, rs, base)

	collapsed := base
	collapsed.collapsed = true
	folded := chooseColumns(200, rs, collapsed)

	nested := collapsed
	nested.folds = folds{anyNested: true}
	withNested := chooseColumns(200, rs, nested)

	if folded[0].width != expanded[0].width-worktreeIndent {
		t.Errorf("collapsed width = %d; want %d (expanded %d minus indent)",
			folded[0].width, expanded[0].width-worktreeIndent, expanded[0].width)
	}
	if withNested[0].width != expanded[0].width {
		t.Errorf("anyNested width = %d; want the full indent back (%d)",
			withNested[0].width, expanded[0].width)
	}
}

// The toggle is sticky: it writes through to the cache session, and a
// fresh model built from that cache comes back collapsed.
func TestToggleCollapse_PersistsToSession(t *testing.T) {
	m := newWorktreeModel(t)
	m = pressW(m)

	if m.cache.Session == nil || !m.cache.Session.CollapseWorktrees {
		t.Fatalf("expected CollapseWorktrees recorded in session; got %+v", m.cache.Session)
	}
	// The toggle sets no status message on purpose; the fold chip on
	// the fixed-height filter row reports the state instead, so the
	// status bar can't wrap and jump the table.
	if m.statusMsg != "" {
		t.Errorf("statusMsg = %q; the toggle must not write to the status bar", m.statusMsg)
	}

	// Rebuild a model from a cache that already carries the flag.
	c := cache.New()
	for _, r := range worktreeFixture() {
		c.Repos[r.Path] = r
	}
	c.Session = &cache.Session{CollapseWorktrees: true}
	restored := New(context.Background(), c, t.TempDir()+"/cache.json", config.Defaults(), "/projects")
	restored.width = 140
	restored.height = 30
	restored.scanning = false

	if !restored.worktreesCollapsed {
		t.Error("restored model should start collapsed")
	}
	if view := restored.View(); !strings.Contains(view, "(+2)") {
		t.Errorf("restored view should already show the badge:\n%s", view)
	}
}

// The overlay is hand-curated, so a new binding can silently miss it.
func TestHelpOverlay_ListsCollapseBinding(t *testing.T) {
	m := newWorktreeModel(t)
	m.showHelp = true
	if view := m.View(); !strings.Contains(view, "fold worktrees") {
		t.Errorf("help overlay missing the w binding:\n%s", view)
	}
}

// lineContaining returns the first rendered line containing want.
func lineContaining(t *testing.T, view, want string) string {
	t.Helper()
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(line, want) {
			return line
		}
	}
	t.Fatalf("no line containing %q in:\n%s", want, view)
	return ""
}

// The rolled-up ⊘ is part of the flags cell, so chooseColumns has to
// budget for it. Without that the row renders wider than the layout
// accounted for, which overflows the pane in split view.
func TestChooseColumns_BudgetsRolledUpFlag(t *testing.T) {
	const cgd = "/projects/go/R/.git"
	rs := []repo.Repo{
		{Name: "R", Path: "/projects/go/R", Branch: "main", CommonGitDir: cgd,
			PrimaryWorktreePath: "/projects/go/R", LastCommitAt: wtAgo(2),
			Dirty: true, AheadOrigin: 1, BehindOrigin: 1},
		{Name: "R-old", Path: "/projects/go/R-old", Branch: "old", CommonGitDir: cgd,
			PrimaryWorktreePath: "/projects/go/R", LastCommitAt: wtAgo(220)},
	}
	repo.AnnotateDerived(rs, 60, wtBase)

	o := tableOpts{root: "/projects", groupBy: "none", collapsed: true, folds: folds{
		count:   map[string]int{"/projects/go/R": 1},
		lagging: map[string]bool{"/projects/go/R": true},
	}}

	// Precondition: the anchor must actually roll up, or this measures
	// nothing. R has its own glyphs (*↑1↓1) so the rollup is additive.
	if !rollsUpLagging(rs[0], o) {
		t.Fatalf("fixture no longer rolls up; flags = %q", flagString(rs[0]))
	}
	if flagString(rs[0]) == flagsCell(rs[0], o) {
		t.Fatalf("rollup added nothing to the flags cell")
	}

	cols := chooseColumns(200, rs, o)
	var flagsW int
	for _, c := range cols {
		if c.key == "flags" {
			flagsW = c.width
		}
	}
	if want := runeLen(flagsCell(rs[0], o)); flagsW < want {
		t.Errorf("flags column budget = %d; need %d for %q",
			flagsW, want, flagsCell(rs[0], o))
	}

	// Every rendered line must fit the width the layout computed.
	out := renderTable(rs[:1], o, -1, 0, 5, 200, newStyles(""))
	for _, line := range strings.Split(out, "\n") {
		if runeLen(line) > columnsWidth(cols) {
			t.Errorf("line is %d cells but layout budgeted %d:\n%q",
				runeLen(line), columnsWidth(cols), line)
		}
	}
}

// Toggling must not change the table's height at any width. A status
// message that wraps the status bar would shrink the table by a row and
// restore it when the message expired, making the view jump twice per
// press. The fold indicator lives on the fixed-height filter row for
// exactly this reason.
func TestToggleCollapse_ViewportHeightNeverChanges(t *testing.T) {
	rs := bounceFixture()
	// Include narrow widths that exercise status-bar wrapping, plus
	// wider controls.
	for _, w := range []int{50, 60, 70, 80, 90, 100, 120} {
		m := newTestModel(t, rs, "/projects")
		m.width = w
		m.height = 14
		m.scanning = false
		m.groupBy = "none"
		m.rebuildRepos()

		barBefore, vpBefore := m.statusBarHeight(), m.viewportRows()
		folded := pressW(m)
		if got := folded.statusBarHeight(); got != barBefore {
			t.Errorf("width=%d: status bar height %d -> %d on fold", w, barBefore, got)
		}
		if got := folded.viewportRows(); got != vpBefore {
			t.Errorf("width=%d: viewport %d -> %d on fold", w, vpBefore, got)
		}
		expanded := pressW(folded)
		if got := expanded.viewportRows(); got != vpBefore {
			t.Errorf("width=%d: viewport %d -> %d on expand", w, vpBefore, got)
		}
		// The whole rendered view must also keep its line count.
		if a, b := lipgloss.Height(m.View()), lipgloss.Height(folded.View()); a != b {
			t.Errorf("width=%d: rendered view height %d -> %d on fold", w, a, b)
		}
	}
}

// The fold state still has to be visible somewhere, or w looks dead in
// a tree with no linked worktrees.
func TestToggleCollapse_ShowsFoldChip(t *testing.T) {
	m := newWorktreeModel(t)
	if strings.Contains(m.View(), "worktrees folded") {
		t.Fatal("expanded view should not advertise a fold")
	}
	folded := pressW(m)
	if !strings.Contains(folded.View(), "worktrees folded") {
		t.Errorf("folded view should show the chip:\n%s", folded.View())
	}
	if !strings.Contains(folded.View(), "w to expand") {
		t.Errorf("chip should name the key that undoes it:\n%s", folded.View())
	}
	if strings.Contains(pressW(folded).View(), "worktrees folded") {
		t.Error("chip should clear when expanded again")
	}
}

// An active filter takes the row back; the badges still carry the
// fold state, and the row stays exactly one line either way.
func TestFoldChip_YieldsToActiveFilter(t *testing.T) {
	m := newWorktreeModel(t)
	m = pressW(m)
	m.filterText = "go/"
	m.rebuildRepos()

	row := m.renderFilterRow(m.width)
	if lipgloss.Height(row) != 1 {
		t.Errorf("filter row is %d lines; must always be 1", lipgloss.Height(row))
	}
	if !strings.Contains(row, "filter:") {
		t.Errorf("an active filter should own the row; got %q", row)
	}
	if strings.Contains(row, "worktrees folded") {
		t.Errorf("fold chip should yield to the filter chip; got %q", row)
	}
}

// The chip must never wrap the row, at any width.
func TestFoldChip_StaysOneLineAtEveryWidth(t *testing.T) {
	m := newWorktreeModel(t)
	m = pressW(m)
	for w := 1; w <= 120; w++ {
		if h := lipgloss.Height(m.renderFilterRow(w)); h != 1 {
			t.Fatalf("width=%d: filter row is %d lines; must always be 1", w, h)
		}
	}
}

func bounceFixture() []repo.Repo {
	var rs []repo.Repo
	for i := 0; i < 12; i++ {
		rs = append(rs, sampleRepo(
			fmt.Sprintf("repo%02d", i),
			fmt.Sprintf("/projects/go/repo%02d", i),
			"main", i+1, false))
	}
	// One multi-worktree project so w has something to fold.
	const cgd = "/projects/go/W/.git"
	rs = append(rs,
		repo.Repo{Name: "W", Path: "/projects/go/W", Branch: "main", CommonGitDir: cgd,
			PrimaryWorktreePath: "/projects/go/W", LastCommitAt: wtAgo(3)},
		repo.Repo{Name: "W-feat", Path: "/projects/go/W-feat", Branch: "feat", CommonGitDir: cgd,
			PrimaryWorktreePath: "/projects/go/W", LastCommitAt: wtAgo(4)})

	return rs
}

// Whatever else changes, the selected row must stay on screen after a
// fold, in every grouping mode and at the widths where layout is
// tightest.
func TestToggleCollapse_SelectionStaysVisible(t *testing.T) {
	rs := bounceFixture()
	for _, w := range []int{60, 80, 100, 140} {
		for _, mode := range []string{"none", "activity", "worktree"} {
			m := newTestModel(t, rs, "/projects")
			m.width = w
			m.height = 14
			m.scanning = false
			m.groupBy = mode
			m.rebuildRepos()

			// Bottom row selected: the position most at risk.
			m.selected = len(m.repos) - 1
			m.selectedPath = m.repos[m.selected].Path
			m.scrollOffset = m.scrollIntoView(m.selected, m.scrollOffset)

			m = pressW(m)
			row := renderRowOfRepo(m.repos, m.tableOpts(), m.selected)
			if row < 0 {
				t.Fatalf("width=%d mode=%s: selected repo has no render row", w, mode)
			}
			if row < m.scrollOffset || row >= m.scrollOffset+m.viewportRows() {
				t.Errorf("width=%d mode=%s: selected render row %d outside viewport [%d,%d)",
					w, mode, row, m.scrollOffset, m.scrollOffset+m.viewportRows())
			}
		}
	}
}
