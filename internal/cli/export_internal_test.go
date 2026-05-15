package cli

import (
	"strings"
	"testing"

	"github.com/sethdeckard/atlas/internal/repo"
)

// TestRenderMarkdown_HostileFieldsAreEscaped feeds renderMarkdown
// repo fields with CommonMark-active characters and asserts the
// output stays well-formed: bold spans don't terminate early,
// backtick code fences expand to contain interior backticks, and
// hostile origin URLs render as inline code instead of clickable
// links.
func TestRenderMarkdown_HostileFieldsAreEscaped(t *testing.T) {
	repos := []repo.Repo{
		{
			Name:         "weird*name[bracket]",
			Path:         "/r/weird*name[bracket]",
			Branch:       "feat`tick",
			Languages:    []string{"go"},
			OriginURL:    "https://github.com/owner/repo",
			ActivityTier: "active",
		},
		{
			Name:         "bash-bang!repo",
			Path:         "/r/bash-bang!repo",
			Branch:       "main",
			OriginURL:    "javascript://example.com/%0aalert(1)",
			ActivityTier: "active",
		},
		{
			Name:         "scp-origin",
			Path:         "/r/scp-origin",
			Branch:       "main",
			OriginURL:    "git@github.com:owner/repo.git",
			ActivityTier: "active",
		},
	}

	out := renderMarkdown(repos, "/r")

	// Bold span around the name must survive: `*` and `[`/`]` are escaped.
	if !strings.Contains(out, `**weird\*name\[bracket\]**`) {
		t.Errorf("expected escaped bold name; got:\n%s", out)
	}
	// Branch backtick is wrapped in a 2-backtick fence (no padding needed
	// since the value neither starts nor ends with a backtick).
	if !strings.Contains(out, "``feat`tick``") {
		t.Errorf("expected double-backtick fence around branch with `; got:\n%s", out)
	}
	// `!` in repo name is escaped so `[` (also escaped) can't ever form an image.
	if !strings.Contains(out, `**bash-bang\!repo**`) {
		t.Errorf("expected escaped `!` in name; got:\n%s", out)
	}
	// javascript: URL must not produce a link target.
	if strings.Contains(out, "[origin](javascript:") {
		t.Errorf("javascript URL leaked into link target:\n%s", out)
	}
	// Instead it should appear as inline code under "origin ".
	if !strings.Contains(out, "origin `javascript:") {
		t.Errorf("expected rejected URL to render as inline code; got:\n%s", out)
	}
	// SCP form is converted by BrowserURL to https — should land as a link.
	if !strings.Contains(out, "[origin](https://github.com/owner/repo)") {
		t.Errorf("expected SCP-form origin to convert into https link; got:\n%s", out)
	}
}

// TestRenderMarkdown_NeutralizesLineBreaks confirms that a repo
// directory name containing a newline can't terminate a bullet and
// inject a second heading or list item on the following line. On
// Unix filesystems literal newlines in directory names are legal,
// so this is the realistic vector — not just a synthetic attack.
func TestRenderMarkdown_NeutralizesLineBreaks(t *testing.T) {
	repos := []repo.Repo{
		{
			Name:         "evil\n## INJECTED HEADING",
			Path:         "/r/evil",
			Branch:       "main\ngarbage",
			ActivityTier: "active",
		},
	}
	out := renderMarkdown(repos, "/r")
	if strings.Contains(out, "\n## INJECTED HEADING") {
		t.Errorf("newline in repo name escaped construct and injected a heading:\n%s", out)
	}
	// Branch code span must not span lines either — a literal \n in
	// the code value would close the span on most renderers.
	if strings.Contains(out, "main\ngarbage") {
		t.Errorf("newline survived branch code escape:\n%s", out)
	}
}

// TestRenderMarkdown_LinkTargetDelimitersEncoded confirms a URL that
// passes BrowserURL but contains characters meaningful inside a
// [text](url) destination — most importantly `)` — gets those
// characters percent-encoded so the link can't be terminated early
// and used to inject additional Markdown.
func TestRenderMarkdown_LinkTargetDelimitersEncoded(t *testing.T) {
	repos := []repo.Repo{
		{
			Name:         "delim",
			Path:         "/r/delim",
			Branch:       "main",
			OriginURL:    "https://example.com/a)[evil](https://evil",
			ActivityTier: "active",
		},
	}
	out := renderMarkdown(repos, "/r")

	// The injection signature — a closing `)` followed by `[evil](` —
	// must not appear in the rendered link destination.
	if strings.Contains(out, "[evil](") {
		t.Errorf("hostile delimiter sequence leaked through into output:\n%s", out)
	}
	// The encoded form should appear instead.
	if !strings.Contains(out, "%29%5Bevil%5D%28") {
		t.Errorf("expected percent-encoded delimiters in link target; got:\n%s", out)
	}
}

// TestMdLinkTarget exercises the encoder directly.
func TestMdLinkTarget(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"https://github.com/owner/repo", "https://github.com/owner/repo"},
		{"https://example.com/a)b", "https://example.com/a%29b"},
		{"https://example.com/[x]", "https://example.com/%5Bx%5D"},
		{"https://example.com/<a>", "https://example.com/%3Ca%3E"},
		{"https://example.com/with space", "https://example.com/with%20space"},
		{"https://example.com/tick`", "https://example.com/tick%60"},
	}
	for _, c := range cases {
		got := mdLinkTarget(c.in)
		if got != c.want {
			t.Errorf("mdLinkTarget(%q) = %q; want %q", c.in, got, c.want)
		}
	}
}

// TestMdEscapeCode covers the fence-length and padding rules.
func TestMdEscapeCode(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"main", "`main`"},
		{"feat`tick", "``feat`tick``"},
		{"`leading", "`` `leading ``"},
		{"trailing`", "`` trailing` ``"},
		{"a``b", "```a``b```"},
		{"", "``"},
	}
	for _, c := range cases {
		got := mdEscapeCode(c.in)
		if got != c.want {
			t.Errorf("mdEscapeCode(%q) = %q; want %q", c.in, got, c.want)
		}
	}
}
