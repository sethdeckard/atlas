package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/sethdeckard/atlas/internal/cli"
	"github.com/sethdeckard/atlas/internal/tui"

	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"
)

var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

const helpLogo = "       _   _           \n" +
	"  __ _| |_| | __ _ ___ \n" +
	" / _` | __| |/ _` / __|\n" +
	"| (_| | |_| | (_| \\__ \\\n" +
	" \\__,_|\\__|_|\\__,_|___/"

func main() {
	os.Exit(run())
}

func run() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	listCmd := cli.NewListCommand()

	var (
		rootFlag string
		cdFlag   bool
	)
	rootCmd := &cobra.Command{
		Use:           "atlas [PATH]",
		Short:         "Curated map of every Git repository under your projects root",
		Long:          helpLogo + "\n\nCurated map of every Git repository under your projects root",
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       fmt.Sprintf("%s (commit %s, built %s)", version, commit, date),
		RunE: func(cmd *cobra.Command, args []string) error {
			// --cd forces TUI mode: the cd-launcher wrapper captures
			// stdout to read the selected repo path, so stdout looks
			// like a pipe but the user actually wants the TUI on
			// /dev/tty. Without --cd, the no-TTY guard below routes
			// piped invocations (`atlas | cat`) to the CLI list as
			// before.
			if !cdFlag && !term.IsTerminal(os.Stdout.Fd()) {
				// Forward --root to listCmd's own flag set (cobra didn't
				// parse it there since we routed through rootCmd).
				// Propagate the parent command's context so SIGINT/
				// SIGTERM cancellation reaches scans and git
				// subprocesses; otherwise listCmd.Context() would
				// default to context.Background().
				if rootFlag != "" {
					_ = listCmd.Flags().Set("root", rootFlag)
				}
				listCmd.SetContext(cmd.Context())
				return listCmd.RunE(listCmd, args)
			}
			arg := ""
			if len(args) == 1 {
				arg = args[0]
			} else if rootFlag != "" {
				arg = rootFlag
			}
			return tui.Run(cmd.Context(), arg)
		},
	}
	rootCmd.Flags().StringVar(&rootFlag, "root", "",
		"scan root (overridden by positional PATH; falls back to config or onboarding)")
	rootCmd.Flags().BoolVar(&cdFlag, "cd", false,
		"force TUI on /dev/tty and reserve stdout for the selected repo path (for shell cd wrappers)")
	rootCmd.AddCommand(listCmd)
	rootCmd.AddCommand(cli.NewExportCommand())
	rootCmd.AddCommand(cli.NewRefreshCommand())
	rootCmd.AddCommand(cli.NewInitCommand())

	if err := rootCmd.ExecuteContext(ctx); err != nil {
		var partial *cli.PartialError
		if errors.As(err, &partial) {
			fmt.Fprintf(os.Stderr, "atlas: %v\n", err)
			return 2
		}
		fmt.Fprintln(os.Stderr, "atlas:", err)
		return 1
	}
	return 0
}
