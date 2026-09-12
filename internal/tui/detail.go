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
	return renderDetailWithinHeight(r, recent, siblings, width, 0, s)
}

// renderDetailWithinHeight renders the detail pane with an optional height
// budget. It budgets worktree-roster rows against maxHeight after accounting
// for fixed detail and recent commits. It does not truncate fixed content;
// callers apply any final height cap. A zero maxHeight leaves the roster
// unbounded, which is useful to callers that render the detail content on its
// own.
func renderDetailWithinHeight(r *repo.Repo, recent recentCommitsState, siblings []repo.Repo, width, maxHeight int, s styles) string {
	if r == nil {
		return s.row.Render("(no selection)")
	}
	lineStyle := lipgloss.NewStyle().Width(width)
	renderLine := lineStyle.Render

	lines := []string{
		renderLine(s.detailHeader.Render(termsafe.Sanitize(r.Name))),
		renderLine(termsafe.Sanitize(config.ContractHome(r.Path))),
	}

	highlights := repo.Highlights(*r)
	if len(highlights) > 0 {
		lines = append(lines, renderLine("Highlights  "+strings.Join(highlights, " · ")))
	}
	lines = append(lines, renderLine(strings.Repeat("─", maxInt(8, width))))

	addRow := func(label, value string) {
		if value == "" {
			return
		}
		lines = append(lines, renderLine(formatDetailRow(label, value)))
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

	recentLines := []string{renderLine(s.detailSection.Render("▸ Recent commits"))}
	switch {
	case recent.err != nil:
		recentLines = append(recentLines, renderLine("  (commits unavailable)"))
	case recent.loaded && len(recent.lines) == 0:
		recentLines = append(recentLines, renderLine("  (no commits)"))
	case recent.loaded:
		for _, line := range recent.lines {
			recentLines = append(recentLines, renderLine("  "+termsafe.Sanitize(line)))
		}
	default:
		// loading or never-requested
		recentLines = append(recentLines, renderLine("  (loading…)"))
	}

	if len(siblings) > 0 {
		lines = append(lines, "", renderLine(s.detailSection.Render(
			fmt.Sprintf("▸ Worktrees (%d)", len(siblings)))))

		rosterLines := make([]string, len(siblings))
		for i, w := range siblings {
			rosterLines[i] = renderLine("  " + worktreeRosterLine(w))
		}
		visible := len(siblings)
		usedRosterRows := renderedLinesHeight(rosterLines)
		rosterRows := usedRosterRows
		if maxHeight > 0 {
			// Besides the roster entries, reserve one row for the blank
			// separator before Recent commits. Measure physical rows rather
			// than logical entries because width-constrained detail lines can
			// wrap. If the roster must shrink, reserve room for an omission
			// line as well.
			rosterRows = maxHeight - renderedLinesHeight(lines) - 1 - renderedLinesHeight(recentLines)
			if usedRosterRows > rosterRows {
				visible = 0
				usedRosterRows = 0
				for visible < len(rosterLines) {
					nextRows := usedRosterRows + lipgloss.Height(rosterLines[visible])
					remaining := len(rosterLines) - visible - 1
					if remaining > 0 {
						nextRows += lipgloss.Height(renderLine(worktreeOmissionLine(remaining)))
					}
					if nextRows > rosterRows {
						break
					}
					usedRosterRows += lipgloss.Height(rosterLines[visible])
					visible++
				}
			}
		}
		lines = append(lines, rosterLines[:visible]...)
		if omitted := len(siblings) - visible; omitted > 0 {
			omittedLine := renderLine(worktreeOmissionLine(omitted))
			if maxHeight <= 0 || usedRosterRows+lipgloss.Height(omittedLine) <= rosterRows {
				lines = append(lines, omittedLine)
			}
		}
	}

	// Recent commits section — three terminal states (loading, loaded
	// with N>=0 commits, loaded with err) plus the not-yet-requested
	// default which we treat as loading so the pane doesn't blank out
	// between selection-change and the first tick firing.
	lines = append(lines, "")
	lines = append(lines, recentLines...)
	return strings.Join(lines, "\n")
}

// renderedLinesHeight returns the physical terminal rows occupied when the
// already-rendered lines are joined. Individual entries can themselves
// contain newlines after Lip Gloss applies a width constraint.
func renderedLinesHeight(lines []string) int {
	if len(lines) == 0 {
		return 0
	}
	return lipgloss.Height(strings.Join(lines, "\n"))
}

func worktreeOmissionLine(n int) string {
	noun := "worktrees"
	if n == 1 {
		noun = "worktree"
	}
	return fmt.Sprintf("  … %d more %s", n, noun)
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
