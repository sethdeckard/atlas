// Package tui's styles file defines the lipgloss.Style values used by
// renderers. Two themes ship today, dispatched by name in newStyles:
//
//   - "default": periwinkle accent on dark-navy panels — atlas's
//     unified TUI brand, shared with atria/loadout/solopub. Sourced
//     from ~/projects/go/colorpreview/main.go (themeSolopubAdapted).
//     Update the pw* constants below in lockstep if the brand
//     palette changes there. Composition rule: never paint a
//     body-row background; surface backgrounds appear only on
//     status bar, selected row, and the help overlay panel.
//
//   - "ansi": uses ANSI 16-color indices ("0"-"15") so the terminal's
//     palette governs the look — fits users with curated terminal
//     themes (Solarized, Gruvbox, etc.).
package tui

import "github.com/charmbracelet/lipgloss"

type styles struct {
	header        lipgloss.Style
	row           lipgloss.Style
	selected      lipgloss.Style
	groupHeader   lipgloss.Style
	statusBar     lipgloss.Style
	hintKey       lipgloss.Style
	hintLabel     lipgloss.Style
	// filterBarActive is the status bar's first-line style when
	// filter mode is open. Distinct from the regular statusBar
	// background so the user can see at a glance that keystrokes
	// are now being captured as a search query.
	filterBarActive lipgloss.Style
	statusMessage   lipgloss.Style
	detailPane    lipgloss.Style
	detailHeader  lipgloss.Style
	detailSection lipgloss.Style
	activityTier  lipgloss.Style
	helpOverlay   lipgloss.Style
}

// Default theme palette (periwinkle on dark navy) — sourced from
// ~/projects/go/colorpreview/main.go (themeSolopubAdapted, dark
// variant). Update both in sync if the brand palette changes there.
// Only currently-consumed tokens are declared; surfaceSubtle,
// textMuted, textFaint, accentStrong, success, info, and special
// are reserved in the brand spec but have no atlas consumer yet.
const (
	pwSurface       lipgloss.Color = "#1A1A2E" // dark navy panel
	pwSurfaceRaised lipgloss.Color = "#2a2a4a" // overlay/modal
	pwBorder        lipgloss.Color = "#3a3a5c" // muted slate
	pwBorderActive  lipgloss.Color = "#7f9cf5" // periwinkle
	pwText          lipgloss.Color = "#d8dce8" // light periwinkle-cream
	pwAccent        lipgloss.Color = "#7f9cf5" // periwinkle (brand primary)
	pwSelectionBg   lipgloss.Color = "#7f9cf5" // periwinkle (same as accent)
	pwSelectionText lipgloss.Color = "#1A1A2E" // dark navy (matches surface)
	pwWarning       lipgloss.Color = "#d8a040" // muted gold
	pwError         lipgloss.Color = "#d85060" // muted red
)

// newStyles dispatches to the named theme constructor. Unknown names
// fall through to the default theme; callers should normalize via
// config.NormalizeTheme before reaching here, but this is a safety
// net.
func newStyles(name string) styles {
	switch name {
	case "ansi":
		return themeANSI()
	default:
		return themeDefault()
	}
}

func themeDefault() styles {
	return styles{
		header: lipgloss.NewStyle().
			Bold(true).
			Underline(true),
		row: lipgloss.NewStyle(),
		selected: lipgloss.NewStyle().
			Foreground(pwSelectionText).
			Background(pwSelectionBg).
			Bold(true),
		groupHeader: lipgloss.NewStyle().
			Bold(true).
			Foreground(pwAccent),
		statusBar: lipgloss.NewStyle().
			Background(pwSurface).
			Foreground(pwText).
			Padding(0, 1),
		hintKey: lipgloss.NewStyle().
			Bold(true),
		hintLabel: lipgloss.NewStyle().
			Faint(true),
		filterBarActive: lipgloss.NewStyle().
			Background(pwWarning).
			Foreground(pwSelectionText).
			Bold(true).
			Padding(0, 1),
		statusMessage: lipgloss.NewStyle().
			Foreground(pwError),
		detailPane: lipgloss.NewStyle().
			BorderStyle(lipgloss.NormalBorder()).
			BorderForeground(pwBorder).
			BorderLeft(true).
			PaddingLeft(1),
		detailHeader: lipgloss.NewStyle().
			Bold(true),
		detailSection: lipgloss.NewStyle().
			Bold(true).
			Foreground(pwAccent),
		activityTier: lipgloss.NewStyle().
			Foreground(pwAccent),
		helpOverlay: lipgloss.NewStyle().
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(pwBorderActive).
			Background(pwSurfaceRaised).
			Foreground(pwText).
			Padding(1, 2),
	}
}

// themeANSI uses ANSI 16-color indices so the terminal palette
// governs rendering. Two contrast fixes vs. earlier iterations:
//
//   - statusBar uses fg "0" / bg "15" — black on bright-white. ANSI
//     0 and 15 sit at opposite ends of any palette by convention,
//     so this pair stays readable regardless of the user's profile.
//     Earlier attempts (fg "15" / bg "8", and `Reverse(true)`) both
//     failed on terminal profiles where the involved indices landed
//     in the same hue family.
//   - selected drops Bold(true) so fg "0" stays the terminal's
//     actual black. Bold + an explicit fg promotes the color to its
//     bright variant on most terminals, which on warm-toned profiles
//     gave a washed-out cyan-on-cyan look.
func themeANSI() styles {
	return styles{
		header: lipgloss.NewStyle().
			Bold(true).
			Underline(true),
		row: lipgloss.NewStyle(),
		selected: lipgloss.NewStyle().
			Foreground(lipgloss.Color("0")).
			Background(lipgloss.Color("6")),
		groupHeader: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("6")),
		statusBar: lipgloss.NewStyle().
			Foreground(lipgloss.Color("0")).
			Background(lipgloss.Color("15")).
			Padding(0, 1),
		hintKey: lipgloss.NewStyle().
			Bold(true),
		hintLabel: lipgloss.NewStyle().
			Faint(true),
		filterBarActive: lipgloss.NewStyle().
			Background(lipgloss.Color("3")).
			Foreground(lipgloss.Color("0")).
			Bold(true).
			Padding(0, 1),
		statusMessage: lipgloss.NewStyle().
			Foreground(lipgloss.Color("1")),
		detailPane: lipgloss.NewStyle().
			BorderStyle(lipgloss.NormalBorder()).
			BorderForeground(lipgloss.Color("8")).
			BorderLeft(true).
			PaddingLeft(1),
		detailHeader: lipgloss.NewStyle().
			Bold(true),
		detailSection: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("6")),
		activityTier: lipgloss.NewStyle().
			Foreground(lipgloss.Color("6")),
		helpOverlay: lipgloss.NewStyle().
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("6")).
			Padding(1, 2),
	}
}
