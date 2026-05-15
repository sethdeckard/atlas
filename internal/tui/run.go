package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/term"

	"github.com/sethdeckard/atlas/internal/cache"
	"github.com/sethdeckard/atlas/internal/config"
	"github.com/sethdeckard/atlas/internal/onboard"
)

// promptForRoot is the seam between the TUI launcher and onboard's
// interactive prompt. Defined as a package var so tests can substitute a
// non-interactive stub; production wires it to onboard.Prompter.EnsureRoot
// against os.Stdin/os.Stderr.
var promptForRoot = defaultPromptForRoot

func defaultPromptForRoot(ctx context.Context, configPath string, cfg config.Config) (string, error) {
	home, _ := os.UserHomeDir()
	p := onboard.Prompter{
		In:         os.Stdin,
		Out:        os.Stderr,
		IsTerminal: stdioInteractive,
		HomeDir:    home,
	}
	return p.EnsureRoot(ctx, configPath, cfg)
}

// stdioInteractive reports whether stdin, stdout, AND stderr are all
// terminals — the conservative gate used before opening an interactive
// prompt. Requiring stdout (in addition to stdin/stderr) prevents
// stdout-redirected callers from blocking on a hidden prompt and routes
// them to the documented no-TTY error from onboard instead.
func stdioInteractive() bool {
	return term.IsTerminal(os.Stdin.Fd()) &&
		term.IsTerminal(os.Stdout.Fd()) &&
		term.IsTerminal(os.Stderr.Fd())
}

// SetPromptForRoot swaps the onboarding prompt seam and returns a restore
// function. Intended for tests only — production callers must not invoke
// this. Use as `defer tui.SetPromptForRoot(stub)()`.
func SetPromptForRoot(f func(ctx context.Context, configPath string, cfg config.Config) (string, error)) func() {
	prev := promptForRoot
	promptForRoot = f
	return func() { promptForRoot = prev }
}

// Run launches the TUI scoped to root (the optional positional argument from
// the CLI). It loads the cache + config synchronously so the first View
// renders instantly, then hands control to Bubble Tea.
func Run(ctx context.Context, rootArg string) error {
	cfgPath, err := config.DefaultPath()
	if err != nil {
		return err
	}
	cfg, warnings, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	// Surface config warnings on stderr before the alt screen takes over
	// so a typo'd theme / skip_dirs entry isn't buried in the ? overlay.
	// Mirrors the CLI's `warning: <text>` convention; the warnings also
	// remain visible inside the TUI via the help overlay.
	for _, w := range warnings {
		fmt.Fprintln(os.Stderr, "warning: "+w)
	}
	root, err := resolveRoot(ctx, rootArg, cfg, cfgPath)
	if err != nil {
		return err
	}
	if info, err := os.Stat(root); err != nil {
		return fmt.Errorf("root %s: %w", root, err)
	} else if !info.IsDir() {
		return fmt.Errorf("root %s is not a directory", root)
	}

	cachePath, err := cache.DefaultPath()
	if err != nil {
		return err
	}
	c, err := cache.Load(cachePath)
	if err != nil {
		return err
	}

	model := New(ctx, c, cachePath, cfg, root)
	model.warnings = warnings

	// Render the TUI on the controlling terminal regardless of how
	// stdin/stdout are connected. This keeps stdout exclusively for
	// the cdTarget print below, so a shell wrapper running
	// `target=$(command atlas --cd ...)` can capture only the path.
	// On Windows (no /dev/tty) and on hosts without a controlling
	// terminal we fall back to bubbletea's defaults.
	opts := []tea.ProgramOption{tea.WithAltScreen(), tea.WithContext(ctx)}
	tty, ttyErr := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if ttyErr == nil {
		defer tty.Close()
		opts = append(opts, tea.WithInput(tty), tea.WithOutput(tty))
	}

	prog := tea.NewProgram(model, opts...)
	final, err := prog.Run()
	// Enter sets cdTarget to the selected repo's path. Print it on
	// stdout after the alt screen tears down so a shell wrapper —
	// e.g. `atlas() { local d; d=$(command atlas --cd "$@") || return; cd "$d"; }`
	// — can `cd` into it. Stdout is the only thing the wrapper
	// captures; every other write must stay on stderr.
	if m, ok := final.(Model); ok && m.cdTarget != "" {
		fmt.Fprintln(os.Stdout, m.cdTarget)
	}
	return err
}

func resolveRoot(ctx context.Context, arg string, cfg config.Config, cfgPath string) (string, error) {
	if arg != "" {
		expanded, err := config.ExpandHome(arg)
		if err != nil {
			return "", err
		}
		return filepath.Abs(expanded)
	}
	if cfg.Root != "" {
		// cfg.Root has already been ~-expanded by config.Load.
		return filepath.Abs(cfg.Root)
	}
	// No source supplied a root — onboard. The prompt persists the
	// answer to cfgPath so subsequent launches skip this step. ctx
	// flows in so SIGINT during the prompt aborts the read.
	return promptForRoot(ctx, cfgPath, cfg)
}
