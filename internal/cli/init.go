package cli

import (
	"context"
	"os"

	"github.com/spf13/cobra"

	"github.com/sethdeckard/atlas/internal/config"
	"github.com/sethdeckard/atlas/internal/onboard"
)

// runInitFn is the seam between `atlas init` and onboard's PromptRoot.
// Tests substitute this to bypass real interactive I/O. Production wires
// it to a Prompter that always re-prompts and persists the answer to
// the config file.
var runInitFn = defaultRunInit

func defaultRunInit(ctx context.Context, configPath string, cfg config.Config) error {
	home, _ := os.UserHomeDir()
	p := onboard.Prompter{
		In:         os.Stdin,
		Out:        os.Stderr,
		IsTerminal: stdioInteractive,
		HomeDir:    home,
	}
	_, err := p.PromptRoot(ctx, configPath, cfg)
	return err
}

// SetRunInitForTest swaps the `atlas init` seam and returns a restore
// function. Intended for tests only — production callers must not invoke
// this. Use as `defer cli.SetRunInitForTest(stub)()`.
func SetRunInitForTest(f func(ctx context.Context, configPath string, cfg config.Config) error) func() {
	prev := runInitFn
	runInitFn = f
	return func() { runInitFn = prev }
}

// NewInitCommand returns the `atlas init` cobra subcommand. It (re)runs
// the onboarding prompt regardless of whether a root is already
// configured, then writes the answer to the config file.
func NewInitCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Configure the projects root directory",
		Long: `Prompts for the projects root and saves it to atlas's config file
(~/.config/atlas/config.toml by default). Existing values you've set
are preserved; commented "atlas:default" blocks are regenerated to
reflect the current build's defaults.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfgPath, err := config.DefaultPath()
			if err != nil {
				return err
			}
			cfg, _, err := config.Load(cfgPath)
			if err != nil {
				return err
			}
			return runInitFn(cmd.Context(), cfgPath, cfg)
		},
	}
}
