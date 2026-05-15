// Package termsafe sanitizes untrusted strings before they reach a
// terminal renderer. The threat model is a commit subject, branch
// name, repo directory name, language manifest, or git origin URL
// that contains C0/C1 control characters or DEL — sequences a
// terminal would interpret as cursor movement, OSC 8 hyperlinks, or
// window-title manipulation.
//
// atlas is observability-only and never clones, but users routinely
// clone untrusted repos and accept upstream PRs, so commit subjects
// in particular are fully attacker-controlled. Sanitize at the
// rendering boundary — never persist the sanitized form back into
// Repo, the cache, or JSON output where a programmatic consumer
// wants the raw bytes.
package termsafe

import (
	"strings"
	"unicode/utf8"
)

// Replacement is the rune substituted for every stripped control
// character. U+FFFD makes the substitution visible to the user;
// silent stripping would hide the fact that something weird was in
// the data.
const Replacement = '�'

// Sanitize returns s with C0 controls (0x00-0x1F), DEL (0x7F), and
// C1 controls (0x80-0x9F) replaced by Replacement. Invalid UTF-8
// bytes are replaced individually so a single bad byte does not
// drop a whole grapheme cluster.
//
// Tabs and newlines are stripped along with the rest of the C0
// range because every call site is a single-line renderer: an
// embedded tab breaks tabwriter alignment and an embedded newline
// breaks lipgloss width math. Callers that want to preserve those
// characters should pre-split on them and sanitize per line.
func Sanitize(s string) string {
	if !needs(s) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			b.WriteRune(Replacement)
			i++
			continue
		}
		if isUnsafe(r) {
			b.WriteRune(Replacement)
		} else {
			b.WriteRune(r)
		}
		i += size
	}
	return b.String()
}

func needs(s string) bool {
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			return true
		}
		if isUnsafe(r) {
			return true
		}
		i += size
	}
	return false
}

func isUnsafe(r rune) bool {
	if r < 0x20 || r == 0x7f {
		return true
	}
	if r >= 0x80 && r <= 0x9f {
		return true
	}
	return false
}
