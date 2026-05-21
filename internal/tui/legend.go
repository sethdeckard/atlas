package tui

import (
	"fmt"
	"strings"
)

// legendHeight is the rendered line count of renderLegend's output:
// the styled "▸ Flags" header plus four glyph/label rows. Constant
// because the View body composition needs it up-front to decide
// whether the legend fits below the detail pane without colliding
// with it.
const legendHeight = 5

// legendEntries returns the four glyph/label rows that explain the
// flags column. No header, no styling — both the docked right-column
// legend and the ? help overlay consume these lines and prepend their
// own header (a styled "▸ Flags" line in the pane, a plain "Flags:"
// label in the help box). Keeping the entry text in one place avoids
// drift between the two surfaces.
//
// Glyph vocabulary mirrors flagString in table.go:
//   *  dirty       ↑N ahead
//   ?  untracked   ↓N behind
//   ▲  stale       ≡N stashed
//   ⊘  lagging     !  error
func legendEntries() []string {
	row := func(lg, ll, rg, rl string) string {
		return fmt.Sprintf("%-3s%-14s%-3s%s", lg, ll, rg, rl)
	}
	return []string{
		row("*", "dirty", "↑N", "ahead"),
		row("?", "untracked", "↓N", "behind"),
		row("▲", "stale", "≡N", "stashed"),
		row("⊘", "lagging", "!", "error"),
	}
}

// renderLegend returns the docked-right-column legend: a styled
// "▸ Flags" section header followed by the four glyph/label rows.
// Matches detail.go's "▸ Recent commits" / "▸ Worktrees (N)" header
// styling so the legend reads as another detail-pane section, just
// bottom-anchored instead of inline.
func renderLegend(s styles) string {
	lines := make([]string, 0, legendHeight)
	lines = append(lines, s.detailSection.Render("▸ Flags"))
	lines = append(lines, legendEntries()...)
	return strings.Join(lines, "\n")
}
