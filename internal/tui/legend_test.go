package tui

import (
	"strings"
	"testing"
)

// The legend's content height is a hard contract: composeRightPane
// uses legendHeight to decide whether the legend fits below the
// detail pane. If renderLegend ever emits a different line count the
// constant must move with it.
func TestRenderLegend_HeightMatchesConstant(t *testing.T) {
	out := renderLegend(newStyles(""))
	lines := strings.Split(out, "\n")
	if len(lines) != legendHeight {
		t.Fatalf("renderLegend produced %d lines; legendHeight = %d", len(lines), legendHeight)
	}
}

// Every glyph that flagString can emit must appear in the legend —
// otherwise a user staring at the table column has no key for the
// symbol they see.
func TestRenderLegend_CoversEveryFlagGlyph(t *testing.T) {
	out := renderLegend(newStyles(""))
	for _, glyph := range []string{"*", "?", "▲", "⊘", "!", "↑", "↓", "≡"} {
		if !strings.Contains(out, glyph) {
			t.Errorf("renderLegend missing glyph %q; got:\n%s", glyph, out)
		}
	}
}

// Both consumers (docked pane via renderLegend, ? help via
// legendEntries) must reflect the same data rows so the wording
// can't drift between surfaces.
func TestLegendEntries_SharedWithRenderLegend(t *testing.T) {
	entries := legendEntries()
	out := renderLegend(newStyles(""))
	for _, e := range entries {
		if !strings.Contains(out, e) {
			t.Errorf("renderLegend does not contain entry %q; got:\n%s", e, out)
		}
	}
}
