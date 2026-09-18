package tui

import "github.com/sethdeckard/atlas/internal/repo"

// folds is the per-rebuild bookkeeping produced by collapseWorktrees. It
// is view state, not domain state: the counts are post-filter and change
// on every keystroke, which is why they don't live on repo.Repo (whose
// WorktreeCount is pre-filter, project-wide, and counts the row itself).
//
// The zero value is usable. Reads from nil maps return the zero value,
// so a model that has never collapsed anything needs no initialization.
type folds struct {
	count   map[string]int    // anchor path -> siblings folded away ((+N) badge)
	into    map[string]string // folded-away path -> anchor path (selection reseat)
	lagging map[string]bool   // anchor path -> some folded sibling is lagging

	// anyNested records that a multi-member cluster survived unfolded,
	// which happens when the anchor itself was filtered out. Those rows
	// still draw tree connectors in the `worktree` grouping mode, so
	// chooseColumns has to keep paying for the indent.
	anyNested bool
}

// worktreeAnchors elects one anchor path per cluster from the pre-filter
// scoped set, so anchor identity doesn't flicker while the user types a
// filter. Preference order:
//
//  1. The project's primary checkout (PrimaryWorktree).
//  2. The bare repository row itself. Bare-backed projects have no
//     primary at all, because git.ResolvePaths only fills
//     PrimaryWorktreePath when the common dir is a `.git` directory.
//     The bare row is the project and carries the project's own name,
//     so folding into it reads better than folding into a worktree.
//  3. The first member in the active sort order, whatever that order
//     is: newest under last_commit_at descending, oldest when
//     reversed, alphabetically first under repo ascending, last when
//     descending. repo.Sort's comparator is total (it tiebreaks on
//     Path), so the choice is deterministic. Following the sort is
//     intended, because it gives the project the table position that
//     sort implies. Re-electing on a sort flip is safe because the
//     caller reseats selection through folds.into on the same rebuild.
func worktreeAnchors(scoped []repo.Repo) map[string]string {
	type candidate struct {
		path string
		tier int // lower wins
	}
	best := make(map[string]candidate, 8)
	for _, r := range scoped {
		if r.WorktreeCount <= 1 || r.CommonGitDir == "" {
			continue
		}
		tier := 2
		switch {
		case r.PrimaryWorktree:
			tier = 0
		case r.Kind == repo.KindBare:
			tier = 1
		}
		k := worktreeClusterKey(r)
		// First writer wins within a tier, which preserves sort order
		// for tier 3 and keeps the choice stable for the others.
		if cur, ok := best[k]; !ok || tier < cur.tier {
			best[k] = candidate{path: r.Path, tier: tier}
		}
	}
	out := make(map[string]string, len(best))
	for k, c := range best {
		out[k] = c.path
	}
	return out
}

// collapseWorktrees folds each project's linked worktrees into a single
// anchor row. visible is the post-filter, sorted row set being rendered;
// scoped is the pre-filter set the anchors are elected from.
//
// A cluster folds only when it has at least two visible members AND the
// elected anchor is one of them. Matching children therefore remain
// unfolded when their elected anchor does not match the filter; when it
// does match, they fold into it like any other sibling. Promoting the
// first *visible* member instead would, under a filter matching two
// children but not the parent, hide one match behind another match that
// doesn't represent it.
//
// Output preserves input order, so the caller's sort still decides where
// each surviving row lands.
func collapseWorktrees(visible, scoped []repo.Repo) ([]repo.Repo, folds) {
	anchors := worktreeAnchors(scoped)

	size := make(map[string]int, 8)
	anchorVisible := make(map[string]bool, 8)
	for _, r := range visible {
		k := worktreeClusterKey(r)
		size[k]++
		if anchors[k] == r.Path {
			anchorVisible[k] = true
		}
	}

	f := folds{
		count:   make(map[string]int),
		into:    make(map[string]string),
		lagging: make(map[string]bool),
	}
	out := make([]repo.Repo, 0, len(visible))
	for _, r := range visible {
		k := worktreeClusterKey(r)
		switch {
		case size[k] <= 1 || !anchorVisible[k]:
			if size[k] > 1 {
				f.anyNested = true
			}
			out = append(out, r)
		case r.Path == anchors[k]:
			out = append(out, r)
			f.count[r.Path] = size[k] - 1
		default:
			f.into[r.Path] = anchors[k]
			if r.LaggingWorktree {
				// The anchor's own lag is already on its own row via
				// flagString; this tracks only what folding hid.
				f.lagging[anchors[k]] = true
			}
		}
	}
	return out, f
}

// indexOfPath returns the index of the repo with the given path, or -1.
func indexOfPath(rs []repo.Repo, path string) int {
	for i, r := range rs {
		if r.Path == path {
			return i
		}
	}
	return -1
}
