// Package cli wires atlas's cobra subcommands. The list command is the real
// entry point in M1; cmd/atlas/main.go delegates the no-subcommand case
// here too.
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/sethdeckard/atlas/internal/cache"
	"github.com/sethdeckard/atlas/internal/repo"
	"github.com/sethdeckard/atlas/internal/termsafe"
	"github.com/spf13/cobra"
)

// PartialError signals a successful list run that surfaced per-record
// failures. main.go translates this to exit code 2 so shell pipelines can
// distinguish "ran cleanly" from "ran but some repos errored".
type PartialError struct {
	Repos int
	Walk  int
}

func (e *PartialError) Error() string {
	return fmt.Sprintf("%d repo error(s), %d walk error(s)", e.Repos, e.Walk)
}

// listOptions captures the parsed flags + positional arg of `atlas list`.
type listOptions struct {
	Path     string
	Root     string
	Dirty    bool
	TopDir   string
	Language string
	Limit    int
	Sort     string
	Reverse  bool
	Format   string
	Cached   bool
	Fresh    bool
}

// nowFunc is the clock used for relative-time formatting and the stale-day
// check. Tests substitute it via SetNowFunc for determinism.
var nowFunc func() time.Time = time.Now

// SetNowFunc replaces the package's clock. Tests use this to pin relative
// time formatting; production code should not call it.
func SetNowFunc(f func() time.Time) {
	if f == nil {
		nowFunc = time.Now
		return
	}
	nowFunc = f
}

// NewListCommand returns the `atlas list` cobra subcommand.
func NewListCommand() *cobra.Command {
	opts := &listOptions{}

	cmd := &cobra.Command{
		Use:   "list [PATH]",
		Short: "List Git repositories under the configured root",
		Long: `List Git repositories under the resolved root. The table format
is the default; --format=name emits one path per line for shell
pipelines (xargs, while-read), and --format=json emits the full
repo record.

The cache is read first and only stale entries are re-validated.
Use --fresh to force a full re-read or --cached to skip git
entirely and trust the existing cache. Filters (--dirty, --top-dir,
--language) compose; sort defaults to last_commit_at descending.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				opts.Path = args[0]
			}
			return runList(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), opts)
		},
	}

	cmd.Flags().StringVar(&opts.Root, "root", "", "scan root (overridden by positional PATH; falls back to config)")
	cmd.Flags().BoolVar(&opts.Dirty, "dirty", false, "show only repos with uncommitted changes")
	cmd.Flags().StringVar(&opts.TopDir, "top-dir", "", "show only repos whose top-level dir under root matches")
	cmd.Flags().StringVar(&opts.Language, "language", "", "show only repos whose detected languages contain this value (case-insensitive)")
	cmd.Flags().IntVar(&opts.Limit, "limit", 0, "maximum number of repos to show (0 = no limit)")
	cmd.Flags().StringVar(&opts.Sort, "sort", "", "sort key: repo | last_commit_at (default: last_commit_at)")
	cmd.Flags().BoolVar(&opts.Reverse, "reverse", false, "reverse sort order")
	cmd.Flags().StringVar(&opts.Format, "format", "table", "output format: table | name | json")
	cmd.Flags().BoolVar(&opts.Cached, "cached", false, "read cache only, no discovery or git")
	cmd.Flags().BoolVar(&opts.Fresh, "fresh", false, "bypass cache validation and re-read every repo")

	return cmd
}

func runList(ctx context.Context, stdout, stderr io.Writer, opts *listOptions) error {
	if ctx == nil {
		ctx = context.Background()
	}

	pipe, err := NewPipeline(ctx, PipelineOpts{
		PathArg:       opts.Path,
		RootFlag:      opts.Root,
		UseCachedOnly: opts.Cached,
		Fresh:         opts.Fresh,
	}, stderr)
	if err != nil {
		return err
	}

	scoped, walkErr := pipe.Run(ctx)
	if walkErr != nil {
		fmt.Fprintf(stderr, "atlas: scan completed with errors: %v\n", walkErr)
	}
	repos := applyFiltersSortLimit(scoped, opts, pipe.Root)
	if err := render(stdout, repos, opts.Format, pipe.Root); err != nil {
		return err
	}
	if err := pipe.Save(); err != nil {
		fmt.Fprintf(stderr, "atlas: cache save failed: %v\n", err)
	}
	return reportPipelinePartial(repos, pipe.WalkErrors())
}

func assertDirectory(root string) error {
	info, err := os.Stat(root)
	if err != nil {
		return fmt.Errorf("root %s: %w", root, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("root %s is not a directory", root)
	}
	return nil
}

// scopedRepos returns the cache entries whose key is rooted under root.
func scopedRepos(c *cache.Cache, root string) []repo.Repo {
	var rs []repo.Repo
	for path, r := range c.Repos {
		if repo.PathUnderRoot(path, root) {
			rs = append(rs, r)
		}
	}
	return rs
}

// runFullRefresh streams full repo.Read calls into the cache.
func runFullRefresh(ctx context.Context, c *cache.Cache, paths []string) {
	if len(paths) == 0 {
		return
	}
	ch := cache.Refresh(ctx, paths, repo.Read, 8)
	for r := range ch {
		c.Repos[r.Path] = r
	}
}

// runStatusRefresh runs the lightweight status pass against cached entries
// at the given paths and writes the updated repos back to the cache.
func runStatusRefresh(ctx context.Context, c *cache.Cache, paths []string) {
	if len(paths) == 0 {
		return
	}
	cached := make([]repo.Repo, 0, len(paths))
	for _, p := range paths {
		if r, ok := c.Repos[p]; ok {
			cached = append(cached, r)
		}
	}
	ch := cache.RefreshStatus(ctx, cached, repo.UpdateStatus, 8)
	for r := range ch {
		c.Repos[r.Path] = r
	}
}

// ---- filter / sort / render helpers ----

func applyFiltersSortLimit(repos []repo.Repo, opts *listOptions, root string) []repo.Repo {
	wantLang := strings.ToLower(opts.Language)
	out := repos[:0:0]
	for _, r := range repos {
		if opts.Dirty && !r.Dirty {
			continue
		}
		if opts.TopDir != "" && topDir(root, r.Path) != opts.TopDir {
			continue
		}
		if wantLang != "" && !hasLanguage(r.Languages, wantLang) {
			continue
		}
		out = append(out, r)
	}

	sortBy := opts.Sort
	if sortBy == "" {
		sortBy = "last_commit_at"
	}
	descending := !opts.Reverse
	repo.Sort(out, sortBy, descending, root)

	if opts.Limit > 0 && len(out) > opts.Limit {
		out = out[:opts.Limit]
	}
	return out
}

// topDir is a local alias for repo.TopDir kept to minimize churn in the
// many call sites in this file.
func topDir(root, path string) string {
	return repo.TopDir(root, path)
}

func hasLanguage(langs []string, want string) bool {
	for _, l := range langs {
		if strings.EqualFold(l, want) {
			return true
		}
	}
	return false
}

func render(w io.Writer, repos []repo.Repo, format, root string) error {
	switch format {
	case "json":
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(repos)
	case "name":
		// Emit paths verbatim. The `name` format is a line-oriented
		// machine target (xargs, while-read pipelines), and rewriting
		// control characters would mean the printed string no longer
		// names an existing path on disk. Use --format=json when you
		// want escaping.
		for _, r := range repos {
			if _, err := fmt.Fprintln(w, r.Path); err != nil {
				return err
			}
		}
		return nil
	case "", "table":
		return renderTable(w, repos, root)
	default:
		return fmt.Errorf("unknown format %q (want: table|name|json)", format)
	}
}

func renderTable(w io.Writer, repos []repo.Repo, root string) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "repo\tbranch\tlast_commit\tflags")
	for _, r := range repos {
		branch := r.Branch
		if r.DetachedHead {
			branch = "(" + r.HeadSHA + ")"
		}
		if branch == "" && r.Kind == repo.KindBare {
			branch = "—"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n",
			termsafe.Sanitize(repo.DisplayPath(root, r)),
			termsafe.Sanitize(branch),
			relativeTime(r.LastCommitAt),
			flagString(r),
		)
	}
	return tw.Flush()
}

func flagString(r repo.Repo) string {
	var b strings.Builder
	if r.Dirty {
		if r.UntrackedOnly {
			b.WriteRune('?')
		} else {
			b.WriteRune('*')
		}
	}
	if r.Stale {
		b.WriteRune('▲')
	}
	if r.Err != "" {
		b.WriteRune('!')
	}
	if r.AheadOrigin > 0 {
		fmt.Fprintf(&b, "↑%d", r.AheadOrigin)
	}
	if r.BehindOrigin > 0 {
		fmt.Fprintf(&b, "↓%d", r.BehindOrigin)
	}
	if r.StashCount > 0 {
		fmt.Fprintf(&b, "≡%d", r.StashCount)
	}
	if b.Len() == 0 {
		return ""
	}
	return b.String()
}

func relativeTime(t *time.Time) string {
	if t == nil {
		return "—"
	}
	now := nowFunc()
	d := now.Sub(*t)
	if d < 0 {
		return "future"
	}
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	case d < 365*24*time.Hour:
		return fmt.Sprintf("%dmo ago", int(d.Hours()/(24*30)))
	default:
		return fmt.Sprintf("%dy ago", int(d.Hours()/(24*365)))
	}
}
