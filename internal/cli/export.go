package cli

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/sethdeckard/atlas/internal/atomicfile"
	"github.com/sethdeckard/atlas/internal/repo"
	"github.com/sethdeckard/atlas/internal/sysopen"
	"github.com/sethdeckard/atlas/internal/termsafe"
	"github.com/spf13/cobra"
)

// exportOptions captures the parsed flags + positional arg of
// `atlas export`. Path/Root/Cached/Fresh flow into the shared
// Pipeline; Markdown is the export-specific output destination.
type exportOptions struct {
	Path     string
	Root     string
	Cached   bool
	Fresh    bool
	Markdown string
}

// NewExportCommand returns the `atlas export` cobra subcommand.
func NewExportCommand() *cobra.Command {
	opts := &exportOptions{}
	cmd := &cobra.Command{
		Use:   "export [PATH]",
		Short: "Render the repo set as markdown",
		Long: `Render the repo set under the resolved root as a markdown document
sectioned by activity tier (recent → active → cold → dormant → empty)
and sub-sectioned by top-level directory. Sections with more than 20
repos collapse under <details> for readability.

The output is auto-derived from the same data atlas uses everywhere
else — git state, manifests, upstream divergence — so refreshing the
cache also refreshes future exports.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				opts.Path = args[0]
			}
			return runExport(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), opts)
		},
	}
	cmd.Flags().StringVar(&opts.Root, "root", "", "scan root (overridden by positional PATH; falls back to config)")
	cmd.Flags().BoolVar(&opts.Cached, "cached", false, "read cache only, no discovery or git")
	cmd.Flags().BoolVar(&opts.Fresh, "fresh", false, "bypass cache validation and re-read every repo")
	cmd.Flags().StringVar(&opts.Markdown, "markdown", "", "output path for the rendered markdown (required)")
	_ = cmd.MarkFlagRequired("markdown")
	return cmd
}

func runExport(ctx context.Context, _ io.Writer, stderr io.Writer, opts *exportOptions) error {
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
	// Sort by last commit (newest first) to give a deterministic order
	// within each (activity, top_dir) bucket.
	repo.Sort(scoped, "last_commit_at", true, pipe.Root)

	body := renderMarkdown(scoped, pipe.Root)
	if err := atomicfile.Write(opts.Markdown, []byte(body), atomicfile.Options{
		TempPattern: "atlas-export-*.md",
		MkdirParent: true,
	}); err != nil {
		return fmt.Errorf("write markdown: %w", err)
	}
	if err := pipe.Save(); err != nil {
		fmt.Fprintf(stderr, "atlas: cache save failed: %v\n", err)
	}
	return reportPipelinePartial(scoped, pipe.WalkErrors())
}

// detailsThreshold is the per-section repo count above which the
// section's bullet list is wrapped in <details><summary> for
// readability. Picked to match the plan's H.1 description.
const detailsThreshold = 20

func renderMarkdown(repos []repo.Repo, root string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Repos under %s\n\n", mdEscapeText(root))
	if len(repos) == 0 {
		b.WriteString("_No repositories found._\n")
		return b.String()
	}

	byTier := groupByActivity(repos)
	for _, tier := range orderedTiers(byTier) {
		bucket := byTier[tier]
		title := titleForTier(tier)
		fmt.Fprintf(&b, "## %s (%d)\n\n", title, len(bucket))
		writeTierSections(&b, bucket, root)
		b.WriteByte('\n')
	}
	return b.String()
}

// groupByActivity buckets repos by ActivityTier (with "" → "other").
// Activity must already be annotated by the pipeline.
func groupByActivity(repos []repo.Repo) map[string][]repo.Repo {
	out := make(map[string][]repo.Repo)
	for _, r := range repos {
		t := r.ActivityTier
		if t == "" {
			t = "other"
		}
		out[t] = append(out[t], r)
	}
	return out
}

// orderedTiers returns the bucket keys in canonical order, with any
// unrecognized tier appended at the end so they still appear in
// output (helps catch annotation-pass regressions).
func orderedTiers(byTier map[string][]repo.Repo) []string {
	out := make([]string, 0, len(byTier))
	seen := make(map[string]bool, len(byTier))
	for _, t := range repo.ActivityTierOrder {
		if _, ok := byTier[t]; ok {
			out = append(out, t)
			seen[t] = true
		}
	}
	// Trailing extras, alphabetized for determinism.
	var extras []string
	for t := range byTier {
		if !seen[t] {
			extras = append(extras, t)
		}
	}
	sort.Strings(extras)
	return append(out, extras...)
}

func titleForTier(tier string) string {
	switch tier {
	case "recent":
		return "Recent"
	case "active":
		return "Active"
	case "cold":
		return "Cold"
	case "dormant":
		return "Dormant"
	case "empty":
		return "Empty"
	default:
		return "Other"
	}
}

// writeTierSections renders one activity tier as `### top_dir`
// sub-sections. Sections with > detailsThreshold repos collapse
// under <details><summary>.
func writeTierSections(b *strings.Builder, repos []repo.Repo, root string) {
	byTopDir := make(map[string][]repo.Repo)
	for _, r := range repos {
		td := repo.TopDir(root, r.Path)
		if td == "" {
			td = "(root)"
		}
		byTopDir[td] = append(byTopDir[td], r)
	}
	keys := make([]string, 0, len(byTopDir))
	for k := range byTopDir {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, td := range keys {
		bucket := byTopDir[td]
		fmt.Fprintf(b, "### %s (%d)\n\n", mdEscapeText(td), len(bucket))
		if len(bucket) > detailsThreshold {
			fmt.Fprintf(b, "<details><summary>%d repos</summary>\n\n", len(bucket))
			writeRepoBullets(b, bucket)
			b.WriteString("\n</details>\n\n")
		} else {
			writeRepoBullets(b, bucket)
			b.WriteByte('\n')
		}
	}
}

// writeRepoBullets renders one repo per line with the conventional
// markdown shape:
//
//	- **Name** — `branch` (↑N ↓N) · last 3d ago · languages: go · [origin](url) · _highlights_
//
// Empty / not-applicable segments are elided so the output stays
// terse for clean repos.
func writeRepoBullets(b *strings.Builder, repos []repo.Repo) {
	for _, r := range repos {
		fmt.Fprintf(b, "- **%s**", mdEscapeText(r.Name))
		if branch := branchForBullet(r); branch != "" {
			fmt.Fprintf(b, " — %s", mdEscapeCode(branch))
		}
		if div := divergenceForBullet(r); div != "" {
			fmt.Fprintf(b, " %s", div)
		}
		if r.LastCommitAt != nil {
			fmt.Fprintf(b, " · last %s", relativeTime(r.LastCommitAt))
		}
		if len(r.Languages) > 0 {
			langs := make([]string, len(r.Languages))
			for i, l := range r.Languages {
				langs[i] = mdEscapeText(l)
			}
			fmt.Fprintf(b, " · languages: %s", strings.Join(langs, " "))
		}
		if r.OriginURL != "" {
			// Gate the origin URL through sysopen.BrowserURL — it parses
			// with net/url, requires http(s), rejects userinfo and embedded
			// control chars. The returned form is browser-safe but not yet
			// safe to drop into a [text](url) destination, because net/url
			// leaves `)`, `[`, `(` etc. intact in path segments where they
			// are URL-legal. mdLinkTarget percent-encodes the Markdown
			// delimiters so the link can't be terminated early. Anything
			// that fails BrowserURL renders as inline code instead.
			if safe, err := sysopen.BrowserURL(r.OriginURL); err == nil {
				fmt.Fprintf(b, " · [origin](%s)", mdLinkTarget(safe))
			} else {
				fmt.Fprintf(b, " · origin %s", mdEscapeCode(r.OriginURL))
			}
		}
		if hl := repo.Highlights(r); len(hl) > 0 {
			escaped := make([]string, len(hl))
			for i, h := range hl {
				escaped[i] = mdEscapeText(h)
			}
			fmt.Fprintf(b, " · _%s_", strings.Join(escaped, ", "))
		}
		b.WriteByte('\n')
	}
}

// mdEscapeText backslash-escapes the ASCII punctuation that has
// CommonMark meaning in text contexts. Safe to apply to any
// repo-controlled string before interpolating into headings, bold
// spans, italics, or comma-joined lists.
//
// Control characters (notably '\n' and '\r') are stripped first via
// termsafe.Sanitize: a literal newline inside a heading or bullet
// would terminate the construct and let the next line be parsed as
// a new heading or list item. Punctuation escapes alone aren't
// enough on their own.
//
// strings.NewReplacer does not re-process its own output, so the `\`
// rule does not double-escape itself.
var mdTextReplacer = strings.NewReplacer(
	`\`, `\\`,
	"`", "\\`",
	"*", `\*`,
	"_", `\_`,
	"[", `\[`,
	"]", `\]`,
	"(", `\(`,
	")", `\)`,
	"<", `\<`,
	">", `\>`,
	"#", `\#`,
	"|", `\|`,
	"~", `\~`,
	"!", `\!`,
)

func mdEscapeText(s string) string { return mdTextReplacer.Replace(termsafe.Sanitize(s)) }

// mdLinkTarget percent-encodes the characters that have meaning in a
// CommonMark inline link destination. Even after BrowserURL has
// canonicalized the URL, characters like `)`, `[`, `(`, `<`, `>`,
// backtick, and whitespace remain literal because they're URL-legal
// inside path segments — and an embedded `)` would terminate the
// destination early, letting subsequent text inject additional
// Markdown. Percent-encoding keeps the canonical [text](url) form
// unambiguous regardless of what's in the upstream URL.
func mdLinkTarget(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '(', ')', '[', ']', '<', '>', '`', ' ', '\t', '\n', '\r':
			fmt.Fprintf(&b, "%%%02X", c)
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// mdEscapeCode wraps s in a backtick fence long enough to contain
// any backticks present in s. Per CommonMark, a code span delimited
// by N backticks may contain any run of fewer than N backticks; if
// the value starts or ends with a backtick, one space of padding is
// stripped from each end during rendering.
//
// Like mdEscapeText, control characters are stripped first so an
// embedded newline can't break out of the code span.
func mdEscapeCode(s string) string {
	s = termsafe.Sanitize(s)
	if s == "" {
		return "``"
	}
	longest, cur := 0, 0
	for _, r := range s {
		if r == '`' {
			cur++
			if cur > longest {
				longest = cur
			}
		} else {
			cur = 0
		}
	}
	fence := strings.Repeat("`", longest+1)
	pad := ""
	if strings.HasPrefix(s, "`") || strings.HasSuffix(s, "`") {
		pad = " "
	}
	return fence + pad + s + pad + fence
}

func branchForBullet(r repo.Repo) string {
	if r.DetachedHead {
		return r.HeadSHA
	}
	return r.Branch
}

func divergenceForBullet(r repo.Repo) string {
	if r.AheadOrigin <= 0 && r.BehindOrigin <= 0 {
		return ""
	}
	a, bh := r.AheadOrigin, r.BehindOrigin
	if a < 0 {
		a = 0
	}
	if bh < 0 {
		bh = 0
	}
	return fmt.Sprintf("(↑%d ↓%d)", a, bh)
}

