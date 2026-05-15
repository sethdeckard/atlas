// Package sysopen wraps the OS-specific commands for opening a URL in
// the user's default browser. The TUI's `o` binding uses it to open the
// selected repo's origin URL.
//
// Git origins aren't always browser-openable as-is (SCP-form
// `git@host:owner/repo.git` is the common case), so this package also
// converts known formats to https://. Anything that can't be safely
// presented to a browser returns ErrNotBrowsable, which the caller
// surfaces as a status-bar message rather than a hard failure.
package sysopen

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// ErrNotBrowsable is returned by BrowserURL for inputs that cannot be
// safely opened in a browser (file://, empty origin, malformed values).
// Callers should surface a user-friendly message and skip the open.
var ErrNotBrowsable = errors.New("origin is not a browser URL")

// BrowserURL converts a git origin URL into something a browser can
// open. Conversion rules:
//
//   - https:// or http:// → returned normalized.
//   - git@host:owner/repo[.git] (SCP-form SSH) →
//     https://host/owner/repo.
//   - ssh://[user@]host[:port]/owner/repo[.git] →
//     https://host/owner/repo.
//   - file:// or empty → ErrNotBrowsable.
//   - Anything else → ErrNotBrowsable.
//
// The trailing `.git` suffix is stripped from path components when
// present. The user (account) portion of an SSH URL is dropped.
//
// The final candidate is run through a strict validator (see
// validateBrowserURL) that rejects userinfo, non-http(s) schemes,
// empty hosts, and embedded control characters — so callers can
// hand the result to a browser or render it as a markdown link
// without further checks.
func BrowserURL(origin string) (string, error) {
	origin = strings.TrimSpace(origin)
	if origin == "" {
		return "", ErrNotBrowsable
	}
	var candidate string
	var err error
	switch {
	case strings.HasPrefix(origin, "https://"), strings.HasPrefix(origin, "http://"):
		candidate = origin
	case strings.HasPrefix(origin, "file://"):
		return "", ErrNotBrowsable
	case strings.HasPrefix(origin, "ssh://"):
		candidate, err = convertSSHScheme(strings.TrimPrefix(origin, "ssh://"))
	case strings.Contains(origin, "@") && strings.Contains(origin, ":") &&
		!strings.HasPrefix(origin, "/"):
		candidate, err = convertSCPForm(origin)
	default:
		return "", ErrNotBrowsable
	}
	if err != nil {
		return "", err
	}
	return validateBrowserURL(candidate)
}

// validateBrowserURL parses raw via net/url and accepts only http/https
// URLs with a non-empty host and no userinfo. Control characters are
// rejected up front because net/url tolerates them in some positions
// (e.g. inside the path). The returned string is the parser's
// canonical form, not the original input.
func validateBrowserURL(raw string) (string, error) {
	for _, r := range raw {
		if r < 0x20 || r == 0x7f {
			return "", ErrNotBrowsable
		}
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", ErrNotBrowsable
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", ErrNotBrowsable
	}
	if u.Host == "" {
		return "", ErrNotBrowsable
	}
	if u.User != nil {
		return "", ErrNotBrowsable
	}
	return u.String(), nil
}

// convertSCPForm handles git@host:owner/repo[.git].
func convertSCPForm(s string) (string, error) {
	at := strings.Index(s, "@")
	if at < 0 || at == len(s)-1 {
		return "", ErrNotBrowsable
	}
	rest := s[at+1:]
	colon := strings.Index(rest, ":")
	if colon < 0 || colon == 0 || colon == len(rest)-1 {
		return "", ErrNotBrowsable
	}
	host := rest[:colon]
	path := rest[colon+1:]
	path = strings.TrimSuffix(path, ".git")
	if path == "" || strings.HasPrefix(path, "/") {
		return "", ErrNotBrowsable
	}
	return fmt.Sprintf("https://%s/%s", host, path), nil
}

// convertSSHScheme handles ssh://[user@]host[:port]/owner/repo[.git].
// Already-stripped-of-prefix form: [user@]host[:port]/path.
func convertSSHScheme(s string) (string, error) {
	slash := strings.Index(s, "/")
	if slash <= 0 || slash == len(s)-1 {
		return "", ErrNotBrowsable
	}
	hostPart := s[:slash]
	path := s[slash+1:]
	if at := strings.Index(hostPart, "@"); at >= 0 {
		hostPart = hostPart[at+1:]
	}
	if colon := strings.Index(hostPart, ":"); colon > 0 {
		hostPart = hostPart[:colon]
	}
	if hostPart == "" {
		return "", ErrNotBrowsable
	}
	path = strings.TrimSuffix(path, ".git")
	if path == "" {
		return "", ErrNotBrowsable
	}
	return fmt.Sprintf("https://%s/%s", hostPart, path), nil
}
