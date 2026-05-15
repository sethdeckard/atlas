package cli

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/sethdeckard/atlas/internal/cache"
	"github.com/sethdeckard/atlas/internal/repo"
	"github.com/spf13/cobra"
)

// refreshOptions captures the parsed flags + positional arg of
// `atlas refresh`. Path/Root flow into the shared Pipeline; Verbose
// turns on the per-repo diff output.
type refreshOptions struct {
	Path    string
	Root    string
	Verbose bool
}

// NewRefreshCommand returns the `atlas refresh` cobra subcommand.
func NewRefreshCommand() *cobra.Command {
	opts := &refreshOptions{}
	cmd := &cobra.Command{
		Use:   "refresh [PATH]",
		Short: "Force a full re-read of every repo into the cache",
		Long: `Force a full re-read of every repo under the resolved root into
the cache. Equivalent to running list with --fresh, but writes
nothing to stdout in default mode — meant for cron / launchd or
post-import paranoia. With --verbose, prints one line per repo
whose post-refresh state differs from the prior cached snapshot.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				opts.Path = args[0]
			}
			return runRefresh(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), opts)
		},
	}
	cmd.Flags().StringVar(&opts.Root, "root", "", "scan root (overridden by positional PATH; falls back to config)")
	cmd.Flags().BoolVarP(&opts.Verbose, "verbose", "v", false, "list repos whose state changed during the refresh")
	return cmd
}

func runRefresh(ctx context.Context, stdout io.Writer, stderr io.Writer, opts *refreshOptions) error {
	if ctx == nil {
		ctx = context.Background()
	}
	pipe, err := NewPipeline(ctx, PipelineOpts{
		PathArg:  opts.Path,
		RootFlag: opts.Root,
		Fresh:    true,
	}, stderr)
	if err != nil {
		return err
	}

	// Snapshot the pre-refresh state so --verbose can diff. Captures
	// only the scoped subset to keep the comparison focused on what
	// this run actually touched.
	pre := snapshotByPath(pipe.Cache, pipe.Root)

	scoped, walkErr := pipe.Run(ctx)
	if walkErr != nil {
		fmt.Fprintf(stderr, "atlas: scan completed with errors: %v\n", walkErr)
	}

	if opts.Verbose {
		writeRefreshDiffs(stdout, pre, scoped)
	}
	if err := pipe.Save(); err != nil {
		fmt.Fprintf(stderr, "atlas: cache save failed: %v\n", err)
	}
	return reportPipelinePartial(scoped, pipe.WalkErrors())
}

// snapshotByPath copies repos under root from the cache, indexed by
// path, so we have a stable "before" view to diff against. Returns
// values, not pointers — the post-refresh comparison reads fields,
// it never compares object identity. Uses repo.PathUnderRoot so the
// scoping semantics match cache.Validate and cli.scopedRepos exactly
// (and so the helper is separator-aware on Windows).
func snapshotByPath(c *cache.Cache, root string) map[string]repo.Repo {
	out := make(map[string]repo.Repo, len(c.Repos))
	for path, r := range c.Repos {
		if repo.PathUnderRoot(path, root) {
			out[path] = r
		}
	}
	return out
}

// writeRefreshDiffs prints one line per changed repo. Output is
// sorted by path for stable test goldens. Only the most user-visible
// transitions are surfaced — adding every M4 numeric counter to the
// diff would make the output noisy on routine refreshes.
func writeRefreshDiffs(out io.Writer, pre map[string]repo.Repo, post []repo.Repo) {
	postByPath := make(map[string]repo.Repo, len(post))
	for _, r := range post {
		postByPath[r.Path] = r
	}

	// Collect every path mentioned in either snapshot.
	pathSet := make(map[string]struct{}, len(pre)+len(post))
	for p := range pre {
		pathSet[p] = struct{}{}
	}
	for p := range postByPath {
		pathSet[p] = struct{}{}
	}
	paths := make([]string, 0, len(pathSet))
	for p := range pathSet {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	for _, path := range paths {
		preRepo, hadPre := pre[path]
		postRepo, hasPost := postByPath[path]
		switch {
		case !hadPre:
			fmt.Fprintf(out, "%s: (new)\n", path)
		case !hasPost:
			fmt.Fprintf(out, "%s: (removed)\n", path)
		default:
			diffs := diffRepoForVerbose(preRepo, postRepo)
			if len(diffs) > 0 {
				fmt.Fprintf(out, "%s: %s\n", path, strings.Join(diffs, ", "))
			}
		}
	}
}

// diffRepoForVerbose returns human-readable transition labels between
// pre and post snapshots of the same repo. Empty slice means "no
// user-visible change."
func diffRepoForVerbose(pre, post repo.Repo) []string {
	var out []string
	if pre.Dirty != post.Dirty {
		if post.Dirty {
			out = append(out, "dirty +")
		} else {
			out = append(out, "dirty -")
		}
	}
	if pre.Branch != post.Branch {
		out = append(out, fmt.Sprintf("branch %s → %s", branchOrDash(pre.Branch), branchOrDash(post.Branch)))
	}
	if !sameLastCommit(pre.LastCommitAt, post.LastCommitAt) {
		out = append(out, "last_commit_at +")
	}
	if pre.AheadOrigin != post.AheadOrigin && post.AheadOrigin >= 0 {
		out = append(out, fmt.Sprintf("ahead %d → %d", maxIntZero(pre.AheadOrigin), post.AheadOrigin))
	}
	if pre.BehindOrigin != post.BehindOrigin && post.BehindOrigin >= 0 {
		out = append(out, fmt.Sprintf("behind %d → %d", maxIntZero(pre.BehindOrigin), post.BehindOrigin))
	}
	if pre.StashCount != post.StashCount {
		out = append(out, fmt.Sprintf("stashes %d → %d", pre.StashCount, post.StashCount))
	}
	if pre.BranchCount != post.BranchCount {
		out = append(out, fmt.Sprintf("branches %d → %d", pre.BranchCount, post.BranchCount))
	}
	if !sameLanguages(pre.Languages, post.Languages) {
		out = append(out, fmt.Sprintf("languages %v → %v", pre.Languages, post.Languages))
	}
	return out
}

func branchOrDash(b string) string {
	if b == "" {
		return "—"
	}
	return b
}

// sameLastCommit treats both nil as equal, both non-nil and equal as
// equal, and any other combination as a change. Used so the verbose
// diff doesn't print a noisy "last_commit_at +" line for every empty
// repo on every refresh.
func sameLastCommit(a, b *time.Time) bool {
	switch {
	case a == nil && b == nil:
		return true
	case a == nil || b == nil:
		return false
	default:
		return a.Equal(*b)
	}
}

// maxIntZero returns n clamped to non-negative — used so the diff
// doesn't render the "no upstream" sentinel (-1) as "ahead -1 → 2"
// when an upstream is freshly added.
func maxIntZero(n int) int {
	if n < 0 {
		return 0
	}
	return n
}

// sameLanguages compares two language slices by content and order.
// Detection is deterministic (precedence-ordered), so any reordering
// implies a real change worth surfacing.
func sameLanguages(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
