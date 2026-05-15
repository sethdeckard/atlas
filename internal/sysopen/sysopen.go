package sysopen

import (
	"fmt"
	"os/exec"
	"runtime"
)

// Opener resolves a URL into the OS-specific command that opens it in
// the default browser. Returns the command, ready to Run.
//
// The factory is a package var so tests can inject a fake without
// actually launching a browser.
var Opener func(url string) *exec.Cmd = defaultOpener

// Open routes `origin` through BrowserURL and runs the resulting browser
// command. Returns ErrNotBrowsable when the origin can't be presented to
// a browser.
func Open(origin string) error {
	url, err := BrowserURL(origin)
	if err != nil {
		return err
	}
	cmd := Opener(url)
	if cmd == nil {
		return fmt.Errorf("no browser command available for %s", runtime.GOOS)
	}
	return cmd.Run()
}

func defaultOpener(url string) *exec.Cmd {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", url)
	case "linux":
		return exec.Command("xdg-open", url)
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		return nil
	}
}
