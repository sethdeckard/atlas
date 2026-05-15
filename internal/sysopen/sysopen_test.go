package sysopen_test

import (
	"errors"
	"os/exec"
	"strings"
	"testing"

	"github.com/sethdeckard/atlas/internal/sysopen"
)

// TestOpen_DispatchesViaInjectedOpener verifies that Open routes the
// converted URL through the injected Opener factory and runs the
// resulting command. We swap in /usr/bin/true so the test never hits
// the user's actual browser.
func TestOpen_DispatchesViaInjectedOpener(t *testing.T) {
	prev := sysopen.Opener
	t.Cleanup(func() { sysopen.Opener = prev })

	var called string
	sysopen.Opener = func(url string) *exec.Cmd {
		called = url
		return exec.Command("/usr/bin/true")
	}

	if err := sysopen.Open("https://github.com/owner/repo"); err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !strings.HasPrefix(called, "https://") {
		t.Errorf("Opener received %q; want a https URL", called)
	}
}

// TestOpen_ConvertsSCPForm: a git@host:owner/repo origin must be
// converted to https before being handed to the OS opener.
func TestOpen_ConvertsSCPForm(t *testing.T) {
	prev := sysopen.Opener
	t.Cleanup(func() { sysopen.Opener = prev })

	var called string
	sysopen.Opener = func(url string) *exec.Cmd {
		called = url
		return exec.Command("/usr/bin/true")
	}
	if err := sysopen.Open("git@github.com:owner/repo.git"); err != nil {
		t.Fatalf("Open: %v", err)
	}
	if called != "https://github.com/owner/repo" {
		t.Errorf("opened %q; want https://github.com/owner/repo", called)
	}
}

// TestOpen_NotBrowsableSurfacesError: empty / file:// / unknown schemes
// must return ErrNotBrowsable so the TUI can show a status message
// instead of running an OS command.
func TestOpen_NotBrowsableSurfacesError(t *testing.T) {
	prev := sysopen.Opener
	t.Cleanup(func() { sysopen.Opener = prev })

	called := false
	sysopen.Opener = func(url string) *exec.Cmd {
		called = true
		return exec.Command("/usr/bin/true")
	}
	for _, in := range []string{"", "file:///tmp/r", "/local/path"} {
		err := sysopen.Open(in)
		if !errors.Is(err, sysopen.ErrNotBrowsable) {
			t.Errorf("Open(%q): err = %v; want ErrNotBrowsable", in, err)
		}
	}
	if called {
		t.Errorf("Opener should not have been invoked for non-browsable inputs")
	}
}
