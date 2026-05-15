package sysopen_test

import (
	"errors"
	"testing"

	"github.com/sethdeckard/atlas/internal/sysopen"
)

func TestBrowserURL(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    string
		wantErr error
	}{
		{"https passthrough", "https://github.com/owner/repo", "https://github.com/owner/repo", nil},
		{"http passthrough", "http://example.com/x", "http://example.com/x", nil},
		{"scp form basic", "git@github.com:owner/repo.git", "https://github.com/owner/repo", nil},
		{"scp form no .git", "git@github.com:owner/repo", "https://github.com/owner/repo", nil},
		{"scp form deep path", "git@gitlab.com:group/sub/repo.git", "https://gitlab.com/group/sub/repo", nil},
		{"ssh:// basic", "ssh://git@github.com/owner/repo.git", "https://github.com/owner/repo", nil},
		{"ssh:// with port", "ssh://git@gitlab.com:22/owner/repo.git", "https://gitlab.com/owner/repo", nil},
		{"ssh:// no user", "ssh://gitlab.com/owner/repo", "https://gitlab.com/owner/repo", nil},

		{"empty", "", "", sysopen.ErrNotBrowsable},
		{"whitespace only", "   ", "", sysopen.ErrNotBrowsable},
		{"file scheme", "file:///tmp/r", "", sysopen.ErrNotBrowsable},
		{"bare path", "/local/path", "", sysopen.ErrNotBrowsable},
		{"scp missing path", "git@github.com:", "", sysopen.ErrNotBrowsable},
		{"scp missing host", "git@:owner/repo", "", sysopen.ErrNotBrowsable},

		// Hardened validation cases.
		{"userinfo username", "https://user@evil.com/x", "", sysopen.ErrNotBrowsable},
		{"userinfo with password", "https://user:pw@evil.com/x", "", sysopen.ErrNotBrowsable},
		{"embedded null", "https://github.com\x00.evil.com/x", "", sysopen.ErrNotBrowsable},
		{"embedded LF", "https://github.com/x\ny", "", sysopen.ErrNotBrowsable},
		{"embedded CR", "https://github.com/x\ry", "", sysopen.ErrNotBrowsable},
		{"embedded BEL", "https://github.com/x\x07", "", sysopen.ErrNotBrowsable},
		{"DEL char", "https://github.com/x\x7f", "", sysopen.ErrNotBrowsable},
		{"javascript scheme", "javascript://example.com/%0aalert(1)", "", sysopen.ErrNotBrowsable},
		{"data scheme", "data:text/html,<script>alert(1)</script>", "", sysopen.ErrNotBrowsable},
		{"empty host", "https:///path", "", sysopen.ErrNotBrowsable},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := sysopen.BrowserURL(c.in)
			if c.wantErr != nil {
				if !errors.Is(err, c.wantErr) {
					t.Errorf("err = %v; want %v", err, c.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if got != c.want {
				t.Errorf("got %q; want %q", got, c.want)
			}
		})
	}
}
