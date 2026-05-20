package repo

import "time"

// AnnotateDerived stamps each repo in `repos` with the M4 transient
// fields (WorktreeCount, ActivityTier, Stale) plus the worktree-relative
// signals (LaggingWorktree, PrimaryWorktree, WorktreeHasLaggingChild).
// All are derived from already-persisted values + config, so they're
// recomputed on every launch rather than persisted to cache — this lets
// a `stale_days` config edit reclassify repos without forcing a cold
// rebuild.
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

	groupKey := func(r *Repo) string {
		// Defensive: a repo with no CommonGitDir is its own group.
		if r.CommonGitDir == "" {
			return r.Path
		}
		return r.CommonGitDir
	}

	// Pass 1: per-project aggregates keyed by CommonGitDir. A solo repo
	// gets count=1; a primary plus N linked worktrees all get
	// count=N+1 (each entry in the cache is its own row, but they share
	// a project). freshest tracks the most recent LastCommitAt across
	// the project so the relative-lag check has a baseline.
	counts := make(map[string]int, len(repos))
	freshest := make(map[string]*time.Time, len(repos))
	for i := range repos {
		r := &repos[i]
		key := groupKey(r)
		counts[key]++
		if r.LastCommitAt != nil {
			if cur, ok := freshest[key]; !ok || cur == nil || r.LastCommitAt.After(*cur) {
				freshest[key] = r.LastCommitAt
			}
		}
	}

	// Pass 2: stamp per-repo fields, including the relative-lag check
	// against the project's freshest worktree.
	for i := range repos {
		r := &repos[i]
		key := groupKey(r)
		r.WorktreeCount = counts[key]
		r.ActivityTier = ClassifyActivity(r.LastCommitAt, now, staleDays)
		r.Stale = r.LastCommitAt != nil && now.Sub(*r.LastCommitAt) >= staleCutoff
		r.PrimaryWorktree = r.PrimaryWorktreePath != "" && r.Path == r.PrimaryWorktreePath

		// Relative lag only makes sense within a multi-worktree
		// project that has at least one dated commit somewhere.
		r.LaggingWorktree = false
		if r.WorktreeCount > 1 {
			if top := freshest[key]; top != nil {
				if r.LastCommitAt == nil || top.Sub(*r.LastCommitAt) >= staleCutoff {
					r.LaggingWorktree = true
				}
			}
		}
	}

	// Pass 3: roll a project's *lagging* children up onto its primary
	// row so a forgotten worktree stays discoverable from the anchor
	// even when it's scrolled off-screen. Only relative-lag counts
	// here, not absolute Stale: a uniformly-old project (every
	// worktree stale, no one behind anyone) means "this project is
	// cold," not "you forgot something" — flagging the anchor with
	// ⊘ would mislead. The primary itself doesn't count either; its
	// own state is visible on its own row.
	hasLagging := make(map[string]bool, len(counts))
	for i := range repos {
		r := &repos[i]
		if r.WorktreeCount > 1 && !r.PrimaryWorktree && r.LaggingWorktree {
			hasLagging[groupKey(r)] = true
		}
	}
	for i := range repos {
		r := &repos[i]
		if r.PrimaryWorktree && r.WorktreeCount > 1 {
			r.WorktreeHasLaggingChild = hasLagging[groupKey(r)]
		}
	}
}
