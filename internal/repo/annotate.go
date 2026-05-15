package repo

import "time"

// AnnotateDerived stamps each repo in `repos` with the M4 transient
// fields (WorktreeCount, ActivityTier, Stale). All three are derived
// from already-persisted values + config, so they're recomputed on every
// launch rather than persisted to cache — this lets a `stale_days`
// config edit reclassify repos without forcing a cold rebuild.
//
// The slice is mutated in place. Call sites:
//   - cli pipeline: final step of (*Pipeline).Run, after the worker
//     pool drains.
//   - tui: after every cache mutation (discovered, repoRefreshed,
//     refreshDone).
func AnnotateDerived(repos []Repo, staleDays int, now time.Time) {
	if len(repos) == 0 {
		return
	}
	staleCutoff := time.Duration(staleDays) * 24 * time.Hour

	// Pass 1: count repos sharing each CommonGitDir to derive
	// WorktreeCount. A solo repo gets count=1; a primary plus N linked
	// worktrees all get count=N+1 (each entry in the cache is its own
	// row, but they share a project).
	counts := make(map[string]int, len(repos))
	for _, r := range repos {
		key := r.CommonGitDir
		if key == "" {
			// Defensive: a repo with no CommonGitDir is its own group.
			counts[r.Path]++
			continue
		}
		counts[key]++
	}

	// Pass 2: stamp each repo.
	for i := range repos {
		r := &repos[i]
		key := r.CommonGitDir
		if key == "" {
			key = r.Path
		}
		r.WorktreeCount = counts[key]
		r.ActivityTier = ClassifyActivity(r.LastCommitAt, now, staleDays)
		r.Stale = r.LastCommitAt != nil && now.Sub(*r.LastCommitAt) >= staleCutoff
	}
}
