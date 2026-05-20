package tui

import (
	"fmt"
	"strings"

	"github.com/sethdeckard/atlas/internal/config"
	"github.com/sethdeckard/atlas/internal/repo"
	"github.com/sethdeckard/atlas/internal/termsafe"

	"github.com/charmbracelet/lipgloss"
)

// renderDetail produces the M5 right-pane content: a "Highlights" line
// explaining why the repo is interesting, a labeled block of git +
// derived fields, and a short list of recent commit subjects whose
// loading lifecycle is described by `recent`.
//
// `recent.loading` shows "(loading…)"; `recent.loaded` shows either the
// subjects, "(no commits)" for an empty repo, or "(commits unavailable)"
// when an error came back. The default zero value (neither loading nor
// loaded — i.e. never requested) also renders as "(loading…)" so the
// pane doesn't go blank between selection-change and the first tick
// firing.
//
// width is the inner width of the pane; an empty repo (nil) returns a
// placeholder string. Rows for missing values are omitted entirely
// rather than rendering "—" everywhere — clean repos shouldn't be
// noisy.
func renderDetail(r *repo.Repo, recent recentCommitsState, siblings []repo.Repo, width int, s styles) string {
	if r == nil {
		return s.row.Render("(no selection)")
	}
	lineStyle := lipgloss.NewStyle().Width(width)

	var b strings.Builder

	b.WriteString(lineStyle.Render(s.detailHeader.Render(termsafe.Sanitize(r.Name))))
	b.WriteByte('\n')
	b.WriteString(lineStyle.Render(termsafe.Sanitize(config.ContractHome(r.Path))))
	b.WriteByte('\n')

	highlights := repo.Highlights(*r)
	if len(highlights) > 0 {
		b.WriteString(lineStyle.Render("Highlights  " + strings.Join(highlights, " · ")))
		b.WriteByte('\n')
	}
	b.WriteString(lineStyle.Render(strings.Repeat("─", maxInt(8, width))))
	b.WriteByte('\n')

	addRow := func(label, value string) {
		if value == "" {
			return
		}
		b.WriteString(lineStyle.Render(formatDetailRow(label, value)))
		b.WriteByte('\n')
	}

	addRow("Kind", r.Kind.String())
	addRow("Branch", branchWithDivergence(r))
	addRow("Origin", termsafe.Sanitize(r.OriginURL))
	addRow("Default", termsafe.Sanitize(r.DefaultBranch))
	if r.LastCommitAt != nil {
		addRow("Last", r.LastCommitAt.UTC().Format("2006-01-02 15:04"))
	}
	if r.ActivityTier != "" {
		addRow("Activity", fmt.Sprintf("%s (%d commits/30d)", s.activityTier.Render(r.ActivityTier), r.CommitsLast30d))
	}
	if len(r.Languages) > 0 {
		langs := make([]string, len(r.Languages))
		for i, l := range r.Languages {
			langs[i] = termsafe.Sanitize(l)
		}
		addRow("Languages", strings.Join(langs, " "))
	}
	if r.BranchCount > 0 {
		addRow("Branches", fmt.Sprintf("%d", r.BranchCount))
	}
	if r.StashCount > 0 {
		addRow("Stashes", fmt.Sprintf("%d", r.StashCount))
	}
	if r.WorktreeCount > 1 && len(siblings) == 0 {
		// Project spans multiple worktrees but the others are out of
		// the active root — show the count without the roster.
		addRow("Worktrees", fmt.Sprintf("%d linked", r.WorktreeCount))
	}
	addRow("Flags", flagString(*r))

	if len(siblings) > 0 {
		b.WriteByte('\n')
		b.WriteString(lineStyle.Render(s.detailSection.Render(
			fmt.Sprintf("▸ Worktrees (%d)", len(siblings)))))
		b.WriteByte('\n')
		for i, w := range siblings {
			b.WriteString(lineStyle.Render("  " + worktreeRosterLine(w)))
			if i < len(siblings)-1 {
				b.WriteByte('\n')
			}
		}
		b.WriteByte('\n')
	}

	// Recent commits section — three terminal states (loading, loaded
	// with N>=0 commits, loaded with err) plus the not-yet-requested
	// default which we treat as loading so the pane doesn't blank out
	// between selection-change and the first tick firing.
	b.WriteByte('\n')
	b.WriteString(lineStyle.Render(s.detailSection.Render("▸ Recent commits")))
	b.WriteByte('\n')
	switch {
	case recent.err != nil:
		b.WriteString(lineStyle.Render("  (commits unavailable)"))
	case recent.loaded && len(recent.lines) == 0:
		b.WriteString(lineStyle.Render("  (no commits)"))
	case recent.loaded:
		for i, line := range recent.lines {
			b.WriteString(lineStyle.Render("  " + termsafe.Sanitize(line)))
			if i < len(recent.lines)-1 {
				b.WriteByte('\n')
			}
		}
	default:
		// loading or never-requested
		b.WriteString(lineStyle.Render("  (loading…)"))
	}
	return b.String()
}

// worktreeRosterLine renders one entry in the detail pane's Worktrees
// section: the checkout's leaf name, branch, recency, activity tier,
// any ▲/⊘ flags, and a (primary) tag for the project's main checkout.
// This is the "see them linked + old vs new" surface that works in
// every view, not just the worktree grouping mode.
func worktreeRosterLine(w repo.Repo) string {
	parts := []string{termsafe.Sanitize(w.Name)}
	if br := branchOf(w); br != "" {
		parts = append(parts, br)
	}
	parts = append(parts, relativeTime(w.LastCommitAt))
	if w.ActivityTier != "" {
		parts = append(parts, w.ActivityTier)
	}
	line := strings.Join(parts, " · ")
	// ⊘ absorbs ▲ for a worktree with commits — see flagString.
	var tags strings.Builder
	switch {
	case w.LaggingWorktree:
		tags.WriteRune('⊘')
	case w.Stale:
		tags.WriteRune('▲')
	}
	if tags.Len() > 0 {
		line += "  " + tags.String()
	}
	if w.PrimaryWorktree {
		line += "  (primary)"
	}
	return line
}

func formatDetailRow(label, value string) string {
	const labelW = 11
	pad := labelW - len([]rune(label))
	if pad < 1 {
		pad = 1
	}
	return label + strings.Repeat(" ", pad) + value
}

func branchWithDivergence(r *repo.Repo) string {
	if r.DetachedHead {
		return "(" + termsafe.Sanitize(r.HeadSHA) + ")"
	}
	if r.Branch == "" {
		return ""
	}
	branch := termsafe.Sanitize(r.Branch)
	if r.AheadOrigin > 0 || r.BehindOrigin > 0 {
		return fmt.Sprintf("%s (↑%d ↓%d)", branch, max0(r.AheadOrigin), max0(r.BehindOrigin))
	}
	return branch
}

func max0(n int) int {
	if n < 0 {
		return 0
	}
	return n
}

