package repo

import (
	"context"

	"github.com/sethdeckard/atlas/internal/git"
)

// UpdateStatus is the lightweight refresh path: it re-runs cheap
// observations the cache doesn't fingerprint and writes the result back
// to a copy of the cached Repo. Fields that don't depend on git or
// filesystem state at this moment (Name, Path, HeadSHA, OriginURL, the
// fingerprint mtimes, etc.) are preserved.
//
// This is the warm-launch counterpart to repo.Read: it runs against
// every cached repo whose mtime fingerprints still match disk, so any
// signal that *can* drift between mtime changes (or between launches)
// has to be refreshed here. Specifically:
//
//   - Dirty / UntrackedOnly: a plain `git status` — the original M2
//     reason this function exists.
//   - Languages: filesystem manifests at the worktree root aren't
//     captured by any tracked git mtime, so adding a go.mod has to be
//     picked up here.
//   - StashCount / BranchCount: refs land in nested namespaces
//     (refs/heads/feature/foo) that the parent dir mtime doesn't
//     reliably capture.
//   - CommitsLast30d: wall-clock-dependent — counts roll over without
//     any git change.
//
// Errors during any sub-step leave the corresponding cached value
// intact rather than zeroing it: a transient `git status` failure
// shouldn't make a clean repo look dirty (or wipe the language list).
// Bare repos skip status/stash/languages but still refresh
// branch/commit counts.
func UpdateStatus(ctx context.Context, r Repo) Repo {
	p, err := git.ResolvePaths(r.Path)
	if err != nil {
		return r
	}

	if !p.Bare {
		if dirty, untrackedOnly, err := git.Status(ctx, p); err == nil {
			r.Dirty = dirty
			r.UntrackedOnly = untrackedOnly
		}
		// Languages depends on the worktree root only — bare repos have
		// no worktree to inspect.
		r.Languages = DetectLanguages(p.WorktreePath)
		if n, err := git.StashCount(ctx, p); err == nil {
			r.StashCount = n
		}
	}
	if n, err := git.BranchCount(ctx, p); err == nil {
		r.BranchCount = n
	}
	if n, err := git.CommitsLast30d(ctx, p); err == nil {
		r.CommitsLast30d = n
	}
	return r
}
