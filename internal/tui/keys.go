package tui

import "github.com/charmbracelet/bubbles/key"

// keyMap is the canonical TUI binding set. Each entry's primary key is
// vim-flavored where applicable; arrow keys and home/end are kept as
// invisible aliases (bound but not surfaced in Help text) so muscle
// memory works either way. The README documents both sets.
type keyMap struct {
	// Navigation
	Up         key.Binding
	Down       key.Binding
	JumpTop    key.Binding
	JumpBottom key.Binding
	HalfUp     key.Binding
	HalfDown   key.Binding

	// Action
	Enter   key.Binding
	Refresh key.Binding
	Help    key.Binding
	Quit    key.Binding

	// M3: filter / sort / group
	Filter      key.Binding
	SortCycle   key.Binding
	SortReverse key.Binding
	GroupCycle  key.Binding

	// M5: detail-pane affordances.
	CopyPath   key.Binding
	OpenOrigin key.Binding

	// M3: filter-mode-only — handled inline, not via key.Matches.
	// Listed as fields so the help overlay can document them.
	FilterAccept key.Binding
	FilterCancel key.Binding
}

func newKeyMap() keyMap {
	return keyMap{
		Up: key.NewBinding(
			key.WithKeys("k", "up"),
			key.WithHelp("k", "up"),
		),
		Down: key.NewBinding(
			key.WithKeys("j", "down"),
			key.WithHelp("j", "down"),
		),
		JumpTop: key.NewBinding(
			key.WithKeys("g", "home"),
			key.WithHelp("g", "first repo"),
		),
		JumpBottom: key.NewBinding(
			key.WithKeys("G", "end"),
			key.WithHelp("G", "last repo"),
		),
		HalfUp: key.NewBinding(
			key.WithKeys("ctrl+u"),
			key.WithHelp("ctrl+u", "half-page up"),
		),
		HalfDown: key.NewBinding(
			key.WithKeys("ctrl+d"),
			key.WithHelp("ctrl+d", "half-page down"),
		),
		Enter: key.NewBinding(
			key.WithKeys("enter"),
			key.WithHelp("enter", "cd into repo & exit"),
		),
		Refresh: key.NewBinding(
			key.WithKeys("r"),
			key.WithHelp("r", "refresh all"),
		),
		Help: key.NewBinding(
			key.WithKeys("?"),
			key.WithHelp("?", "help"),
		),
		Quit: key.NewBinding(
			key.WithKeys("q", "ctrl+c"),
			key.WithHelp("q", "quit"),
		),
		Filter: key.NewBinding(
			key.WithKeys("/"),
			key.WithHelp("/", "filter"),
		),
		SortCycle: key.NewBinding(
			key.WithKeys("s"),
			key.WithHelp("s", "cycle sort"),
		),
		SortReverse: key.NewBinding(
			key.WithKeys("S"),
			key.WithHelp("S", "reverse sort"),
		),
		GroupCycle: key.NewBinding(
			key.WithKeys("tab"),
			key.WithHelp("tab", "cycle grouping"),
		),
		CopyPath: key.NewBinding(
			key.WithKeys("c"),
			key.WithHelp("c", "copy path"),
		),
		OpenOrigin: key.NewBinding(
			key.WithKeys("o"),
			key.WithHelp("o", "open origin"),
		),
		FilterAccept: key.NewBinding(
			key.WithKeys("enter"),
			key.WithHelp("enter", "accept filter"),
		),
		FilterCancel: key.NewBinding(
			key.WithKeys("esc"),
			key.WithHelp("esc", "clear filter"),
		),
	}
}
