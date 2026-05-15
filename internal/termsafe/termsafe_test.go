package termsafe_test

import (
	"strings"
	"testing"

	"github.com/sethdeckard/atlas/internal/termsafe"
)

func TestSanitize(t *testing.T) {
	repl := string(termsafe.Replacement)
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"plain ascii", "hello", "hello"},
		{"utf8 multibyte", "héllo世界", "héllo世界"},
		{"strips ESC", "a\x1bb", "a" + repl + "b"},
		{"strips BEL", "a\x07b", "a" + repl + "b"},
		{"strips OSC 8 hyperlink", "a\x1b]8;;http://evil/\x1b\\spoof\x1b]8;;\x1b\\b", "a" + repl + "]8;;http://evil/" + repl + "\\spoof" + repl + "]8;;" + repl + "\\b"},
		{"strips newline", "line1\nline2", "line1" + repl + "line2"},
		{"strips tab", "col1\tcol2", "col1" + repl + "col2"},
		{"strips DEL", "x\x7fy", "x" + repl + "y"},
		{"strips C1 NEL (U+0085)", "xy", "x" + repl + "y"},
		{"strips C1 IND (U+0084)", "xy", "x" + repl + "y"},
		{"strips raw 0x80 byte", "x\x80y", "x" + repl + "y"},
		{"preserves space and printable punctuation", "a b !@#$%", "a b !@#$%"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := termsafe.Sanitize(c.in)
			if got != c.want {
				t.Errorf("Sanitize(%q) = %q; want %q", c.in, got, c.want)
			}
		})
	}
}

func TestSanitize_FastPathReturnsSameString(t *testing.T) {
	clean := "no control chars here"
	got := termsafe.Sanitize(clean)
	// We don't assert pointer identity (Go strings are immutable), just
	// that the value is unchanged.
	if got != clean {
		t.Errorf("clean input mutated: %q -> %q", clean, got)
	}
}

func TestSanitize_ReplacementIsVisible(t *testing.T) {
	if !strings.ContainsRune("�", termsafe.Replacement) {
		t.Errorf("Replacement = %q; expected U+FFFD", termsafe.Replacement)
	}
}
