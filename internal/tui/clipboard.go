package tui

import "github.com/atotto/clipboard"

// Clipboard abstracts atotto/clipboard.WriteAll so tests can swap in a
// fake without touching the host clipboard. The interface is
// intentionally minimal — atlas only needs to write a single string
// (the selected repo's path) when the user presses `c`.
type Clipboard interface {
	Write(text string) error
}

// defaultClipboard is the production implementation backed by
// atotto/clipboard, which delegates to pbcopy/xclip/clip.exe per OS.
type defaultClipboard struct{}

// Write satisfies Clipboard.
func (defaultClipboard) Write(text string) error {
	return clipboard.WriteAll(text)
}
