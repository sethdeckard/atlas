package tui

import (
	"strings"
	"testing"

	"github.com/sethdeckard/atlas/internal/repo"
)

func TestPackStatusBar_FitsOneLine(t *testing.T) {
	parts := []string{"atlas", "root: ~", "3 repos"}
	got := packStatusBar(parts, 80)
	want := "atlas │ root: ~ │ 3 repos"
	if got != want {
		t.Fatalf("packStatusBar fit-on-one-line: got %q, want %q", got, want)
	}
	if strings.Contains(got, "\n") {
		t.Errorf("expected single line, got multi-line: %q", got)
	}
}

func TestPackStatusBar_WrapsWhenTooWide(t *testing.T) {
	parts := []string{
		"atlas",
		"root: ~/projects",
		"58 repos",
		"12 dirty",
		"11 ahead",
		"1 behind",
		"1 stale",
		"sort: repo ↑",
		"group: activity",
	}
	// Single-line form is ~95 cells; pack at 40 to force multi-line.
	got := packStatusBar(parts, 40)
	lines := strings.Split(got, "\n")
	if len(lines) < 2 {
		t.Fatalf("expected multi-line wrap at width=40, got 1 line: %q", got)
	}
	// Every line must be within the available width (width - padding).
	for i, ln := range lines {
		if w := runeLen(ln); w > 40-statusBarPadding {
			t.Errorf("line %d width %d exceeds avail %d: %q", i, w, 40-statusBarPadding, ln)
		}
	}
	// All parts must survive the pack — nothing dropped.
	for _, p := range parts {
		if !strings.Contains(got, p) {
			t.Errorf("part %q missing from packed output:\n%s", p, got)
		}
	}
}

func TestPackStatusBar_ExactFitNoWrap(t *testing.T) {
	parts := []string{"abc", "def"}
	// "abc │ def" = 9 cells, plus padding=2 → width must be ≥ 11.
	got := packStatusBar(parts, 11)
	if strings.Contains(got, "\n") {
		t.Errorf("expected single line at exact-fit width=11, got: %q", got)
	}
	got = packStatusBar(parts, 10)
	if !strings.Contains(got, "\n") {
		t.Errorf("expected wrap at width=10 (under threshold), got: %q", got)
	}
}

func TestPackStatusBar_SinglePartWiderThanWidth(t *testing.T) {
	// Degenerate input: a part wider than the available width. It must
	// still be emitted (overlong row beats dropped signal).
	parts := []string{"loooooooooooooooooooooooong", "short"}
	got := packStatusBar(parts, 10)
	if !strings.Contains(got, "loooooooooooooooooooooooong") {
		t.Errorf("expected overlong part preserved, got: %q", got)
	}
	if !strings.Contains(got, "short") {
		t.Errorf("expected trailing part preserved, got: %q", got)
	}
	// Should be two lines — overlong on its own, "short" on another.
	if got != "loooooooooooooooooooooooong\nshort" {
		t.Errorf("unexpected layout: %q", got)
	}
}

func TestPackStatusBar_EmptyAndZeroWidth(t *testing.T) {
	if got := packStatusBar(nil, 80); got != "" {
		t.Errorf("nil parts: want empty string, got %q", got)
	}
	// width <= 0 (pre-WindowSizeMsg) falls back to single-line.
	parts := []string{"a", "b", "c"}
	got := packStatusBar(parts, 0)
	if strings.Contains(got, "\n") {
		t.Errorf("width=0 fallback should be single-line, got: %q", got)
	}
}

func TestBucketByGroup_ActivityCanonicalOrder(t *testing.T) {
	// Intentionally seed in scrambled order: a sort-by-repo run would
	// produce something like this (active first because it sorted to
	// the top of the alphabetized repo names).
	rs := []repo.Repo{
		{Path: "/r/a", ActivityTier: "active"},
		{Path: "/r/b", ActivityTier: "cold"},
		{Path: "/r/c", ActivityTier: "dormant"},
		{Path: "/r/d", ActivityTier: "empty"},
		{Path: "/r/e", ActivityTier: "recent"},
	}
	got := bucketByGroup(rs, "activity", "/r")
	gotOrder := make([]string, 0, len(got))
	for _, r := range got {
		gotOrder = append(gotOrder, r.ActivityTier)
	}
	want := []string{"recent", "active", "cold", "dormant", "empty"}
	if !equalStrings(gotOrder, want) {
		t.Errorf("activity group order: got %v, want %v", gotOrder, want)
	}
}

func TestBucketByGroup_ActivityPreservesIntraBucketOrder(t *testing.T) {
	// Two repos in the same tier; their relative order from the input
	// must survive the canonical-tier reorder.
	rs := []repo.Repo{
		{Path: "/r/c", ActivityTier: "active"},
		{Path: "/r/a", ActivityTier: "recent"},
		{Path: "/r/b", ActivityTier: "recent"},
	}
	got := bucketByGroup(rs, "activity", "/r")
	if got[0].Path != "/r/a" || got[1].Path != "/r/b" {
		t.Errorf("intra-bucket order broken: %v", []string{got[0].Path, got[1].Path})
	}
	if got[2].Path != "/r/c" {
		t.Errorf("trailing tier wrong: %s", got[2].Path)
	}
}

func TestBucketByGroup_NonActivityKeepsFirstAppearance(t *testing.T) {
	// For top_dir grouping, there's no canonical sequence — first
	// appearance is the right behavior and must not be reordered.
	rs := []repo.Repo{
		{Path: "/r/zeta/a"},
		{Path: "/r/alpha/b"},
		{Path: "/r/zeta/c"},
	}
	got := bucketByGroup(rs, "top_dir", "/r")
	want := []string{"/r/zeta/a", "/r/zeta/c", "/r/alpha/b"}
	gotPaths := make([]string, 0, len(got))
	for _, r := range got {
		gotPaths = append(gotPaths, r.Path)
	}
	if !equalStrings(gotPaths, want) {
		t.Errorf("top_dir first-appearance broken: got %v, want %v", gotPaths, want)
	}
}

func TestStatusBarHeight_AccountsForStyleWrap(t *testing.T) {
	// A single part wider than width - statusBarPadding stays one
	// logical line in packStatusBar, but lipgloss's Width-bounded
	// Render wraps it physically. statusBarHeight must reflect the
	// rendered height so viewportRows doesn't undercount and let the
	// body overflow.
	longRoot := "/very/long/projects/path/that/exceeds/the/narrow/terminal/width/by/a/lot"
	m := newTestModel(t, nil, longRoot)
	m.width = 30
	m.height = 20
	m.scanning = false

	logical := packStatusBar(m.statusBarParts(), m.width)
	logicalLines := strings.Count(logical, "\n") + 1
	got := m.statusBarHeight()
	if got <= logicalLines {
		t.Errorf("statusBarHeight=%d should exceed logical-line count=%d when a part wraps physically; packed:\n%s",
			got, logicalLines, logical)
	}

	// Sanity: viewportRows must shrink to absorb the wrapped status.
	rows := m.viewportRows()
	if rows >= m.height-1 {
		t.Errorf("viewportRows=%d should be < height-1=%d when status wraps", rows, m.height-1)
	}
}

func equalStrings(a, b []string) bool {
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
