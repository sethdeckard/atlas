// Package onboard runs atlas's interactive prompt for the projects root.
// It's the fallback the CLI pipeline and the TUI fall through to when
// neither a positional [PATH], a --root flag, nor a `root:` in the config
// supplies a value. The user's answer is persisted to the config file so
// subsequent launches skip the prompt.
//
// The package depends on internal/config (for Save and Config) and the
// stdlib only — internal/cli and internal/tui both depend on it, so it
// must stay leaf-level.
package onboard

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/sethdeckard/atlas/internal/config"
)

// Prompter runs the interactive prompt. All I/O is injectable so tests
// can drive it without touching a real TTY.
type Prompter struct {
	In         io.Reader   // typically os.Stdin
	Out        io.Writer   // typically os.Stderr — keep stdout clean for pipelines
	IsTerminal func() bool // wraps term.IsTerminal on real Fds
	HomeDir    string      // for the ~/projects existence check
}

// EnsureRoot prompts for a root and persists it to configPath. It is the
// implicit-onboarding entry point: callers fall through to it only when no
// other source supplied a root. Returns the resolved absolute path. ctx
// cancellation (e.g. SIGINT via the cobra root's signal.NotifyContext)
// aborts the read and returns the wrapped ctx error.
func (p Prompter) EnsureRoot(ctx context.Context, configPath string, cfg config.Config) (string, error) {
	if !p.isTerminal() {
		return "", noTTYError(configPath)
	}
	return p.runPrompt(ctx, configPath, cfg, suggestionFor("", p.HomeDir))
}

// PromptRoot prompts for a root regardless of whether one is configured.
// It's the explicit-onboarding entry point used by `atlas init`. The
// suggestion seeds from cfg.Root if non-empty, otherwise from the
// ~/projects-if-exists fallback. ctx cancellation aborts the read.
func (p Prompter) PromptRoot(ctx context.Context, configPath string, cfg config.Config) (string, error) {
	if !p.isTerminal() {
		return "", noTTYError(configPath)
	}
	return p.runPrompt(ctx, configPath, cfg, suggestionFor(cfg.Root, p.HomeDir))
}

func (p Prompter) isTerminal() bool {
	if p.IsTerminal == nil {
		return false
	}
	return p.IsTerminal()
}

// runPrompt is the shared loop: ask, validate, persist, return.
func (p Prompter) runPrompt(ctx context.Context, configPath string, cfg config.Config, suggestion string) (string, error) {
	writeWelcome(p.Out, configPath)

	scanner := bufio.NewScanner(p.In)
	for {
		if suggestion != "" {
			fmt.Fprintf(p.Out, "  projects root [%s]: ", suggestion)
		} else {
			fmt.Fprint(p.Out, "  projects root: ")
		}

		text, ok, err := readLine(ctx, scanner)
		if err != nil {
			return "", err
		}
		if !ok {
			return "", fmt.Errorf("no input on stdin")
		}
		typed := strings.TrimSpace(text)
		if typed == "" {
			if suggestion == "" {
				fmt.Fprintln(p.Out, "  a path is required")
				continue
			}
			typed = suggestion
		}

		expanded, err := config.ExpandHome(typed)
		if err != nil {
			fmt.Fprintf(p.Out, "  %v\n", err)
			continue
		}
		absPath, err := filepath.Abs(expanded)
		if err != nil {
			fmt.Fprintf(p.Out, "  %v\n", err)
			continue
		}
		info, err := os.Stat(absPath)
		if err != nil || !info.IsDir() {
			fmt.Fprintf(p.Out, "  directory does not exist: %s\n", typed)
			continue
		}

		// Persist the as-typed form so a saved "~/code" reads naturally
		// in the TOML; Load expands it again on the next launch.
		cfg.Root = typed
		if err := config.WriteInitTOML(configPath, cfg, config.Defaults()); err != nil {
			return "", fmt.Errorf("save config: %w", err)
		}
		fmt.Fprintf(p.Out, "  saved root to %s\n", config.ContractHome(configPath))
		return absPath, nil
	}
}

// writeWelcome prints the figlet logo + a short copy block that names
// the configPath the answer will be saved to.
func writeWelcome(w io.Writer, configPath string) {
	fmt.Fprintln(w)
	fmt.Fprintln(w, "       _   _           ")
	fmt.Fprintln(w, "  __ _| |_| | __ _ ___ ")
	fmt.Fprintln(w, " / _` | __| |/ _` / __|")
	fmt.Fprintln(w, "| (_| | |_| | (_| \\__ \\")
	fmt.Fprintln(w, " \\__,_|\\__|_|\\__,_|___/")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  Where do your Git projects live? atlas walks this tree")
	fmt.Fprintln(w, "  to discover repos.")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "  Saved to %s\n", config.ContractHome(configPath))
	fmt.Fprintln(w)
}

// scanResult bridges a blocking bufio.Scanner read into a select-able
// channel value.
type scanResult struct {
	text string
	ok   bool
	err  error
}

// readLine wraps scanner.Scan() in a goroutine so the caller can race
// it against ctx cancellation. On cancel the goroutine leaks until the
// next byte arrives on stdin; that's fine here because the process is
// exiting (SIGINT cancelled the cobra root's signal.NotifyContext).
func readLine(ctx context.Context, scanner *bufio.Scanner) (string, bool, error) {
	ch := make(chan scanResult, 1)
	go func() {
		ok := scanner.Scan()
		ch <- scanResult{text: scanner.Text(), ok: ok, err: scanner.Err()}
	}()
	select {
	case r := <-ch:
		if r.err != nil {
			return "", false, fmt.Errorf("read input: %w", r.err)
		}
		return r.text, r.ok, nil
	case <-ctx.Done():
		return "", false, ctx.Err()
	}
}

// suggestionFor returns the prefilled prompt suggestion: prefer the
// already-configured root when set, otherwise "~/projects" when that
// directory exists under HomeDir, otherwise empty (no suggestion).
func suggestionFor(currentRoot, homeDir string) string {
	if currentRoot != "" {
		return currentRoot
	}
	if homeDir == "" {
		return ""
	}
	candidate := filepath.Join(homeDir, "projects")
	if info, err := os.Stat(candidate); err == nil && info.IsDir() {
		return "~/projects"
	}
	return ""
}

func noTTYError(configPath string) error {
	return fmt.Errorf(`no projects root configured. Run "atlas init" interactively, or set root = "/your/projects" in %s`, config.ContractHome(configPath))
}
