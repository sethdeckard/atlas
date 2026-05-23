// Package tui is the Bubble Tea front-end for atlas. The model holds an
// in-memory snapshot of the cache scoped to the active root, drives
// background refresh via a worker pool, and lets the user navigate to and
// edit each repo.
package tui

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sethdeckard/atlas/internal/cache"
	"github.com/sethdeckard/atlas/internal/config"
	"github.com/sethdeckard/atlas/internal/repo"
	"github.com/sethdeckard/atlas/internal/scan"
	"github.com/sethdeckard/atlas/internal/sysopen"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const (
	saveEveryNRefreshes = 10
	statusMessageTTL    = 3 * time.Second
)

// Model is the root tea.Model for atlas's TUI.
type Model struct {
	ctx context.Context

	cache     *cache.Cache
	cachePath string
	cfg       config.Config
	root      string

	// repos is the *filtered + sorted* repo set rendered to the table.
	// selected indexes into repos; selectedPath tracks the highlighted
	// repo's Path so navigation survives filter/sort/refresh rebuilds.
	repos        []repo.Repo
	selected     int
	selectedPath string

	// scanning is true while the initial discover is still in flight for
	// the active root (no warm cache entries to seed the table). Set in
	// New when the cache yields no scoped repos; cleared in
	// handleDiscovered. Drives the cold-cache message in View so users
	// see "Discovering..." instead of an empty table during the first
	// scan.
	scanning bool
	scrollOffset int
	width        int
	height       int

	// M3: filter / sort / group state.
	filterText    string
	filterMode    bool
	filterInput   textinput.Model
	sortBy   string // last_commit_at | repo
	sortDesc bool
	groupBy  string // top_dir | none (M4 widens to activity | language)

	// Refresh state machine.
	refreshGen         int
	refreshing         bool
	refreshDoneCount   int
	refreshTotal       int
	activeCh           <-chan repo.Repo
	pendingStatusPass  []repo.Repo
	refreshesSinceSave int
	// refreshCancel cancels the context handed to the active refresh
	// pool. Calling it unblocks any worker stuck on `out <- result`
	// (older generations whose results the model is no longer reading)
	// so superseded refreshes don't leak goroutines.
	refreshCancel context.CancelFunc

	// saves serializes async cache writes — at most one in flight at a
	// time, with a single freshest pending snapshot queued behind. See
	// save.go.
	saves saveCoordinator
	// quitPending is set when the user pressed q while a save was in
	// flight: we want the queue to drain (latest snapshot included)
	// before actually exiting, so handleCacheSaved fires tea.Quit once
	// the coordinator has nothing left to dispatch.
	quitPending bool

	statusMsg   string
	statusIsErr bool
	keys        keyMap
	styles      styles

	// cdTarget is set by handleEnter to the selected repo's Path. After
	// the program exits, tui.Run reads it and prints it on stdout so a
	// shell wrapper can `cd "$(atlas)"`.
	cdTarget string

	// warnings carries config-load warnings into the TUI; rendered in
	// the status bar count and the ? help overlay (G.5).
	warnings []string

	// M5: detail pane state.
	//
	// recentCommits is the per-repo lazy-load lifecycle for the
	// detail pane's commit subjects. The state struct has explicit
	// loading/loaded/err fields so the renderer can distinguish "fetch
	// in flight" from "loaded with no commits" (the empty-repo case)
	// from "loaded with an error". A missing map entry means the path
	// has never been requested. Invalidated on repoRefreshedMsg for
	// the same path so a refresh-and-open flow doesn't show stale
	// subjects.
	//
	// recentGen is bumped on every selection change (including ones
	// that land on already-cached repos). The 150ms debounce tick
	// carries the gen it was scheduled with; only the latest gen
	// kicks off an actual fetch — fast j/k scrolling supersedes prior
	// ticks without spawning shellouts.
	recentCommits map[string]recentCommitsState
	recentGen     int

	// showHelp toggles the centered help overlay (?). Modal-feeling but
	// rendered as an inline replacement, not a separate window.
	showHelp bool

	// clipboard is swapped to a fake in tests so c (copy path) can be
	// asserted without touching the host clipboard. Production uses
	// defaultClipboard{} (atotto/clipboard).
	clipboard Clipboard
}

// New returns a model ready for tea.NewProgram. cache, cfg, and root should
// already be loaded/resolved by the caller (so the model can render
// immediately on first View). ctx is the long-lived program context, used
// to cancel refresh workers on shutdown.
//
// Sort and grouping start at hardcoded defaults (last_commit_at desc,
// activity grouping) and are overridden by `cache.Session` when it
// exists. Config doesn't carry these any more — the TUI's sticky
// session is the single source of truth across launches.
func New(ctx context.Context, c *cache.Cache, cachePath string, cfg config.Config, root string) Model {
	s := newStyles(config.NormalizeTheme(cfg.Theme))

	ti := textinput.New()
	ti.Prompt = "/"
	ti.CharLimit = 200
	ti.Placeholder = "type to filter (esc to clear)"
	// Paint the textinput's internal segments (prompt, placeholder,
	// typed text, cursor) with filterBarActive's background so the
	// gold extends across the whole line. Without this, bubbles
	// emits its own SGR codes that reset the background mid-line,
	// leaving a dark patch in the middle of the bar.
	bg := s.filterBarActive.GetBackground()
	fg := s.filterBarActive.GetForeground()
	ti.PromptStyle = lipgloss.NewStyle().Background(bg).Foreground(fg).Bold(true)
	ti.TextStyle = lipgloss.NewStyle().Background(bg).Foreground(fg)
	ti.PlaceholderStyle = lipgloss.NewStyle().Background(bg).Foreground(fg).Faint(true)
	ti.Cursor.Style = lipgloss.NewStyle().Background(fg).Foreground(bg)
	ti.Cursor.TextStyle = lipgloss.NewStyle().Background(fg).Foreground(bg)

	m := Model{
		ctx:           ctx,
		cache:         c,
		cachePath:     cachePath,
		cfg:           cfg,
		root:          root,
		keys:          newKeyMap(),
		styles:        s,
		filterInput:   ti,
		sortBy:        "last_commit_at",
		sortDesc:      true,
		groupBy:       "activity",
		recentCommits: make(map[string]recentCommitsState),
		clipboard:     defaultClipboard{},
	}
	if c != nil && c.Session != nil {
		if c.Session.SortBy != "" {
			m.sortBy = c.Session.SortBy
		}
		if c.Session.SortOrder != "" {
			m.sortDesc = c.Session.SortOrder == "desc"
		}
		if c.Session.GroupBy != "" {
			m.groupBy = c.Session.GroupBy
		}
	}
	(&m).rebuildRepos()
	m.scanning = len(m.repos) == 0
	return m
}

// Init dispatches the initial discover and an immediate kickoff
// message that asks Update to schedule the warm-launch detail-pane
// load. The kickoff indirection exists because Init has a value
// receiver — any mutations (e.g. bumping recentGen) made directly
// here are lost when Init returns, leaving any tick scheduled from
// Init permanently mismatching the live model's recentGen. Routing
// through a message lets Update mutate the live model.
func (m Model) Init() tea.Cmd {
	return tea.Batch(
		discoverCmd(m.ctx, m.root, m.scanOptions(), false),
		func() tea.Msg { return initialLoadMsg{} },
	)
}

// scanOptions builds the scan.Options from config. Shared by the launch
// discovery (Init) and the r-triggered refresh (startFullRefresh) so the
// two scans never drift on which dirs to skip or how deep to walk.
func (m Model) scanOptions() scan.Options {
	return scan.Options{
		SkipBaseNames: m.cfg.SkipBaseNames,
		SkipAbsPaths:  m.cfg.SkipAbsPaths,
		MaxDepth:      m.cfg.MaxDepth,
	}
}

// Update implements tea.Model.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		// Reserve room for everything that piles up around the
		// typed-text area inside the gold bar:
		//   - 2 cols for filterBarActive's Padding(0, 1)
		//   - prompt width (1 col for "/")
		//   - 1 col for the cursor cell that bubbles renders past
		//     the end of the value (visible width = ti.Width + 1
		//     whenever pos >= len(value), which is the typical
		//     trailing-cursor case while typing)
		// Otherwise the rendered bar exceeds the terminal width by
		// 1 col and the terminal wraps the filter row to two lines.
		m.filterInput.Width = msg.Width - 2 - lipgloss.Width(m.filterInput.Prompt) - 1
		if m.filterInput.Width < 1 {
			m.filterInput.Width = 1
		}
		m.scrollOffset = m.clampScroll(m.scrollOffset)
		m.scrollOffset = m.scrollIntoView(m.selected, m.scrollOffset)
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)

	case discoveredMsg:
		return m.handleDiscovered(msg)

	case refreshStartedMsg:
		return m.handleRefreshStarted(msg)

	case repoRefreshedMsg:
		return m.handleRepoRefreshed(msg)

	case refreshDoneMsg:
		return m.handleRefreshDone(msg)

	case cacheSavedMsg:
		var cmds []tea.Cmd
		if msg.err != nil {
			m.statusMsg = "cache save failed: " + msg.err.Error()
			m.statusIsErr = true
			cmds = append(cmds, clearStatusAfter(statusMessageTTL))
		}
		var next *cache.Cache
		m.saves, next = m.saves.Complete()
		if next != nil {
			cmds = append(cmds, saveCacheCmd(m.cachePath, next))
		} else if m.quitPending {
			// Queue drained — exit now that disk is up to date.
			cmds = append(cmds, tea.Quit)
		}
		return m, tea.Batch(cmds...)

	case clearStatusMsg:
		m.statusMsg = ""
		m.statusIsErr = false
		return m, nil

	case initialLoadMsg:
		// Warm-launch kickoff: schedule the detail-pane load against
		// the live model. Bumping recentGen here (rather than from
		// Init) is what makes the resulting tick's gen actually match
		// when it arrives.
		return m, (&m).scheduleRecentCommitsLoad()

	case recentCommitsTickMsg:
		// Debounced wakeup. If the user has moved selection since this
		// tick was scheduled, a newer tick supersedes us and we drop
		// out — no shellout for the intermediate selections during a
		// fast j/k scroll. Already-loaded entries short-circuit too;
		// re-fetching wastes a shellout for an answer we have.
		if msg.gen != m.recentGen {
			return m, nil
		}
		if state, ok := m.recentCommits[msg.path]; ok && (state.loaded || state.loading) {
			return m, nil
		}
		// Reserve the slot as "loading" so subsequent ticks for the
		// same path don't re-issue while the fetch is in flight.
		m.recentCommits[msg.path] = recentCommitsState{loading: true}
		return m, fetchRecentCommitsCmd(m.ctx, msg.path)

	case recentCommitsLoadedMsg:
		// Always transition to a loaded state — empty/nil lines for
		// empty repos and the error case both go through here so the
		// detail pane can show a definitive answer instead of stuck
		// "(loading…)".
		state := recentCommitsState{loaded: true, err: msg.err}
		if msg.err == nil {
			if msg.lines == nil {
				state.lines = []string{}
			} else {
				state.lines = append([]string(nil), msg.lines...)
			}
		}
		m.recentCommits[msg.path] = state
		return m, nil

	case errMsg:
		m.statusMsg = msg.err.Error()
		m.statusIsErr = true
		return m, clearStatusAfter(statusMessageTTL)
	}
	return m, nil
}

// detailPaneMinWidth is the threshold below which the detail pane is
// hidden — narrow terminals fall back to single-pane (M2 layout).
const detailPaneMinWidth = 100

// detailPaneWidth is the rendered width of the right-pane detail view
// when shown.
const detailPaneWidth = 36

// View renders the screen: status bar, body (table ± detail pane),
// bottom bar (hint or filter prompt). When the help overlay is up it
// replaces the body entirely.
func (m Model) View() string {
	width := m.width
	if width <= 0 {
		width = 80
	}
	rows := m.viewportRows()

	if m.showHelp {
		return m.viewHelp(width)
	}

	tableWidth, showDetail := paneWidths(width)
	var tableBody string
	switch {
	case m.selected >= 0:
		tableBody = renderTable(m.repos, m.root, m.groupBy, m.selected, m.scrollOffset, rows, tableWidth, m.styles)
	case m.scanning:
		tableBody = m.styles.row.Render(fmt.Sprintf(
			"Discovering repositories under %s...",
			config.ContractHome(m.root),
		))
	case m.filterText != "":
		// Render the column headers (sized to the *pre-filter*
		// repo set so widths match what the user just saw) and
		// place the "(no matches)" placeholder underneath. Keeps
		// the table structure visible so it's obvious the screen
		// hasn't lost the data — the filter just doesn't match.
		cols := chooseColumns(tableWidth, m.scopedRepos(), m.root, m.groupBy)
		header := m.styles.header.Render(formatRow(cols, headerCells(cols)))
		tableBody = header + "\n" + m.styles.row.Render("(no matches)")
	default:
		tableBody = m.styles.row.Render(emptyStateMessage(m.root, m.cfg.MaxDepth))
	}

	// Pad both panes to the full body height so the bottom hint
	// bar is anchored at terminal bottom regardless of how many
	// rows the table or detail pane has rendered. Without this,
	// a short list (or `(no matches)`, or refresh draining) lets
	// the bottom bar drift up.
	bodyHeight := rows + 1 // viewportRows + table header
	tableBody = padToHeight(tableBody, bodyHeight)

	var body string
	if showDetail {
		var selected *repo.Repo
		var recent recentCommitsState
		var siblings []repo.Repo
		if m.selected >= 0 && m.selected < len(m.repos) {
			r := m.repos[m.selected]
			selected = &r
			recent = m.recentCommits[r.Path]
			siblings = m.worktreeSiblings(r)
		}
		detailContent := renderDetail(selected, recent, siblings, detailPaneWidth-3, m.styles)
		detail := composeRightPane(detailContent, m.styles, bodyHeight)
		body = lipgloss.JoinHorizontal(
			lipgloss.Top,
			lipgloss.NewStyle().Width(tableWidth).Render(tableBody),
			m.styles.detailPane.Width(detailPaneWidth).Render(detail),
		)
	} else {
		body = tableBody
	}

	var b strings.Builder
	b.WriteString(m.renderStatusBar(width))
	b.WriteByte('\n')
	b.WriteString(m.renderFilterRow(width))
	b.WriteByte('\n')
	b.WriteString(body)
	b.WriteByte('\n')
	b.WriteString(m.bottomBar())
	return b.String()
}

// padToHeight returns s with enough trailing blank lines appended
// to reach exactly h lines. If s is already h lines or taller,
// it's returned unchanged.
func padToHeight(s string, h int) string {
	cur := lipgloss.Height(s)
	if cur >= h {
		return s
	}
	return s + strings.Repeat("\n", h-cur)
}

// composeRightPane lays out the detail-pane content with the flag
// legend bottom-anchored beneath it when bodyHeight has room for
// detail + at least one blank spacer + the full legend. If the budget
// is tight (short terminal, content-heavy detail pane), the legend is
// dropped entirely and the pane falls back to today's pad-to-fill
// behavior — graceful collapse, no partial legend.
func composeRightPane(detail string, s styles, bodyHeight int) string {
	detailH := lipgloss.Height(detail)
	if detailH+1+legendHeight > bodyHeight {
		return padToHeight(detail, bodyHeight)
	}
	// blanks = visible empty rows between detail and legend. The
	// separator string itself needs blanks+1 newlines: one to
	// terminate detail's last line, plus one per blank row.
	blanks := bodyHeight - detailH - legendHeight
	return detail + strings.Repeat("\n", blanks+1) + renderLegend(s)
}

// renderStatusBar produces the top status bar block. Always plain
// rendering — filter context lives on its own dedicated row below
// the status bar (see renderFilterRow).
func (m Model) renderStatusBar(width int) string {
	return m.styles.statusBar.Width(width).Render(m.statusBar())
}

// renderFilterRow owns the row between the status bar and the
// table. Three states:
//
//   - filterMode open → gold bar with the live input.
//   - filterText set + mode closed → gold bar with
//     "filter: <text> · esc to clear" so the user always knows a
//     filter is applied and how to remove it.
//   - no filter → blank row, intentional breathing room between
//     the status bar and the column headers.
//
// Always one line tall regardless of state, so viewportRows can
// subtract a constant. The applied-filter label truncates the
// filter text with an ellipsis if it would push the chip wider
// than the terminal — otherwise lipgloss would wrap the row to
// multiple physical lines and break the fixed-height layout.
func (m Model) renderFilterRow(width int) string {
	switch {
	case m.filterMode:
		return m.styles.filterBarActive.Width(width).Render(m.filterInput.View())
	case m.filterText != "":
		const prefix = "filter: "
		const suffix = " · esc to clear"
		// 2 cols for filterBarActive's Padding(0, 1). At widths
		// below that the padded bar would itself overflow, so
		// fall back to a blank row — the chip just disappears at
		// pathologically narrow widths rather than wrapping.
		available := width - 2
		if available < 1 {
			return strings.Repeat(" ", width)
		}
		textSpace := available - lipgloss.Width(prefix) - lipgloss.Width(suffix)
		var label string
		switch {
		case textSpace >= 1:
			text := m.filterText
			if lipgloss.Width(text) > textSpace {
				text = truncateToWidth(text, textSpace)
			}
			label = prefix + text + suffix
		default:
			// Terminal too narrow for the full chrome. Drop the
			// "esc to clear" suffix and truncate whatever's left
			// (which may eat into the prefix) so the chip still
			// fits on one line. The bottom-bar hints still
			// advertise the clear key.
			label = truncateToWidth(prefix+m.filterText, available)
		}
		return m.styles.filterBarActive.Width(width).Render(label)
	default:
		return strings.Repeat(" ", width)
	}
}

// truncateToWidth returns the longest prefix of s whose rendered
// width fits in max cells, with the last char replaced by "…" to
// signal truncation. Returns "" if max is non-positive.
func truncateToWidth(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= max {
		return s
	}
	runes := []rune(s)
	for len(runes) > 0 {
		candidate := string(runes) + "…"
		if lipgloss.Width(candidate) <= max {
			return candidate
		}
		runes = runes[:len(runes)-1]
	}
	return "…"
}

// emptyStateMessage renders the multi-line copy shown when an initial
// discover under root completed with zero repositories. Surfaces the
// two knobs most likely responsible — max_depth and skip_dirs — so a
// first-time user knows where to look without leaving the TUI.
func emptyStateMessage(root string, maxDepth int) string {
	return fmt.Sprintf(
		"No repositories found under %s.\nTry increasing max_depth (currently %d) or\nreview skip_dirs in ~/.config/atlas/config.toml.",
		config.ContractHome(root),
		maxDepth,
	)
}

// paneWidths returns the table width and whether the detail pane should
// be rendered. Below detailPaneMinWidth we collapse to a single pane.
func paneWidths(termWidth int) (tableWidth int, showDetail bool) {
	if termWidth < detailPaneMinWidth {
		return termWidth, false
	}
	return termWidth - detailPaneWidth, true
}

// viewHelp renders a centered help overlay listing every keymap entry.
// Each row is sourced from a key.Binding Help() value — content stays
// in lockstep with keys.go — but the order/inclusion is curated here.
// When adding a new binding to keys.go, also add it to the list below.
func (m Model) viewHelp(width int) string {
	var b strings.Builder
	b.WriteString("atlas — keys\n\n")
	rows := []key.Binding{
		m.keys.Up, m.keys.Down, m.keys.JumpTop, m.keys.JumpBottom,
		m.keys.HalfUp, m.keys.HalfDown,
		m.keys.Filter, m.keys.FilterCancel,
		m.keys.SortCycle, m.keys.SortReverse,
		m.keys.GroupCycle, m.keys.CopyPath,
		m.keys.OpenOrigin, m.keys.Refresh,
		m.keys.Help, m.keys.Quit,
		// Enter's desc ("cd into repo & exit") is the longest in the
		// list — render it as the trailing solo row so its width
		// never feeds back into either column's padding budget and
		// stretches the overlay.
		m.keys.Enter,
	}
	// Column widths driven by actual help-text length, not fixed
	// padding — keeps the 2-col layout as tight as the data allows,
	// and lets us decide whether it fits the terminal before
	// rendering. Paired (left, right) so the longest right-side desc
	// is unbounded.
	keyW, leftW, twoColW := helpColumnWidths(rows)
	// helpOverlay = rounded border (2 cols) + Padding(1, 2) (4 cols)
	// = 6 cols of chrome that the content has to share with the
	// terminal width.
	const helpBoxOverhead = 6
	twoCol := width >= twoColW+helpBoxOverhead
	// Bold the key with raw SGR codes rather than
	// m.styles.hintKey.Render: lipgloss closes each styled segment
	// with `\x1b[0m`, which would reset the helpOverlay's background
	// mid-line and let the terminal default bleed through the
	// descriptions. `\x1b[22m` turns off bold without disturbing the
	// surrounding background.
	const (
		ansiBold    = "\x1b[1m"
		ansiBoldOff = "\x1b[22m"
	)
	// Pad the key to its column width *before* styling so the bold
	// escapes don't get counted toward fmt's width budget.
	styledKey := func(k string, w int) string {
		return ansiBold + fmt.Sprintf("%-*s", w, k) + ansiBoldOff
	}
	if twoCol {
		for i := 0; i < len(rows); i += 2 {
			left := rows[i]
			if i+1 < len(rows) {
				right := rows[i+1]
				fmt.Fprintf(&b, "  %s %-*s %s %s\n",
					styledKey(left.Help().Key, keyW), leftW, left.Help().Desc,
					styledKey(right.Help().Key, keyW), right.Help().Desc)
			} else {
				fmt.Fprintf(&b, "  %s %s\n", styledKey(left.Help().Key, keyW), left.Help().Desc)
			}
		}
	} else {
		for _, kb := range rows {
			fmt.Fprintf(&b, "  %s %s\n", styledKey(kb.Help().Key, keyW), kb.Help().Desc)
		}
	}
	if len(m.warnings) > 0 {
		b.WriteString("\nWarnings:\n")
		for _, w := range m.warnings {
			b.WriteString("  " + w + "\n")
		}
	}
	b.WriteString("\nFlags:\n")
	for _, line := range legendEntries() {
		b.WriteString("  " + line + "\n")
	}
	b.WriteString("\nPress esc, q, or ? to close this help.")
	box := m.styles.helpOverlay.Render(b.String())
	height := m.height
	if height <= 0 {
		height = 24
	}
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, box)
}

// helpColumnWidths returns the (key, left-desc) column widths and the
// total 2-col content width — driven by the longest key, longest
// even-index (left-column) desc, and longest odd-index (right-column)
// desc actually present in the keymap. The 2-col layout is:
//
//	"  " + key(keyW) + " " + desc(leftW) + " " + key(keyW) + " " + desc(rightW)
//
// twoColW counts those segments. viewHelp uses it to decide whether
// the 2-col layout fits the terminal width — if not, it falls back to
// a 1-col layout so the overlay never overruns the terminal edge.
func helpColumnWidths(rows []key.Binding) (keyW, leftW, twoColW int) {
	var rightW int
	for i, kb := range rows {
		help := kb.Help()
		if w := lipgloss.Width(help.Key); w > keyW {
			keyW = w
		}
		desc := lipgloss.Width(help.Desc)
		switch {
		case i%2 == 0 && desc > leftW:
			leftW = desc
		case i%2 == 1 && desc > rightW:
			rightW = desc
		}
	}
	// 2 (indent) + key + 1 + leftDesc + 1 + key + 1 + rightDesc
	twoColW = 2 + keyW + 1 + leftW + 1 + keyW + 1 + rightW
	return keyW, leftW, twoColW
}

// bottomBar renders the key hint row. The filter input lives on
// the status bar's first line during filter mode (see
// renderStatusBar), not here.
func (m Model) bottomBar() string {
	return m.hintBar()
}

// viewportRows is the number of body rows the table can show — the
// terminal height minus the status bar (which may wrap), the
// dedicated filter row, the bottom hint bar, and the table header.
// The filter row is always reserved so the subtraction is constant
// regardless of filter state.
func (m Model) viewportRows() int {
	body := m.height - m.statusBarHeight() - 1 - 1 // -filter row, -bottom bar
	if body < 1 {
		body = 1
	}
	rows := body - 1 // table header line
	if rows < 1 {
		rows = 1
	}
	return rows
}

// scrollIntoView returns a scrollOffset that keeps the selected repo's
// rendered row visible. selected is an index into m.repos; offset is in
// render-row units (which may include group headers).
func (m Model) scrollIntoView(selected, offset int) int {
	if selected < 0 || len(m.repos) == 0 {
		return 0
	}
	target := renderRowOfRepo(m.repos, m.root, m.groupBy, selected)
	if target < 0 {
		return m.clampScroll(offset)
	}
	rows := m.viewportRows()
	if target < offset {
		offset = target
	} else if target >= offset+rows {
		offset = target - rows + 1
	}
	// When we scroll up to a repo that has a group header above it,
	// nudge offset up by 1 so the header is also visible.
	if offset > 0 && target == offset {
		// Look at the previous render row — if it's a header, include it.
		all := buildRenderRows(m.repos, m.root, m.groupBy)
		if offset-1 < len(all) && all[offset-1].kind == rowGroup {
			offset--
		}
	}
	return m.clampScroll(offset)
}

func (m Model) clampScroll(offset int) int {
	rows := m.viewportRows()
	total := renderRowsForRepos(m.repos, m.root, m.groupBy)
	max := total - rows
	if max < 0 {
		max = 0
	}
	if offset > max {
		offset = max
	}
	if offset < 0 {
		offset = 0
	}
	return offset
}

// ---- Key handling ----

// keyCopy / keyOpenOrigin are local key.Binding values. Unlike the
// keymap fields these aren't shown in the bottom hint bar today; they
// are documented in the ? help overlay (G.5) and stay alongside the
// other M5 affordances here so wiring them stays in one place.

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.filterMode {
		return m.handleFilterKey(msg)
	}
	if m.showHelp {
		return m.handleHelpKey(msg)
	}
	switch {
	case msg.Type == tea.KeyEsc && m.filterText != "":
		// Outside filter mode, esc clears an applied filter
		// (matching the chip's "esc to clear" affordance). esc with
		// no filter applied is intentionally a no-op — only q and
		// ctrl+c exit the program.
		return m.clearFilter()

	case key.Matches(msg, m.keys.Quit):
		// If a save is already in flight, defer the exit: park the latest
		// snapshot via the coordinator and let handleCacheSaved fire
		// tea.Quit once the queue drains. Quitting now would race with
		// the in-flight rename and could leave stale data on disk.
		if m.saves.InFlight() {
			m.saves, _ = m.saves.Request(m.cache.Snapshot())
			m.quitPending = true
			return m, nil
		}
		// No in-flight save → sync save and quit. Failures don't block
		// exit; cache is best-effort.
		if err := cache.Save(m.cachePath, m.cache.Snapshot()); err != nil {
			_ = err
		}
		return m, tea.Quit

	case key.Matches(msg, m.keys.Up):
		return m.moveSelection(-1)

	case key.Matches(msg, m.keys.Down):
		return m.moveSelection(1)

	case key.Matches(msg, m.keys.HalfUp):
		return m.moveSelection(-m.viewportRows() / 2)

	case key.Matches(msg, m.keys.HalfDown):
		return m.moveSelection(m.viewportRows() / 2)

	case key.Matches(msg, m.keys.JumpTop):
		return m.jumpTo(0)

	case key.Matches(msg, m.keys.JumpBottom):
		return m.jumpTo(len(m.repos) - 1)

	case key.Matches(msg, m.keys.Filter):
		return m.enterFilterMode(), nil

	case key.Matches(msg, m.keys.SortCycle):
		return m.cycleSort()

	case key.Matches(msg, m.keys.SortReverse):
		return m.toggleSortDirection()

	case key.Matches(msg, m.keys.GroupCycle):
		return m.cycleGroup()

	case key.Matches(msg, m.keys.CopyPath):
		return m.copySelectedPath()

	case key.Matches(msg, m.keys.OpenOrigin):
		return m.openSelectedOrigin()

	case key.Matches(msg, m.keys.Help):
		m.showHelp = true
		return m, nil

	case key.Matches(msg, m.keys.Enter):
		return m.handleEnter()

	case key.Matches(msg, m.keys.Refresh):
		return m.startFullRefresh()
	}
	return m, nil
}

// handleHelpKey routes input while the help overlay is up: any
// recognized key (?, esc, q) closes it, others fall through to the
// normal key handler so users can keep typing without first dismissing.
func (m Model) handleHelpKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Help),
		msg.Type == tea.KeyEsc,
		msg.Type == tea.KeyRunes && len(msg.Runes) == 1 && msg.Runes[0] == 'q':
		m.showHelp = false
		return m, nil
	}
	return m, nil
}

// copySelectedPath copies the highlighted repo's absolute path to the
// system clipboard via the model's Clipboard interface (swappable in
// tests). The result surfaces in the status bar — failures don't
// interrupt anything else.
func (m Model) copySelectedPath() (tea.Model, tea.Cmd) {
	if len(m.repos) == 0 || m.selected < 0 {
		m.statusMsg = "no selection"
		m.statusIsErr = true
		return m, clearStatusAfter(statusMessageTTL)
	}
	path := m.repos[m.selected].Path
	if err := m.clipboard.Write(path); err != nil {
		m.statusMsg = "copy failed: " + err.Error()
		m.statusIsErr = true
		return m, clearStatusAfter(statusMessageTTL)
	}
	m.statusMsg = "copied path"
	m.statusIsErr = false
	return m, clearStatusAfter(statusMessageTTL)
}

// openSelectedOrigin runs the origin URL through sysopen.BrowserURL +
// the OS's default browser command. Empty / SSH-form / unbrowsable
// origins surface as a status message rather than failing the program.
func (m Model) openSelectedOrigin() (tea.Model, tea.Cmd) {
	if len(m.repos) == 0 || m.selected < 0 {
		m.statusMsg = "no selection"
		m.statusIsErr = true
		return m, clearStatusAfter(statusMessageTTL)
	}
	origin := m.repos[m.selected].OriginURL
	if origin == "" {
		m.statusMsg = "no origin URL"
		m.statusIsErr = true
		return m, clearStatusAfter(statusMessageTTL)
	}
	if err := sysopen.Open(origin); err != nil {
		m.statusMsg = "open: " + err.Error()
		m.statusIsErr = true
		return m, clearStatusAfter(statusMessageTTL)
	}
	m.statusMsg = "opened origin"
	m.statusIsErr = false
	return m, clearStatusAfter(statusMessageTTL)
}

// moveSelection shifts m.selected by delta (clamped) and updates
// selectedPath + scroll. delta of 0 is a no-op. -1 = up; +1 = down;
// negative half-page = up half-page; etc.
func (m Model) moveSelection(delta int) (Model, tea.Cmd) {
	if len(m.repos) == 0 || m.selected < 0 {
		return m, nil
	}
	target := m.selected + delta
	if target < 0 {
		target = 0
	}
	if target >= len(m.repos) {
		target = len(m.repos) - 1
	}
	prev := m.selectedPath
	m.selected = target
	m.selectedPath = m.repos[m.selected].Path
	m.scrollOffset = m.scrollIntoView(m.selected, m.scrollOffset)
	if prev != m.selectedPath {
		return m, m.scheduleRecentCommitsLoad()
	}
	return m, nil
}

// jumpTo sets selected directly (clamped) and scrolls into view.
func (m Model) jumpTo(idx int) (Model, tea.Cmd) {
	if len(m.repos) == 0 || m.selected < 0 {
		return m, nil
	}
	if idx < 0 {
		idx = 0
	}
	if idx >= len(m.repos) {
		idx = len(m.repos) - 1
	}
	prev := m.selectedPath
	m.selected = idx
	m.selectedPath = m.repos[m.selected].Path
	m.scrollOffset = m.scrollIntoView(m.selected, m.scrollOffset)
	if prev != m.selectedPath {
		return m, m.scheduleRecentCommitsLoad()
	}
	return m, nil
}

// scheduleRecentCommitsLoad bumps recentGen and (when the selected
// repo is uncached) schedules a debounced fetch tick. Fast scrolling
// supersedes earlier ticks because each new selection bumps recentGen,
// and the tick handler ignores any tick whose gen is no longer current.
//
// Importantly, recentGen is bumped on *every* call — even when the
// selection lands on an already-cached repo. Otherwise an in-flight
// tick scheduled for the previous (uncached) repo would still match
// the unchanged gen and run a git log for a repo the user has already
// moved away from.
func (m *Model) scheduleRecentCommitsLoad() tea.Cmd {
	if m.selected < 0 || m.selected >= len(m.repos) {
		return nil
	}
	m.recentGen++
	path := m.repos[m.selected].Path
	// Already loaded or in flight → no need to schedule another fetch
	// for this path. recentGen still bumped above so any older
	// in-flight tick (for the previous selection) won't dispatch when
	// it fires.
	if state, ok := m.recentCommits[path]; ok && (state.loaded || state.loading) {
		return nil
	}
	return recentCommitsTickCmd(150*time.Millisecond, m.recentGen, path)
}

// enterFilterMode focuses the filter input and switches input routing.
// The filter text is preserved across enter/exit cycles — esc clears it.
func (m Model) enterFilterMode() Model {
	m.filterMode = true
	m.filterInput.SetValue(m.filterText)
	m.filterInput.Focus()
	m.filterInput.CursorEnd()
	return m
}

// handleFilterKey routes key events while filterMode == true. Enter exits
// the input but keeps the active filter; esc exits AND clears; ctrl+c
// quits the program. Everything else is forwarded to the bubbles textinput.
func (m Model) handleFilterKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyCtrlC:
		// Match the global Quit semantics — let the normal quit path
		// run by re-invoking handleKey with filterMode off.
		m.filterMode = false
		m.filterInput.Blur()
		return m.handleKey(msg)
	case tea.KeyEnter:
		m.filterMode = false
		m.filterInput.Blur()
		m.filterText = strings.TrimSpace(m.filterInput.Value())
		return m, (&m).rebuildRepos()
	case tea.KeyEsc:
		return m.clearFilter()
	}
	var cmd tea.Cmd
	m.filterInput, cmd = m.filterInput.Update(msg)
	// Live filter — rebuild on every keystroke.
	m.filterText = strings.TrimSpace(m.filterInput.Value())
	return m, tea.Batch(cmd, (&m).rebuildRepos())
}

// clearFilter resets all filter state and rebuilds the repo list.
// Shared by the in-filter-mode esc path and the out-of-filter-mode
// esc-cascade path in handleKey.
func (m Model) clearFilter() (Model, tea.Cmd) {
	m.filterMode = false
	m.filterInput.Blur()
	m.filterInput.SetValue("")
	m.filterText = ""
	return m, (&m).rebuildRepos()
}

// cycleSort toggles sortBy between last_commit_at and repo. The visible
// REPO column drives the second mode — sorting by anything else (e.g.
// the old `name` and `path` keys) produced orderings the user couldn't
// reconcile with what was on screen, so the cycle is intentionally
// minimal. The persistent `sort: …` segment in the status bar (built in
// statusBar) reflects m.sortBy/m.sortDesc immediately, so there's no
// need to also write a transient statusMsg — doing so duplicates the
// segment side-by-side until something else overwrites statusMsg.
//
// The new sort is persisted into m.cache.Session and flushed via the
// save coordinator so the next launch comes back with the same sort.
func (m Model) cycleSort() (Model, tea.Cmd) {
	if m.sortBy == "last_commit_at" {
		m.sortBy = "repo"
	} else {
		m.sortBy = "last_commit_at"
	}
	rebuildCmd := (&m).rebuildRepos()
	m.recordSession()
	var saveCmd tea.Cmd
	m, saveCmd = m.requestSave()
	return m, tea.Batch(rebuildCmd, saveCmd)
}

// toggleSortDirection flips sortDesc, rebuilds, and persists the new
// direction so the next launch comes back the same way. As with
// cycleSort, the persistent `sort: …` status-bar segment already shows
// the new direction; no transient statusMsg needed.
func (m Model) toggleSortDirection() (Model, tea.Cmd) {
	m.sortDesc = !m.sortDesc
	rebuildCmd := (&m).rebuildRepos()
	m.recordSession()
	var saveCmd tea.Cmd
	m, saveCmd = m.requestSave()
	return m, tea.Batch(rebuildCmd, saveCmd)
}

// recordSession copies the current sort/group state onto
// m.cache.Session so the next call to requestSave persists it. The
// pointer-receiver intent is captured by mutating m.cache directly —
// Cache is a *cache.Cache, so the receiver chain doesn't need a
// pointer here.
func (m *Model) recordSession() {
	if m.cache == nil {
		return
	}
	if m.cache.Session == nil {
		m.cache.Session = &cache.Session{}
	}
	m.cache.Session.SortBy = m.sortBy
	if m.sortDesc {
		m.cache.Session.SortOrder = "desc"
	} else {
		m.cache.Session.SortOrder = "asc"
	}
	m.cache.Session.GroupBy = m.groupBy
}

// cycleGroup advances the group-by mode through:
//
//	activity → top_dir → language → worktree → none → activity
//
// The order is chosen so that one press from the smart default
// ("activity") returns the M3-era layout ("top_dir"), and the cycle
// always lands on something visible — the final none step disables
// grouping but is reachable in four presses, so a separate on/off
// toggle isn't needed. The "worktree" step renders each multi-worktree
// project as a tree (primary checkout anchoring its linked worktrees);
// see buildWorktreeRows.
//
// Group changes mutate the visible repo *order* (rebuildRepos clusters
// same-key repos when groupBy != "none"), so the rebuild is required.
// The new mode is persisted into m.cache.Session and flushed via the
// save coordinator so the next launch comes back with the same mode.
func (m Model) cycleGroup() (Model, tea.Cmd) {
	switch m.groupBy {
	case "activity":
		m.groupBy = "top_dir"
	case "top_dir":
		m.groupBy = "language"
	case "language":
		m.groupBy = "worktree"
	case "worktree":
		m.groupBy = "none"
	default:
		m.groupBy = "activity"
	}
	rebuildCmd := (&m).rebuildRepos()
	m.statusMsg = "group: " + m.groupBy
	m.recordSession()
	var saveCmd tea.Cmd
	m, saveCmd = m.requestSave()
	return m, tea.Batch(rebuildCmd, saveCmd)
}

func sortArrow(desc bool) string {
	if desc {
		return "↓"
	}
	return "↑"
}

// handleEnter sets cdTarget to the selected repo's path and quits the
// program. tui.Run prints cdTarget on stdout after the program exits,
// so a shell wrapper can `cd "$(atlas)"`.
//
// The exit path mirrors the q-handler's save drain: if a save is in
// flight (e.g. the user just cycled sort or grouping and the sticky-
// session write hasn't landed yet), we park the latest snapshot and
// let handleCacheSaved fire tea.Quit when the queue drains. Quitting
// immediately would race the rename and could lose the just-applied
// sticky state.
func (m Model) handleEnter() (tea.Model, tea.Cmd) {
	if len(m.repos) == 0 {
		return m, nil
	}
	m.cdTarget = m.repos[m.selected].Path
	if m.saves.InFlight() {
		m.saves, _ = m.saves.Request(m.cache.Snapshot())
		m.quitPending = true
		return m, nil
	}
	if err := cache.Save(m.cachePath, m.cache.Snapshot()); err != nil {
		_ = err
	}
	return m, tea.Quit
}

// beginRefreshPhase cancels any prior phase's worker context, allocates a
// fresh cancellable child of m.ctx, bumps the generation counter, and
// resets the per-phase counters. Every refresh start site goes through
// here so the worker pools never outlive their relevance — pressing R
// mid-stream unblocks the previous pool's goroutines instead of leaking
// them.
func (m Model) beginRefreshPhase(total int) (Model, context.Context) {
	if m.refreshCancel != nil {
		m.refreshCancel()
	}
	ctx, cancel := context.WithCancel(m.ctx)
	m.refreshCancel = cancel
	m.refreshGen++
	m.refreshing = true
	m.refreshDoneCount = 0
	m.refreshTotal = total
	return m, ctx
}

// ---- Refresh state machine ----

func (m Model) handleDiscovered(msg discoveredMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.statusMsg = "discover: " + msg.err.Error()
		m.statusIsErr = true
	}
	// Reconcile prunes gone-from-disk entries and returns the
	// classification both the CLI pipeline and the TUI need on every
	// discover. TUI carries full Repo values into the status pass (the
	// status updater needs the cached fields), so we materialize the
	// repos here from the statusOnly paths. forceFull (the r refresh)
	// passes through as Reconcile's fresh flag so every surviving repo is
	// force re-read; launch discovery (forceFull=false) stays incremental.
	stale, statusOnly := m.cache.Reconcile(m.root, msg.paths, msg.forceFull)
	statusPass := make([]repo.Repo, 0, len(statusOnly))
	for _, p := range statusOnly {
		if r, ok := m.cache.Repos[p]; ok {
			statusPass = append(statusPass, r)
		}
	}
	rebuildCmd := (&m).rebuildRepos()

	if len(stale) == 0 && len(statusPass) == 0 {
		// Discovery is final and no refresh work follows — what's in
		// m.repos right now is the whole story. Clear scanning so the
		// View switches from "Discovering..." to either the table or
		// the empty-state hint. Also clear the refresh flags: an r
		// refresh that discovers zero repos (e.g. every repo deleted)
		// set refreshing=true up front, so without this the status bar
		// would stay stuck on "[refreshing]".
		m.scanning = false
		m.refreshing = false
		m.refreshTotal = 0
		m.refreshDoneCount = 0
		return m, rebuildCmd
	}
	m.pendingStatusPass = statusPass
	if len(stale) > 0 {
		var ctx context.Context
		m, ctx = m.beginRefreshPhase(len(stale))
		return m, tea.Batch(rebuildCmd, startRefreshCmd(ctx, m.refreshGen, stale))
	}
	// Skip directly to phase 2 (status-only) when nothing is stale.
	mm, statusCmd := m.startStatusPass()
	return mm, tea.Batch(rebuildCmd, statusCmd)
}

func (m Model) startFullRefresh() (tea.Model, tea.Cmd) {
	// "Refresh all" re-scans the filesystem rather than re-reading the
	// cached path set: a fresh discovery is what lets handleDiscovered's
	// Reconcile prune repos deleted since launch (otherwise they'd be
	// re-read into Err records and linger as tombstoned rows) and pick up
	// newly-added ones. forceFull flows into Reconcile's fresh parameter so
	// every surviving repo is force re-read, not just the mtime-stale ones.
	//
	// Invalidate any in-flight refresh up front: cancel its worker context
	// and bump the generation so superseded repoRefreshedMsg are dropped on
	// arrival. Without this, results from the prior refresh could land
	// during the discovery window — and if the forced scan finds nothing
	// (the "nothing to do" branch never reaches beginRefreshPhase) a stale
	// worker result would reinsert a repo that Reconcile just pruned.
	// refreshTotal is established later in handleDiscovered/beginRefreshPhase;
	// setting refreshing here gives immediate status-bar feedback during the
	// brief discovery window.
	if m.refreshCancel != nil {
		m.refreshCancel()
		m.refreshCancel = nil
	}
	m.refreshGen++
	m.pendingStatusPass = nil
	m.activeCh = nil
	m.refreshing = true
	m.refreshDoneCount = 0
	m.refreshTotal = 0
	return m, discoverCmd(m.ctx, m.root, m.scanOptions(), true)
}

func (m Model) handleRefreshStarted(msg refreshStartedMsg) (tea.Model, tea.Cmd) {
	if msg.gen != m.refreshGen {
		return m, nil
	}
	m.refreshTotal = msg.total
	m.activeCh = msg.ch
	if msg.total == 0 {
		return m.advanceAfterPhase()
	}
	return m, nextRefreshCmd(msg.ch, msg.gen)
}

func (m Model) handleRepoRefreshed(msg repoRefreshedMsg) (tea.Model, tea.Cmd) {
	if msg.gen != m.refreshGen {
		return m, nil
	}
	// First refreshed row implies the cold-launch "discovering" phase is
	// over, even if rebuildRepos hasn't surfaced rows yet. Always cheap.
	m.scanning = false
	m.cache.Repos[msg.repo.Path] = msg.repo
	// A refresh may have produced new commits — drop the cached
	// recent-subjects so the next selection re-fetches fresh data.
	delete(m.recentCommits, msg.repo.Path)
	rebuildCmd := (&m).rebuildRepos()
	m.refreshDoneCount++
	m.refreshesSinceSave++

	cmds := []tea.Cmd{}
	if rebuildCmd != nil {
		cmds = append(cmds, rebuildCmd)
	}
	if m.refreshesSinceSave >= saveEveryNRefreshes {
		m.refreshesSinceSave = 0
		var saveCmd tea.Cmd
		m, saveCmd = m.requestSave()
		if saveCmd != nil {
			cmds = append(cmds, saveCmd)
		}
	}
	cmds = append(cmds, m.continueRefresh(msg.gen))
	return m, tea.Batch(cmds...)
}

// requestSave hands the latest cache snapshot to the saveCoordinator. If no
// save is in flight, the returned cmd dispatches it; otherwise the
// snapshot is parked and dispatched later by handleCacheSaved.
//
// Snapshots are taken now (not when the cmd runs) so the async marshal can
// never race with concurrent map writes.
func (m Model) requestSave() (Model, tea.Cmd) {
	var snap *cache.Cache
	m.saves, snap = m.saves.Request(m.cache.Snapshot())
	if snap == nil {
		return m, nil
	}
	return m, saveCacheCmd(m.cachePath, snap)
}

func (m Model) handleRefreshDone(msg refreshDoneMsg) (tea.Model, tea.Cmd) {
	if msg.gen != m.refreshGen {
		return m, nil
	}
	return m.advanceAfterPhase()
}

// advanceAfterPhase runs the next refresh phase (status-only) if pending,
// otherwise persists the cache and clears the refreshing flag.
func (m Model) advanceAfterPhase() (tea.Model, tea.Cmd) {
	m.activeCh = nil
	if len(m.pendingStatusPass) > 0 {
		return m.startStatusPass()
	}
	// All refresh phases complete — the cold-launch "discovering" mode
	// is definitively over even if no rows surfaced (every read errored,
	// or the refresh found nothing to update). Without this clear the
	// View would stay on "Discovering..." forever in those edge cases.
	m.scanning = false
	m.refreshing = false
	m.refreshTotal = 0
	m.refreshDoneCount = 0
	m.refreshesSinceSave = 0
	if m.refreshCancel != nil {
		// Workers have already finished naturally; calling cancel is a
		// no-op for them but releases the context's resources.
		m.refreshCancel()
		m.refreshCancel = nil
	}
	var cmd tea.Cmd
	m, cmd = m.requestSave()
	return m, cmd
}

func (m Model) startStatusPass() (tea.Model, tea.Cmd) {
	cached := m.pendingStatusPass
	m.pendingStatusPass = nil
	var ctx context.Context
	m, ctx = m.beginRefreshPhase(len(cached))
	return m, startStatusRefreshCmd(ctx, m.refreshGen, cached, repo.UpdateStatus)
}

// continueRefresh re-issues nextRefreshCmd for the active gen by reading
// from the model's own activeCh — but since channels are passed via msg, we
// need a different mechanism. We piggyback on the message: the channel is
// re-discovered from refreshStartedMsg, but here we don't have it. So
// continueRefresh is implemented by issuing a small synthetic command that
// retrieves the channel from a model-level slot. Since Update is allowed
// to mutate state, we stash the channel on refreshStartedMsg.
//
// Implementation note: refreshStartedMsg is consumed once; we re-issue
// nextRefreshCmd from there. Subsequent repoRefreshedMsg handlers use the
// channel stored on the model. To keep this honest we stash it on the model
// in handleRefreshStarted and read it here.
func (m Model) continueRefresh(gen int) tea.Cmd {
	if m.activeCh == nil {
		return nil
	}
	return nextRefreshCmd(m.activeCh, gen)
}

// ---- Helpers ----

// scopedRepos returns the cache entries under root, sorted by the model's
// configured sort. Filter is *not* applied here — callers that want the
// rendered set call rebuildRepos.
func (m Model) scopedRepos() []repo.Repo {
	sep := string(filepath.Separator)
	var out []repo.Repo
	for path, r := range m.cache.Repos {
		if path == m.root || strings.HasPrefix(path, m.root+sep) {
			out = append(out, r)
		}
	}
	repo.Sort(out, m.sortBy, m.sortDesc, m.root)
	return out
}

// worktreeSiblings returns every worktree of `r`'s project, ordered
// primary-first then most-recent-first, for the detail pane's roster.
// It annotates a fresh scoped slice (same pattern as statusBarParts) so
// the roster reflects the real project even when the active filter
// hides some siblings. Returns nil for a solo repo (WorktreeCount <= 1)
// or when CommonGitDir is unknown.
func (m Model) worktreeSiblings(r repo.Repo) []repo.Repo {
	if r.WorktreeCount <= 1 || r.CommonGitDir == "" {
		return nil
	}
	scoped := m.scopedRepos()
	repo.AnnotateDerived(scoped, m.cfg.StaleDays, nowFunc())
	var fam []repo.Repo
	for _, s := range scoped {
		if s.CommonGitDir == r.CommonGitDir {
			fam = append(fam, s)
		}
	}
	sort.SliceStable(fam, func(i, j int) bool {
		if fam[i].PrimaryWorktree != fam[j].PrimaryWorktree {
			return fam[i].PrimaryWorktree
		}
		ti, tj := fam[i].LastCommitAt, fam[j].LastCommitAt
		switch {
		case ti == nil && tj == nil:
			return false
		case ti == nil:
			return false
		case tj == nil:
			return true
		default:
			return ti.After(*tj)
		}
	})
	return fam
}

// rebuildRepos recomputes m.repos from the cache, applies the active fuzzy
// filter, sorts, annotates derived signals, and (when groupBy != "none")
// clusters same-key repos so each group appears as a single contiguous
// block. Without bucketing, a last_commit_at-sorted slice with
// `groupBy: top_dir` would emit a fresh `▸ go (N)` header every time
// the date order crossed a top dir, producing repeated misleading
// headers.
//
// Annotation runs over the full scoped set *before* filtering so that
// transient signals like WorktreeCount reflect the real project size
// (all linked worktrees of one project share a CommonGitDir), even when
// only one of those worktrees survives the filter.
//
// Selection is preserved across rebuilds: the selectedPath is looked up
// in the new repos slice; if missing, selection falls back to the nearest
// preceding index, then 0; if zero matches, selected = -1 so renderers
// can show a "no matches" placeholder.
//
// Returns a tea.Cmd to schedule the recent-commits load when the
// rebuild lands selection on a different repo than before — that
// includes the cold-launch case where `m.selectedPath` transitions
// from "" to the first cached repo, which would otherwise leave the
// detail pane at "(loading…)" until the user pressed j/k.
//
// Called whenever any of (cache contents, filterText, sortBy, sortDesc,
// groupBy) change.
func (m *Model) rebuildRepos() tea.Cmd {
	scoped := m.scopedRepos()
	repo.AnnotateDerived(scoped, m.cfg.StaleDays, nowFunc())
	filtered := filterRepos(scoped, m.filterText, m.root)
	// scopedRepos already sorted by configured sort; filterRepos preserves
	// input order — no re-sort needed.
	bucketed := bucketByGroup(filtered, m.groupBy, m.root)
	prevPath := m.selectedPath
	prevIndex := m.selected
	m.repos = bucketed

	if len(bucketed) == 0 {
		m.selected = -1
		m.selectedPath = ""
		m.scrollOffset = 0
		return nil
	}

	// Find prevPath in new slice.
	idx := -1
	if prevPath != "" {
		for i, r := range bucketed {
			if r.Path == prevPath {
				idx = i
				break
			}
		}
	}
	switch {
	case idx >= 0:
		m.selected = idx
	case prevIndex < 0:
		m.selected = 0
	case prevIndex >= len(bucketed):
		m.selected = len(bucketed) - 1
	default:
		m.selected = prevIndex
	}
	m.selectedPath = bucketed[m.selected].Path
	m.scrollOffset = m.scrollIntoView(m.selected, m.scrollOffset)

	if m.selectedPath != prevPath {
		return m.scheduleRecentCommitsLoad()
	}
	return nil
}

// bucketByGroup re-orders rs so repos sharing a group key are contiguous.
// The relative order of repos within each bucket is preserved (so the
// caller's primary sort still applies inside a group). Bucket order is
// the order each key first appears in the input, except for
// groupBy == "activity" — those follow repo.ActivityTierOrder (newest
// activity first) so the user sees a stable, intuitive sequence
// regardless of how the rows are sorted. groupBy == "none" or ""
// returns rs unchanged.
//
// Empty group keys (e.g. a top_dir of "" for a repo at the root level)
// share the same "empty" bucket, but no group header is emitted for that
// bucket at render time — see buildRenderRows.
func bucketByGroup(rs []repo.Repo, groupBy, root string) []repo.Repo {
	if groupBy == "" || groupBy == "none" || len(rs) == 0 {
		return rs
	}
	if groupBy == "worktree" {
		return bucketWorktrees(rs)
	}
	type bucket struct {
		rs []repo.Repo
	}
	seen := make(map[string]int, 8)
	var order []string
	buckets := make(map[string]*bucket, 8)
	for _, r := range rs {
		k := groupKey(r, groupBy, root)
		if _, ok := seen[k]; !ok {
			seen[k] = len(order)
			order = append(order, k)
			buckets[k] = &bucket{}
		}
		buckets[k].rs = append(buckets[k].rs, r)
	}
	if groupBy == "activity" {
		order = sortActivityKeys(order)
	}
	out := make([]repo.Repo, 0, len(rs))
	for _, k := range order {
		out = append(out, buckets[k].rs...)
	}
	return out
}

// worktreeClusterKey identifies the cluster a row belongs to in the
// `worktree` grouping mode. Multi-worktree projects cluster on their
// shared CommonGitDir; everything else (solo repos, or a worktree whose
// siblings are all out of scope) gets a unique key so it stays a
// standalone row. The prefixes keep a CommonGitDir path from ever
// colliding with a repo Path.
func worktreeClusterKey(r repo.Repo) string {
	if r.WorktreeCount > 1 && r.CommonGitDir != "" {
		return "wt:" + r.CommonGitDir
	}
	return "solo:" + r.Path
}

// bucketWorktrees orders rs into the forest the `worktree` mode renders.
// Cluster order follows first appearance in the (already sort-applied)
// input, so the active sort key drives which project comes first — the
// freshest worktree under last_commit_at, or the alphabetically-first
// under repo. Within a multi-worktree cluster the primary checkout is
// pulled to the front (the subtree root); the remaining worktrees keep
// their inherited sorted order. buildWorktreeRows turns this flat slice
// into parent + indented child rows.
func bucketWorktrees(rs []repo.Repo) []repo.Repo {
	seen := make(map[string]int, 8)
	var order []string
	buckets := make(map[string][]repo.Repo, 8)
	for _, r := range rs {
		k := worktreeClusterKey(r)
		if _, ok := seen[k]; !ok {
			seen[k] = len(order)
			order = append(order, k)
		}
		buckets[k] = append(buckets[k], r)
	}
	out := make([]repo.Repo, 0, len(rs))
	for _, k := range order {
		members := buckets[k]
		if len(members) > 1 {
			// Stable partition: primaries first, then the rest, each
			// keeping inherited sorted order.
			primaries := members[:0:0]
			rest := make([]repo.Repo, 0, len(members))
			for _, m := range members {
				if m.PrimaryWorktree {
					primaries = append(primaries, m)
				} else {
					rest = append(rest, m)
				}
			}
			out = append(out, primaries...)
			out = append(out, rest...)
			continue
		}
		out = append(out, members...)
	}
	return out
}

// sortActivityKeys reorders activity-tier keys to match
// repo.ActivityTierOrder. Unknown tiers (defensive — shouldn't occur)
// trail the known ones, alphabetized for determinism.
func sortActivityKeys(keys []string) []string {
	rank := make(map[string]int, len(repo.ActivityTierOrder))
	for i, t := range repo.ActivityTierOrder {
		rank[t] = i
	}
	out := append([]string(nil), keys...)
	sort.SliceStable(out, func(i, j int) bool {
		ri, oki := rank[out[i]]
		rj, okj := rank[out[j]]
		if oki && okj {
			return ri < rj
		}
		if oki != okj {
			return oki
		}
		return out[i] < out[j]
	})
	return out
}

// statusBarSeparator is the visual divider between status bar parts. A
// thin vertical so it doesn't compete with the underlying background.
const statusBarSeparator = " │ "

// statusBarPadding is the horizontal padding applied by m.styles.statusBar
// (Padding(0, 1) → 1 cell on each side). packStatusBar subtracts this from
// the available width so wrapped lines don't overflow into the gutter.
const statusBarPadding = 2

func (m Model) statusBar() string {
	return packStatusBar(m.statusBarParts(), m.width)
}

// statusBarParts returns the ordered list of segments rendered into the
// top status bar. Group by what's invariant (identity, counts) and what
// reflects current view state (sort, group, filter, warnings). The packer
// joins them with " │ " — see packStatusBar.
func (m Model) statusBarParts() []string {
	// Aggregate over the *scoped* (pre-filter) set so signal counts
	// don't shrink keystroke-by-keystroke while typing into the
	// filter. Otherwise parts drop out (e.g. "5 dirty" → nothing)
	// and the bar re-wraps, pushing the filter row up. The filter
	// chip already advertises that a filter is active; the visible
	// count is implicit in the table itself.
	//
	// Stale (and any other transient signal) is only populated by
	// AnnotateDerived, so the scoped slice from scopedRepos must be
	// annotated before counting — rebuildRepos annotates its own
	// local slice, not anything we can reuse here.
	scoped := m.scopedRepos()
	repo.AnnotateDerived(scoped, m.cfg.StaleDays, nowFunc())
	var dirty, ahead, behind, stash, stale, lagging int
	for _, r := range scoped {
		if r.Dirty {
			dirty++
		}
		if r.AheadOrigin > 0 {
			ahead++
		}
		if r.BehindOrigin > 0 {
			behind++
		}
		if r.StashCount > 0 {
			stash++
		}
		if r.Stale {
			stale++
		}
		if r.LaggingWorktree {
			lagging++
		}
	}
	parts := []string{
		"atlas",
		"root: " + config.ContractHome(m.root),
		fmt.Sprintf("%d repos", len(scoped)),
	}
	if dirty > 0 {
		parts = append(parts, fmt.Sprintf("%d dirty", dirty))
	}
	if ahead > 0 {
		parts = append(parts, fmt.Sprintf("%d ahead", ahead))
	}
	if behind > 0 {
		parts = append(parts, fmt.Sprintf("%d behind", behind))
	}
	if stash > 0 {
		parts = append(parts, fmt.Sprintf("%d stash", stash))
	}
	if stale > 0 {
		parts = append(parts, fmt.Sprintf("%d stale", stale))
	}
	if lagging > 0 {
		parts = append(parts, fmt.Sprintf("%d lagging", lagging))
	}
	parts = append(parts, fmt.Sprintf("sort: %s %s", m.sortBy, sortArrow(m.sortDesc)))
	// Filter state is shown by the substituted first row of the
	// status bar (see renderStatusBar) — don't mirror it here.
	// Mirroring would either duplicate the display or, worse, grow
	// with every keystroke and re-introduce wrap-driven jitter.
	if len(m.warnings) > 0 {
		parts = append(parts, fmt.Sprintf("! %d config warnings", len(m.warnings)))
	}
	if m.refreshing {
		parts = append(parts, fmt.Sprintf("[refreshing %d/%d]", m.refreshDoneCount, m.refreshTotal))
	}
	if m.statusMsg != "" {
		if m.statusIsErr {
			parts = append(parts, m.styles.statusMessage.Render(m.statusMsg))
		} else {
			parts = append(parts, m.statusMsg)
		}
	}
	return parts
}

// packStatusBar joins parts with " │ " separators, wrapping to multiple
// lines when the single-line form would exceed width. Wrapping is greedy
// (fill the current line until the next part wouldn't fit, then start a
// new line). A part wider than the available width is emitted on its own
// line — better an overlong row than a dropped signal. width <= 0 falls
// back to single-line (early-init / pre-WindowSizeMsg path).
func packStatusBar(parts []string, width int) string {
	if len(parts) == 0 {
		return ""
	}
	joined := strings.Join(parts, statusBarSeparator)
	avail := width - statusBarPadding
	if width <= 0 || lipgloss.Width(joined) <= avail {
		return joined
	}
	var lines []string
	current := parts[0]
	for _, p := range parts[1:] {
		candidate := current + statusBarSeparator + p
		if lipgloss.Width(candidate) <= avail {
			current = candidate
			continue
		}
		lines = append(lines, current)
		current = p
	}
	lines = append(lines, current)
	return strings.Join(lines, "\n")
}

// statusBarHeight is the number of rendered rows the top status bar will
// occupy at the current width. Used by viewportRows so the table body
// shrinks when the status bar wraps. width <= 0 falls back to 1 line.
//
// Measures the *style-rendered* output, not the packed string: when a
// single part is wider than the available width (long root, long filter
// text, long error message), packStatusBar keeps it on one logical line
// but lipgloss's Width-bounded Render physically wraps it. Counting the
// rendered height keeps viewportRows in sync with what View actually
// emits.
func (m Model) statusBarHeight() int {
	if m.width <= 0 {
		return 1
	}
	rendered := m.styles.statusBar.Width(m.width).Render(packStatusBar(m.statusBarParts(), m.width))
	h := lipgloss.Height(rendered)
	if h < 1 {
		h = 1
	}
	return h
}

func (m Model) hintBar() string {
	pairs := []struct{ key, label string }{
		{"k/j", "nav"},
		{"/", "filter"},
		{"s/S", "sort"},
		{"tab", "group"},
		{"enter", "cd"},
		{"r", "refresh"},
		{"?", "help"},
		{"q", "quit"},
	}
	parts := make([]string, 0, len(pairs))
	for _, p := range pairs {
		parts = append(parts, m.styles.hintKey.Render(p.key)+" "+m.styles.hintLabel.Render(p.label))
	}
	return strings.Join(parts, "  ")
}

