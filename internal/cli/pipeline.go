package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/charmbracelet/x/term"

	"github.com/sethdeckard/atlas/internal/cache"
	"github.com/sethdeckard/atlas/internal/config"
	"github.com/sethdeckard/atlas/internal/onboard"
	"github.com/sethdeckard/atlas/internal/repo"
	"github.com/sethdeckard/atlas/internal/scan"
)

// promptForRoot is the seam between the cli pipeline and onboard's
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
// prompt. Crucially, requiring stdout to be a TTY prevents script-mode
// invocations like `atlas list >out.txt` (or the no-TTY fallback in
// cmd/atlas/main.go that re-routes `atlas | cat` through `list`) from
// blocking on stdin: stdout-redirected callers get the documented
// no-TTY error from onboard instead of a hidden prompt.
func stdioInteractive() bool {
	return term.IsTerminal(os.Stdin.Fd()) &&
		term.IsTerminal(os.Stdout.Fd()) &&
		term.IsTerminal(os.Stderr.Fd())
}

// SetPromptForRoot swaps the onboarding prompt seam and returns a restore
// function. Intended for tests only — production callers must not invoke
// this. Use as `defer cli.SetPromptForRoot(stub)()`.
func SetPromptForRoot(f func(ctx context.Context, configPath string, cfg config.Config) (string, error)) func() {
	prev := promptForRoot
	promptForRoot = f
	return func() { promptForRoot = prev }
}

// Pipeline is the shared backbone every cache-consuming CLI subcommand
// runs through: load config → resolve root → load cache → discover →
// reconcile → refresh stale → status pass over the rest → annotate
// derived signals → return the scoped+annotated repo set. `list`,
// `export`, and `refresh` are all just thin filter/sort/render layers
// on top of this so cache and refresh behavior can't drift between
// subcommands.
type Pipeline struct {
	Config       config.Config
	Root         string
	Cache        *cache.Cache
	cachePath    string
	useCachedOnly bool
	fresh        bool
	walkErrors   int
}

// PipelineOpts captures the cross-subcommand inputs that decide where
// to scan and which work to skip. Subcommand-specific flags
// (--language, --markdown, --verbose, etc.) live on the subcommand
// itself and are applied after Run returns.
type PipelineOpts struct {
	// PathArg is the positional [PATH] argument; empty if not given.
	PathArg string
	// RootFlag is the value of --root; empty if not given.
	RootFlag string
	// UseCachedOnly skips discover/reconcile/refresh and reads
	// straight from the cache. Renders persisted last-known values
	// stale-by-design (the same contract Dirty already had); fast
	// for shell pipelines that don't want git latency.
	UseCachedOnly bool
	// Fresh bypasses cache mtime validation and forces a full Read
	// for every discovered path. Mutually exclusive with
	// UseCachedOnly (NewPipeline returns an error if both set).
	Fresh bool
}

// NewPipeline loads config + cache, prints any config warnings to
// `warningsOut`, and returns a ready-to-Run pipeline. Errors here are
// fatal-to-the-subcommand: bad flags, unreadable root, or a
// cache-path that can't be resolved.
func NewPipeline(ctx context.Context, opts PipelineOpts, warningsOut io.Writer) (*Pipeline, error) {
	if opts.UseCachedOnly && opts.Fresh {
		return nil, fmt.Errorf("--cached and --fresh are mutually exclusive")
	}

	cfgPath, err := config.DefaultPath()
	if err != nil {
		return nil, err
	}
	cfg, warnings, err := config.Load(cfgPath)
	if err != nil {
		return nil, err
	}
	if warningsOut != nil {
		for _, w := range warnings {
			fmt.Fprintln(warningsOut, "warning: "+w)
		}
	}

	root, err := resolvePipelineRoot(ctx, opts, cfg, cfgPath)
	if err != nil {
		return nil, err
	}
	if !opts.UseCachedOnly {
		if err := assertDirectory(root); err != nil {
			return nil, err
		}
	}

	cachePath, err := cache.DefaultPath()
	if err != nil {
		return nil, err
	}
	c, err := cache.Load(cachePath)
	if err != nil {
		return nil, err
	}

	return &Pipeline{
		Config:        cfg,
		Root:          root,
		Cache:         c,
		cachePath:     cachePath,
		useCachedOnly: opts.UseCachedOnly,
		fresh:         opts.Fresh,
	}, nil
}

// Run executes the pipeline and returns every repo under Root with
// derived signals annotated. ctx cancellation is honored at every
// stage (discovery, refresh) so SIGINT during the run terminates
// early rather than completing the work.
//
// The returned slice is the *scoped+annotated* set, before any
// subcommand-specific filter/sort/limit. Subcommands run their
// applyFiltersSortLimit afterward.
func (p *Pipeline) Run(ctx context.Context) ([]repo.Repo, error) {
	if p.useCachedOnly {
		scoped := scopedRepos(p.Cache, p.Root)
		repo.AnnotateDerived(scoped, p.Config.StaleDays, nowFunc())
		return scoped, nil
	}

	paths, walkErr := scan.Discover(ctx, p.Root, scan.Options{
		SkipBaseNames: p.Config.SkipBaseNames,
		SkipAbsPaths:  p.Config.SkipAbsPaths,
		MaxDepth:      p.Config.MaxDepth,
	})
	if walkErr != nil {
		// Non-fatal — partial discovery still produces useful data.
		// The exit-code policy at the subcommand layer surfaces it.
		p.walkErrors++
	}

	pathsToRead, statusOnlyPaths := p.Cache.Reconcile(p.Root, paths, p.fresh)
	runFullRefresh(ctx, p.Cache, pathsToRead)
	runStatusRefresh(ctx, p.Cache, statusOnlyPaths)

	scoped := scopedRepos(p.Cache, p.Root)
	repo.AnnotateDerived(scoped, p.Config.StaleDays, nowFunc())
	return scoped, walkErr
}

// Save persists the cache atomically. Subcommands call this after
// Run for normal subcommand behavior; --cached short-circuits skip
// the save (nothing changed).
func (p *Pipeline) Save() error {
	if p.useCachedOnly {
		return nil
	}
	return cache.Save(p.cachePath, p.Cache)
}

// WalkErrors returns the number of non-fatal walk errors discovery
// surfaced. Used by subcommands' exit-code policy.
func (p *Pipeline) WalkErrors() int { return p.walkErrors }

// resolvePipelineRoot encodes the precedence: positional PATH wins,
// then --root, then config root, then onboarding. A [PATH] arg or --root
// flag is treated as a one-off override and is **never** offered for save
// — onboarding only fires when no source supplies a value. ctx flows
// into the onboarding prompt so SIGINT during the read aborts cleanly.
func resolvePipelineRoot(ctx context.Context, opts PipelineOpts, cfg config.Config, cfgPath string) (string, error) {
	switch {
	case opts.PathArg != "":
		expanded, err := config.ExpandHome(opts.PathArg)
		if err != nil {
			return "", err
		}
		return filepath.Abs(expanded)
	case opts.RootFlag != "":
		expanded, err := config.ExpandHome(opts.RootFlag)
		if err != nil {
			return "", err
		}
		return filepath.Abs(expanded)
	case cfg.Root != "":
		return filepath.Abs(cfg.Root)
	default:
		return promptForRoot(ctx, cfgPath, cfg)
	}
}

// reportPipelinePartial is the shared exit-code helper. 0 ok; 2 if
// any per-repo Err is set OR the discovery walk produced any errors.
func reportPipelinePartial(repos []repo.Repo, walkErrors int) error {
	repoErrs := 0
	for _, r := range repos {
		if r.Err != "" {
			repoErrs++
		}
	}
	if repoErrs == 0 && walkErrors == 0 {
		return nil
	}
	return &PartialError{Repos: repoErrs, Walk: walkErrors}
}

