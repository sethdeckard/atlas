package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/sethdeckard/atlas/internal/repo"
	"github.com/sethdeckard/atlas/internal/termsafe"
)

// nowFunc is overridable for deterministic relative-time formatting in
// tests (mirrors the cli package).
var nowFunc func() time.Time = time.Now

// SetNowFunc replaces the package's clock for relative-time rendering.
// Production code should not call it.
func SetNowFunc(f func() time.Time) {
	if f == nil {
		nowFunc = time.Now
		return
	}
	nowFunc = f
}

// renderTable produces the body of the TUI: header + a window of rows
// fitted to the available width and viewport height. scrollOffset is the
// index of the first *render row* to draw; render rows include any group
// headers when groupBy != "none". selected is the index of the highlighted
// repo within `repos`; rows outside the visible window are simply skipped.
func renderTable(repos []repo.Repo, root, groupBy string, selected, scrollOffset, viewportRows, width int, s styles) string {
	if len(repos) == 0 {
		return s.row.Render("(no repositories)")
	}
	cols := chooseColumns(width, repos, root, groupBy)
	pathCol := pathColumnIndex(cols)

	rows := buildRenderRows(repos, root, groupBy)
	if scrollOffset < 0 {
		scrollOffset = 0
	}
	if scrollOffset > len(rows) {
		scrollOffset = len(rows)
	}
	end := scrollOffset + viewportRows
	if end > len(rows) {
		end = len(rows)
	}
	var b strings.Builder
	b.WriteString(s.header.Render(formatRow(cols, headerCells(cols))))
	b.WriteByte('\n')
	for i := scrollOffset; i < end; i++ {
		row := rows[i]
		var line string
		if row.kind == rowGroup {
			line = s.groupHeader.Render(formatGroupHeader(row.label, row.count, columnsWidth(cols)))
		} else {
			cells := rowCells(cols, row.repo, root)
			if groupBy == "worktree" && pathCol >= 0 {
				cells[pathCol] = worktreeConnector(row) + cells[pathCol]
			}
			if groupBy == "worktree" && row.depth == 0 &&
				row.repo.PrimaryWorktree && row.repo.WorktreeHasLaggingChild &&
				!row.repo.LaggingWorktree {
				// Roll a forgotten child up onto the anchor so it's
				// visible even when the child is scrolled off. Skip
				// when the primary itself already lags — flagString
				// has put ⊘ on this row, and appending again would
				// double-mark the same fact.
				cells[len(cells)-1] += " ⊘"
			}
			rendered := formatRow(cols, cells)
			switch {
			case row.repoIdx == selected:
				line = s.selected.Render(rendered)
			case groupBy == "worktree" && row.depth > 0 &&
				(row.repo.LaggingWorktree || row.repo.Stale):
				line = s.laggingRow.Render(rendered)
			default:
				line = s.row.Render(rendered)
			}
		}
		b.WriteString(line)
		if i < end-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// rowEntry is the tagged union for the rendered table: either a repo row
// (with a back-pointer to its index in the source slice for selection
// highlighting) or a group header.
type rowEntry struct {
	kind    rowKind
	repo    repo.Repo
	repoIdx int    // index into the source []repo.Repo when kind == rowRepo
	label   string // group label when kind == rowGroup
	count   int    // group repo count when kind == rowGroup

	// Tree decoration, set only in the `worktree` grouping mode.
	// depth 0 = a solo repo or a project's primary checkout (the
	// subtree root); depth 1 = a linked worktree nested under it.
	// lastChild marks the final child of a subtree so the renderer
	// can draw └─ instead of ├─.
	depth     int
	lastChild bool
}

type rowKind int

const (
	rowRepo rowKind = iota
	rowGroup
)

// buildRenderRows walks `repos` (already filtered + sorted) and produces
// the flat list of render rows the table draws. Group headers are inserted
// when groupBy != "none" AND the group label is non-empty (an empty group
// label means "no top dir" and we render those repos directly with no
// section header — keeps the layout uncluttered for repos that live at
// root level).
func buildRenderRows(repos []repo.Repo, root, groupBy string) []rowEntry {
	if groupBy == "" || groupBy == "none" {
		out := make([]rowEntry, len(repos))
		for i, r := range repos {
			out[i] = rowEntry{kind: rowRepo, repo: r, repoIdx: i}
		}
		return out
	}

	if groupBy == "worktree" {
		return buildWorktreeRows(repos, root)
	}

	// Pre-pass: collect group keys and counts in input order.
	groupCounts := make(map[string]int, 8)
	groupOrder := make([]string, 0, 8)
	keys := make([]string, len(repos))
	for i, r := range repos {
		k := groupKey(r, groupBy, root)
		keys[i] = k
		if _, seen := groupCounts[k]; !seen {
			groupOrder = append(groupOrder, k)
		}
		groupCounts[k]++
	}

	out := make([]rowEntry, 0, len(repos)+len(groupOrder))
	var prev string
	for i, r := range repos {
		k := keys[i]
		// First repo in a non-empty group → emit a header.
		if k != "" && k != prev {
			out = append(out, rowEntry{kind: rowGroup, label: k, count: groupCounts[k]})
		}
		out = append(out, rowEntry{kind: rowRepo, repo: r, repoIdx: i})
		prev = k
	}
	return out
}

// buildWorktreeRows turns the worktree-bucketed slice (see
// bucketWorktrees: clusters contiguous, primary first within each) into
// the forest the table draws. Each multi-worktree project is a subtree:
//
//   - When the primary checkout is in scope it is the subtree root —
//     a depth-0 repo row, no synthetic header — and its linked
//     worktrees follow as depth-1 child rows.
//   - When no primary is in scope (primary outside the active root or a
//     bare-backed project), there's no anchor row, so the cluster gets
//     a synthetic `▸ <ProjectLabel> (N)` header and every worktree
//     renders as a depth-1 child beneath it.
//   - A cluster with only one worktree visible (siblings filtered or
//     out of scope) has nothing to nest, so it renders as a plain
//     depth-0 row.
//
// Solo (single-worktree) repos always render as plain depth-0 rows.
func buildWorktreeRows(repos []repo.Repo, root string) []rowEntry {
	out := make([]rowEntry, 0, len(repos)+4)
	for i := 0; i < len(repos); {
		key := worktreeClusterKey(repos[i])
		j := i
		for j < len(repos) && worktreeClusterKey(repos[j]) == key {
			j++
		}
		members := repos[i:j]

		if len(members) == 1 {
			out = append(out, rowEntry{kind: rowRepo, repo: members[0], repoIdx: i})
			i = j
			continue
		}

		// bucketWorktrees pulled any primary to members[0].
		if members[0].PrimaryWorktree {
			out = append(out, rowEntry{kind: rowRepo, repo: members[0], repoIdx: i})
			for off := 1; off < len(members); off++ {
				out = append(out, rowEntry{
					kind:      rowRepo,
					repo:      members[off],
					repoIdx:   i + off,
					depth:     1,
					lastChild: off == len(members)-1,
				})
			}
		} else {
			out = append(out, rowEntry{
				kind:  rowGroup,
				label: repo.ProjectLabel(root, members[0]),
				count: len(members),
			})
			for off := 0; off < len(members); off++ {
				out = append(out, rowEntry{
					kind:      rowRepo,
					repo:      members[off],
					repoIdx:   i + off,
					depth:     1,
					lastChild: off == len(members)-1,
				})
			}
		}
		i = j
	}
	return out
}

// groupKey returns the string key used to group `r` for the given groupBy
// mode. Returns "" when the repo has no value for the chosen dimension
// (e.g. a repo at root level under top_dir, or a repo with no detected
// languages); empty-key groups render without a header.
func groupKey(r repo.Repo, groupBy, root string) string {
	switch groupBy {
	case "top_dir":
		return repo.TopDir(root, r.Path)
	case "activity":
		return r.ActivityTier
	case "language":
		if len(r.Languages) == 0 {
			return ""
		}
		return r.Languages[0] // primary language wins for grouping
	case "worktree":
		return worktreeClusterKey(r)
	default:
		return ""
	}
}

func formatGroupHeader(label string, count, width int) string {
	header := fmt.Sprintf("▸ %s (%d)", label, count)
	if width > 0 && runeLen(header) > width {
		// best-effort truncation
		return header[:width]
	}
	return header
}

// worktreeConnector returns the tree-branch prefix for a child row in
// the `worktree` grouping mode: ├─ , or └─ when it's the last sibling.
// Parent/solo rows render flush-left (empty prefix); column alignment
// for the rows that follow is handled by the widened path column
// (chooseColumns adds worktreeIndent), not by left-padding the parent.
func worktreeConnector(row rowEntry) string {
	if row.depth == 0 {
		return ""
	}
	if row.lastChild {
		return "└─ "
	}
	return "├─ "
}

// pathColumnIndex returns the index of the "path" column, or -1 when it
// was dropped (very narrow terminals never drop "path", but be safe).
func pathColumnIndex(cols []column) int {
	for i, c := range cols {
		if c.key == "path" {
			return i
		}
	}
	return -1
}

func columnsWidth(cols []column) int {
	if len(cols) == 0 {
		return 0
	}
	total := (len(cols) - 1) * 2
	for _, c := range cols {
		total += c.width
	}
	return total
}

// renderRowsForRepos returns how many render rows would be generated for
// the given repo set under the active groupBy. Used by callers (Model.View
// / scroll math) to size the viewport without re-running the renderer.
func renderRowsForRepos(repos []repo.Repo, root, groupBy string) int {
	return len(buildRenderRows(repos, root, groupBy))
}

// renderRowOfRepo returns the index of the given repo's row in the
// rendered output. Returns -1 if repoIdx is out of range. Used to keep the
// scroll offset in sync with selection across header insertions.
func renderRowOfRepo(repos []repo.Repo, root, groupBy string, repoIdx int) int {
	if repoIdx < 0 || repoIdx >= len(repos) {
		return -1
	}
	rows := buildRenderRows(repos, root, groupBy)
	for i, row := range rows {
		if row.kind == rowRepo && row.repoIdx == repoIdx {
			return i
		}
	}
	return -1
}

type column struct {
	key   string
	title string
	width int
}

// worktreeIndent is the fixed column budget reserved for a child
// worktree's tree connector ("├─ " / "└─ ", both 3 runes) in the
// `worktree` grouping mode. Parent/solo rows render flush-left; the
// path column is widened by this so indented children still align the
// columns that follow.
const worktreeIndent = 3

func chooseColumns(width int, repos []repo.Repo, root, groupBy string) []column {
	// Compute natural widths (clamped). Drop columns gracefully on narrow
	// terminals: keep repo + last_commit + flags at minimum.
	pathW := 4    // "repo"
	branchW := 6  // "branch"
	commitW := 11 // "last_commit"
	flagsW := 5   // "flags"
	for _, r := range repos {
		pathW = maxInt(pathW, runeLen(displayPath(root, r)))
		branchW = maxInt(branchW, runeLen(branchOf(r)))
		commitW = maxInt(commitW, runeLen(relativeTime(r.LastCommitAt)))
		flagsW = maxInt(flagsW, runeLen(flagString(r)))
	}
	if groupBy == "worktree" {
		pathW += worktreeIndent
	}
	gap := 2
	full := pathW + branchW + commitW + flagsW + 3*gap
	cols := []column{
		{key: "path", title: "repo", width: pathW},
		{key: "branch", title: "branch", width: branchW},
		{key: "last", title: "last_commit", width: commitW},
		{key: "flags", title: "flags", width: flagsW},
	}
	if width <= 0 || full <= width {
		return cols
	}
	// Drop branch on narrow terminals (repo already encodes top_dir/name).
	cols = removeColumn(cols, "branch")
	return cols
}

func removeColumn(cols []column, key string) []column {
	out := cols[:0]
	for _, c := range cols {
		if c.key != key {
			out = append(out, c)
		}
	}
	// Re-slice into a fresh slice so callers can safely keep the original.
	return append([]column(nil), out...)
}

func headerCells(cols []column) []string {
	out := make([]string, len(cols))
	for i, c := range cols {
		out[i] = c.title
	}
	return out
}

func rowCells(cols []column, r repo.Repo, root string) []string {
	out := make([]string, len(cols))
	for i, c := range cols {
		switch c.key {
		case "path":
			out[i] = displayPath(root, r)
		case "branch":
			out[i] = branchOf(r)
		case "last":
			out[i] = relativeTime(r.LastCommitAt)
		case "flags":
			out[i] = flagString(r)
		}
	}
	return out
}

// displayPath returns the repo's display path with terminal control
// characters stripped. The sanitization rune-count matches the
// original so width math stays consistent with what gets rendered.
func displayPath(root string, r repo.Repo) string {
	return termsafe.Sanitize(repo.DisplayPath(root, r))
}

func formatRow(cols []column, cells []string) string {
	parts := make([]string, len(cols))
	for i, c := range cols {
		parts[i] = padRight(cells[i], c.width)
	}
	return strings.Join(parts, "  ")
}

func padRight(s string, w int) string {
	n := runeLen(s)
	if n >= w {
		return s
	}
	return s + strings.Repeat(" ", w-n)
}

func branchOf(r repo.Repo) string {
	if r.DetachedHead {
		return "(" + termsafe.Sanitize(r.HeadSHA) + ")"
	}
	if r.Branch == "" {
		return "—"
	}
	return termsafe.Sanitize(r.Branch)
}

func flagString(r repo.Repo) string {
	var b strings.Builder
	if r.Dirty {
		if r.UntrackedOnly {
			b.WriteRune('?')
		} else {
			b.WriteRune('*')
		}
	}
	// ⊘ absorbs the ▲ meaning: a worktree with commits can only lag
	// by also being absolutely stale (the math is unidirectional), so
	// rendering both glyphs would double-mark the same row. ⊘ is the
	// stronger signal — "this is the forgotten one" — so suppress ▲
	// when ⊘ fires. ▲ alone keeps its current meaning: "old, generic
	// — no standout worktree."
	switch {
	case r.LaggingWorktree:
		b.WriteRune('⊘')
	case r.Stale:
		b.WriteRune('▲')
	}
	if r.Err != "" {
		b.WriteRune('!')
	}
	if r.AheadOrigin > 0 {
		fmt.Fprintf(&b, "↑%d", r.AheadOrigin)
	}
	if r.BehindOrigin > 0 {
		fmt.Fprintf(&b, "↓%d", r.BehindOrigin)
	}
	if r.StashCount > 0 {
		fmt.Fprintf(&b, "≡%d", r.StashCount)
	}
	return b.String()
}

func relativeTime(t *time.Time) string {
	if t == nil {
		return "—"
	}
	now := nowFunc()
	d := now.Sub(*t)
	if d < 0 {
		return "future"
	}
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	case d < 365*24*time.Hour:
		return fmt.Sprintf("%dmo ago", int(d.Hours()/(24*30)))
	default:
		return fmt.Sprintf("%dy ago", int(d.Hours()/(24*365)))
	}
}

func runeLen(s string) int {
	return len([]rune(s))
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
