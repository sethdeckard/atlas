package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/sethdeckard/atlas/internal/repo"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
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

// A transient status message must never change the bar's height. Bar
// height feeds viewportRows, so a message that wrapped the bar would
// shrink the table while it showed and give the row back when it
// expired, jumping the view twice per keypress.
func TestStatusBar_MessageNeverChangesHeight(t *testing.T) {
	repos := []repo.Repo{
		sampleRepo("alpha", "/projects/go/alpha", "main", 1, true),
		sampleRepo("bravo", "/projects/go/bravo", "main", 2, false),
		sampleRepo("charlie", "/projects/ruby/charlie", "main", 3, false),
	}
	messages := []string{
		"group: none",
		"copied path",
		"opened origin",
		"no origin URL",
		"cache save failed: /some/quite/long/path/to/a/cache/file.json",
		strings.Repeat("very long message ", 12),
	}
	for _, w := range []int{40, 60, 70, 80, 90, 100, 120, 200} {
		m := newTestModel(t, repos, "/projects")
		m.width = w
		m.height = 20
		m.scanning = false

		wantBar, wantViewport := m.statusBarHeight(), m.viewportRows()
		for _, msg := range messages {
			withMsg := m
			withMsg.statusMsg = msg
			if got := withMsg.statusBarHeight(); got != wantBar {
				t.Errorf("width=%d msg=%.20q: bar height %d; want %d",
					w, msg, got, wantBar)
			}
			if got := withMsg.viewportRows(); got != wantViewport {
				t.Errorf("width=%d msg=%.20q: viewport %d; want %d",
					w, msg, got, wantViewport)
			}
			// The bar must also stay inside the terminal.
			for _, line := range strings.Split(withMsg.statusBar(), "\n") {
				if lipgloss.Width(line) > w-statusBarPadding {
					t.Errorf("width=%d msg=%.20q: bar line is %d cells, budget %d:\n%q",
						w, msg, lipgloss.Width(line), w-statusBarPadding, line)
				}
			}
		}
	}
}

// A message with enough available width is shown in full.
func TestStatusBar_MessageShownWhenItFits(t *testing.T) {
	m := newTestModel(t, []repo.Repo{
		sampleRepo("alpha", "/projects/go/alpha", "main", 1, false),
	}, "/projects")
	m.width = 200
	m.height = 20
	m.scanning = false
	m.statusMsg = "copied path"

	if !strings.Contains(m.statusBar(), "copied path") {
		t.Errorf("a message with room to spare should render whole:\n%s", m.statusBar())
	}
}

// Pressing tab must not resize the table at any width.
func TestCycleGroup_ViewportHeightNeverChanges(t *testing.T) {
	repos := []repo.Repo{
		sampleRepo("alpha", "/projects/go/alpha", "main", 1, true),
		sampleRepo("bravo", "/projects/go/bravo", "main", 2, false),
		sampleRepo("charlie", "/projects/ruby/charlie", "main", 3, false),
	}
	for _, w := range []int{50, 60, 70, 80, 90, 100, 120} {
		m := newTestModel(t, repos, "/projects")
		m.width = w
		m.height = 20
		m.scanning = false

		wantBar, wantViewport := m.statusBarHeight(), m.viewportRows()
		// A full lap through every grouping mode.
		for i := 0; i < 5; i++ {
			nm, _ := m.cycleGroup()
			m = nm
			if got := m.statusBarHeight(); got != wantBar {
				t.Fatalf("width=%d groupBy=%s: bar height %d; want %d",
					w, m.groupBy, got, wantBar)
			}
			if got := m.viewportRows(); got != wantViewport {
				t.Fatalf("width=%d groupBy=%s: viewport %d; want %d",
					w, m.groupBy, got, wantViewport)
			}
		}
	}
}

// cycleGroup's message has to expire, or "group: none" sits in the bar
// until something else happens to overwrite it.
func TestCycleGroup_SchedulesStatusClear(t *testing.T) {
	m := newTestModel(t, []repo.Repo{
		sampleRepo("alpha", "/projects/go/alpha", "main", 1, false),
	}, "/projects")
	m.width = 120
	m.height = 20
	m.scanning = false

	// tea.Tick really sleeps, so shorten the TTL rather than pay the
	// full three seconds to observe the clear.
	defer func(d time.Duration) { statusMessageTTL = d }(statusMessageTTL)
	statusMessageTTL = time.Millisecond

	nm, cmd := m.cycleGroup()
	if nm.statusMsg == "" {
		t.Fatal("expected a group message")
	}
	if cmd == nil {
		t.Fatal("expected a batched command")
	}
	if !emitsClearStatus(t, cmd) {
		t.Error("cycleGroup must schedule clearStatusAfter; otherwise the " +
			"group message never expires")
	}
}

// emitsClearStatus reports whether cmd (possibly a batch) eventually
// produces a clearStatusMsg.
func emitsClearStatus(t *testing.T, cmd tea.Cmd) bool {
	t.Helper()
	if cmd == nil {
		return false
	}
	switch msg := cmd().(type) {
	case clearStatusMsg:
		return true
	case tea.BatchMsg:
		for _, c := range msg {
			if emitsClearStatus(t, c) {
				return true
			}
		}
	}
	return false
}

// scan.Discover joins walk failures with errors.Join, so a discover
// error arrives with embedded newlines. Splicing those into the bar
// would add rendered rows that statusBarHeight never sees.
func TestStatusBar_MultilineMessageStaysOneLine(t *testing.T) {
	repos := []repo.Repo{
		sampleRepo("alpha", "/projects/go/alpha", "main", 1, false),
		sampleRepo("bravo", "/projects/go/bravo", "main", 2, true),
	}
	multiline := "discover: open /a/b: permission denied\n" +
		"open /c/d: permission denied\nopen /e/f: permission denied"

	for _, w := range []int{40, 60, 80, 120, 200} {
		m := newTestModel(t, repos, "/projects")
		m.width = w
		m.height = 20
		m.scanning = false
		want := m.statusBarHeight()

		m.statusMsg = multiline
		m.statusIsErr = true

		bar := m.statusBar()
		if strings.Contains(bar, "\n") && lipgloss.Height(bar) != want {
			t.Errorf("width=%d: bar renders %d lines; statusBarHeight reports %d\n%q",
				w, lipgloss.Height(bar), want, bar)
		}
		if got := m.statusBarHeight(); got != want {
			t.Errorf("width=%d: statusBarHeight %d; want %d", w, got, want)
		}
		if got := m.viewportRows(); got != newTestModelViewport(t, repos, w) {
			t.Errorf("width=%d: viewport moved with a multiline message", w)
		}
	}
}

func newTestModelViewport(t *testing.T, repos []repo.Repo, w int) int {
	t.Helper()
	m := newTestModel(t, repos, "/projects")
	m.width = w
	m.height = 20
	m.scanning = false
	return m.viewportRows()
}

// A failure must never be silently swallowed because the bar's own text
// filled the line. The persistent text yields instead.
func TestStatusBar_ErrorSurvivesAFullBar(t *testing.T) {
	// Enough signal counts to crowd the bar at narrow widths.
	repos := []repo.Repo{
		sampleRepo("alpha", "/projects/go/alpha", "main", 1, true),
		sampleRepo("bravo", "/projects/go/bravo", "main", 400, false),
		sampleRepo("charlie", "/projects/ruby/charlie", "main", 2, true),
	}
	for _, w := range []int{40, 50, 60, 70, 80} {
		for _, msg := range []string{"no origin URL", "copy failed: x", "no selection"} {
			m := newTestModel(t, repos, "/projects")
			m.width = w
			m.height = 20
			m.scanning = false
			m.statusMsg = msg
			m.statusIsErr = true

			bar := m.statusBar()
			// The message may be clipped, but its opening has to show.
			head := msg
			if len(head) > 6 {
				head = head[:6]
			}
			if !strings.Contains(bar, head) {
				t.Errorf("width=%d: %q dropped entirely from the bar:\n%q", w, msg, bar)
			}
			for _, line := range strings.Split(bar, "\n") {
				if lipgloss.Width(line) > w-statusBarPadding {
					t.Errorf("width=%d: line is %d cells, budget %d:\n%q",
						w, lipgloss.Width(line), w-statusBarPadding, line)
				}
			}
		}
	}
}

// The rendered bar height must equal what statusBarHeight reserves, at
// every width and with or without a message. Narrow terminals are the
// interesting case: an overlong part wraps physically, and clipping it
// to make room for a message can stop that wrap, shortening the bar by
// a row while viewportRows still reserves the taller figure.
func TestStatusBar_RenderedHeightMatchesReserved(t *testing.T) {
	repos := []repo.Repo{
		sampleRepo("alpha", "/projects/go/alpha", "main", 1, true),
		sampleRepo("bravo", "/projects/go/bravo", "main", 400, false),
	}
	messages := []string{
		"copied path",
		"no origin URL",
		"group: activity",
		"cache save failed: /a/long/path/to/the/cache.json",
		"discover: open /a: denied\nopen /b: denied",
	}
	for w := 10; w <= 140; w++ {
		m := newTestModel(t, repos, "/projects")
		m.width = w
		m.height = 24
		m.scanning = false

		reserved := m.statusBarHeight()
		rendered := func(mm Model) int {
			return lipgloss.Height(mm.styles.statusBar.Width(w).Render(mm.statusBar()))
		}
		if got := rendered(m); got != reserved {
			t.Fatalf("width=%d: bare bar renders %d; reserved %d", w, got, reserved)
		}
		for _, msg := range messages {
			withMsg := m
			withMsg.statusMsg = msg
			if got := rendered(withMsg); got != reserved {
				t.Errorf("width=%d msg=%.18q: bar renders %d; reserved %d",
					w, msg, got, reserved)
			}
			if got := withMsg.statusBarHeight(); got != reserved {
				t.Errorf("width=%d msg=%.18q: statusBarHeight %d; reserved %d",
					w, msg, got, reserved)
			}
		}
	}
}

// Styling is applied after truncation, so an error that has to be
// clipped still measures correctly. Truncating a styled string instead
// would slice escape bytes as if they were characters, putting the
// ellipsis inside an escape sequence and throwing off the width.
func TestStatusBar_ErrorTruncatesOnPlainText(t *testing.T) {
	repos := []repo.Repo{
		sampleRepo("alpha", "/projects/go/alpha", "main", 1, true),
	}
	const msg = "cache save failed: /a/very/long/path/to/the/cache/file.json"
	for _, w := range []int{40, 55, 70, 85} {
		m := newTestModel(t, repos, "/projects")
		m.width = w
		m.height = 20
		m.scanning = false
		m.statusMsg = msg
		m.statusIsErr = true

		bar := m.statusBar()
		for _, line := range strings.Split(bar, "\n") {
			if lipgloss.Width(line) > w-statusBarPadding {
				t.Errorf("width=%d: line is %d visible cells, budget %d:\n%q",
					w, lipgloss.Width(line), w-statusBarPadding, line)
			}
		}
		// Clipped, so the head shows and the tail does not.
		if !strings.Contains(bar, "cache") {
			t.Errorf("width=%d: message head missing:\n%q", w, bar)
		}
		if strings.Contains(bar, "file.json") {
			t.Errorf("width=%d: message should be clipped at this width:\n%q", w, bar)
		}
	}
}
