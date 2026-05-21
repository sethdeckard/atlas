package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/sethdeckard/atlas/internal/repo"
)

// wtBase is the fixed "now" the worktree tests reckon recency against.
var wtBase = time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)

func wtAgo(days int) *time.Time {
	t := wtBase.Add(time.Duration(-days*24) * time.Hour)
	return &t
}

// project P: fresh primary + an in-window worktree + a forgotten one
// (also absolute-stale). Plus a solo repo. StaleDays=60.
func worktreeFixture() []repo.Repo {
	const cgd = "/projects/go/P/.git"
	return []repo.Repo{
		{Name: "P", Path: "/projects/go/P", Branch: "main",
			CommonGitDir: cgd, PrimaryWorktreePath: "/projects/go/P", LastCommitAt: wtAgo(2)},
		{Name: "P-feat", Path: "/projects/go/P-feat", Branch: "feat",
			CommonGitDir: cgd, PrimaryWorktreePath: "/projects/go/P", LastCommitAt: wtAgo(5)},
		{Name: "P-old", Path: "/projects/go/P-old", Branch: "old",
			CommonGitDir: cgd, PrimaryWorktreePath: "/projects/go/P", LastCommitAt: wtAgo(220)},
		{Name: "solo", Path: "/projects/go/solo", Branch: "main",
			CommonGitDir: "/projects/go/solo/.git", LastCommitAt: wtAgo(1)},
	}
}

// The parent (primary) must render before every one of its children,
// and children must be depth-1, no matter how the rows are sorted —
// the tree shape is structural, not a function of the sort key.
func TestBuildWorktreeRows_ParentAboveChildrenInvariant(t *testing.T) {
	cases := []struct {
		by   string
		desc bool
	}{
		{"last_commit_at", true},
		{"last_commit_at", false},
		{"repo", true},
		{"repo", false},
	}
	for _, c := range cases {
		name := c.by
		if c.desc {
			name += "_desc"
		} else {
			name += "_asc"
		}
		t.Run(name, func(t *testing.T) {
			rs := worktreeFixture()
			repo.AnnotateDerived(rs, 60, wtBase)
			repo.Sort(rs, c.by, c.desc, "/projects")
			rows := buildWorktreeRows(bucketWorktrees(rs), "/projects")

			primaryIdx, lastChildIdx := -1, -1
			childDepthsOK := true
			for i, row := range rows {
				if row.kind != rowRepo {
					t.Fatalf("unexpected non-repo row in primary-present forest: %+v", row)
				}
				switch row.repo.Path {
				case "/projects/go/P":
					primaryIdx = i
					if row.depth != 0 {
						t.Errorf("primary depth = %d; want 0", row.depth)
					}
				case "/projects/go/P-feat", "/projects/go/P-old":
					if row.depth != 1 {
						childDepthsOK = false
					}
					if i > lastChildIdx {
						lastChildIdx = i
					}
				case "/projects/go/solo":
					if row.depth != 0 {
						t.Errorf("solo depth = %d; want 0", row.depth)
					}
				}
			}
			if primaryIdx < 0 {
				t.Fatal("primary row not emitted")
			}
			if !childDepthsOK {
				t.Errorf("a child worktree was not depth 1")
			}
			if primaryIdx > firstChildIndex(rows) {
				t.Errorf("primary (idx %d) must precede its children (first child idx %d)",
					primaryIdx, firstChildIndex(rows))
			}
			// Exactly one child flagged lastChild, and it's the final
			// child in the subtree.
			lastCount := 0
			for _, row := range rows {
				if row.depth == 1 && row.lastChild {
					lastCount++
				}
			}
			if lastCount != 1 {
				t.Errorf("expected exactly one lastChild; got %d", lastCount)
			}
		})
	}
}

func firstChildIndex(rows []rowEntry) int {
	for i, row := range rows {
		if row.depth == 1 {
			return i
		}
	}
	return -1
}

// When no member's primary is in scope the cluster degrades to a
// synthetic header followed by every worktree as a depth-1 child.
func TestBuildWorktreeRows_NoPrimaryFallbackHeader(t *testing.T) {
	const cgd = "/elsewhere/d/.git"
	rs := []repo.Repo{
		{Name: "d-a", Path: "/scope/d-a", CommonGitDir: cgd,
			PrimaryWorktreePath: "/elsewhere/d", LastCommitAt: wtAgo(1)},
		{Name: "d-b", Path: "/scope/d-b", CommonGitDir: cgd,
			PrimaryWorktreePath: "/elsewhere/d", LastCommitAt: wtAgo(2)},
	}
	repo.AnnotateDerived(rs, 60, wtBase)
	repo.Sort(rs, "last_commit_at", true, "/scope")
	rows := buildWorktreeRows(bucketWorktrees(rs), "/scope")

	if len(rows) != 3 {
		t.Fatalf("want header + 2 children = 3 rows; got %d", len(rows))
	}
	if rows[0].kind != rowGroup {
		t.Fatalf("first row should be the synthetic project header; got %+v", rows[0])
	}
	if rows[0].label != "d" {
		t.Errorf("header label = %q; want project label %q", rows[0].label, "d")
	}
	for _, row := range rows[1:] {
		if row.kind != rowRepo || row.depth != 1 {
			t.Errorf("fallback children must be depth-1 repo rows; got %+v", row)
		}
	}
}

// A single visible worktree of a multi-worktree project has nothing to
// nest: it renders as a plain depth-0 row, no header, no connector.
func TestBuildWorktreeRows_LoneVisibleWorktree(t *testing.T) {
	rs := []repo.Repo{
		{Name: "P-feat", Path: "/projects/go/P-feat", CommonGitDir: "/projects/go/P/.git",
			PrimaryWorktreePath: "/projects/go/P", LastCommitAt: wtAgo(3)},
	}
	repo.AnnotateDerived(rs, 60, wtBase)
	rows := buildWorktreeRows(bucketWorktrees(rs), "/projects")
	if len(rows) != 1 || rows[0].kind != rowRepo || rows[0].depth != 0 {
		t.Fatalf("lone worktree should be one plain depth-0 row; got %+v", rows)
	}
}

// End-to-end render: the worktree view shows tree connectors, the ⊘
// lagging glyph on the forgotten child, the rolled-up ⊘ on the anchor,
// and the detail-pane roster.
func TestWorktreeView_RendersTreeAndMarkers(t *testing.T) {
	m := newTestModel(t, worktreeFixture(), "/projects")
	m.width = 140
	m.height = 30
	m.scanning = false
	for m.groupBy != "worktree" {
		nm, _ := m.cycleGroup()
		m = nm
	}
	view := m.View()
	if !strings.Contains(view, "├─ ") && !strings.Contains(view, "└─ ") {
		t.Errorf("expected tree connectors in worktree view; got:\n%s", view)
	}
	if !strings.Contains(view, "⊘") {
		t.Errorf("expected ⊘ lagging glyph for the forgotten worktree; got:\n%s", view)
	}
	// ⊘ suppresses ▲ on the same row, and this fixture's only
	// stale repo is also lagging — so ▲ shouldn't render in the
	// table. The right-pane Flags legend always lists ▲ as a
	// vocabulary entry, so scope the check to each line's
	// pre-separator (table) half.
	for _, line := range strings.Split(view, "\n") {
		left := line
		if i := strings.Index(line, "│"); i >= 0 {
			left = line[:i]
		}
		if strings.Contains(left, "▲") {
			t.Errorf("▲ should be suppressed when ⊘ fires; got line:\n%s\nfull view:\n%s", line, view)
		}
	}
}

// When the primary itself lags (a child is fresher than the primary)
// the primary's own row already carries ⊘ via flagString. The rollup
// marker must not append a second ⊘ — exactly one ⊘ on the anchor.
func TestWorktreeView_PrimaryLagsNoDoubleRollup(t *testing.T) {
	const cgd = "/projects/go/Q/.git"
	rs := []repo.Repo{
		{Name: "Q", Path: "/projects/go/Q", Branch: "main",
			CommonGitDir: cgd, PrimaryWorktreePath: "/projects/go/Q", LastCommitAt: wtAgo(200)},
		{Name: "Q-fresh", Path: "/projects/go/Q-fresh", Branch: "fresh",
			CommonGitDir: cgd, PrimaryWorktreePath: "/projects/go/Q", LastCommitAt: wtAgo(1)},
	}
	m := newTestModel(t, rs, "/projects")
	m.width = 140
	m.height = 30
	m.scanning = false
	for m.groupBy != "worktree" {
		nm, _ := m.cycleGroup()
		m = nm
	}
	view := m.View()
	// Find the line for the primary Q row and count ⊘.
	for _, line := range strings.Split(view, "\n") {
		if !strings.Contains(line, "go/Q ") && !strings.HasSuffix(line, "go/Q") {
			continue
		}
		if strings.Count(line, "⊘") != 1 {
			t.Errorf("primary row should carry exactly one ⊘; got %d in:\n%s",
				strings.Count(line, "⊘"), line)
		}
		return
	}
	t.Errorf("primary Q row not found in view:\n%s", view)
}

// The detail pane lists the selected project's full worktree roster
// with per-checkout recency/flags and a (primary) tag — independent of
// the active grouping mode.
func TestRenderDetail_WorktreeRoster(t *testing.T) {
	rs := worktreeFixture()
	repo.AnnotateDerived(rs, 60, wtBase)
	var primary repo.Repo
	var siblings []repo.Repo
	for _, r := range rs {
		if r.CommonGitDir == "/projects/go/P/.git" {
			siblings = append(siblings, r)
			if r.PrimaryWorktree {
				primary = r
			}
		}
	}
	out := renderDetail(&primary, recentCommitsState{}, siblings, 80, newStyles(""))
	if !strings.Contains(out, "▸ Worktrees (3)") {
		t.Errorf("expected roster header; got:\n%s", out)
	}
	if !strings.Contains(out, "(primary)") {
		t.Errorf("expected (primary) tag on the main checkout; got:\n%s", out)
	}
	if !strings.Contains(out, "⊘") {
		t.Errorf("expected ⊘ on the lagging worktree in the roster; got:\n%s", out)
	}
	if strings.Contains(out, "▲") {
		t.Errorf("▲ should be suppressed when ⊘ fires; got:\n%s", out)
	}
}
