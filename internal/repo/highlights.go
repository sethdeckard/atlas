package repo

import "fmt"

// Highlights returns the human-readable labels for the signals that make
// `r` interesting. The same wording is consumed by the table-flag
// glyphs, the status-bar count, the M5 detail-pane "Highlights" line,
// and M6's export markdown — one source of truth so the four surfaces
// can never drift apart.
//
// A repo with no notable signals returns an empty slice — the caller
// renders nothing rather than e.g. "(none)".
//
// Rules:
//   - "dirty" or "untracked" — surfaces uncommitted work.
//   - "N commits ahead" / "N commits behind" — only when an upstream
//     exists AND the count is non-zero. A no-upstream repo is not
//     automatically interesting (callers concerned about no-upstream
//     repos should look at OriginURL instead).
//   - "N stash" / "N stashes" — surfaces forgotten WIP.
//   - "stale" — drives the ▲ flag; flipped by repo.AnnotateDerived
//     based on cfg.StaleDays.
//   - "linked worktree" — only when a project has more than one
//     worktree (this row is one of N).
//   - "no origin" — surfaces a repo that's never been pushed anywhere.
//   - "problem" — when Repo.Err is set.
//
// Stale is already pre-computed on r (transient via
// repo.AnnotateDerived), so the helper takes only `r` — no config
// dependency, which keeps internal/repo from importing internal/config.
func Highlights(r Repo) []string {
	out := make([]string, 0, 4)
	if r.Err != "" {
		out = append(out, "problem")
	}
	if r.Dirty {
		if r.UntrackedOnly {
			out = append(out, "untracked")
		} else {
			out = append(out, "dirty")
		}
	}
	if r.AheadOrigin > 0 {
		out = append(out, fmt.Sprintf("%d commits ahead", r.AheadOrigin))
	}
	if r.BehindOrigin > 0 {
		out = append(out, fmt.Sprintf("%d commits behind", r.BehindOrigin))
	}
	if r.StashCount > 0 {
		out = append(out, pluralize(r.StashCount, "stash", "stashes"))
	}
	if r.Stale {
		out = append(out, "stale")
	}
	if r.WorktreeCount > 1 {
		out = append(out, "linked worktree")
	}
	if r.OriginURL == "" && r.Kind != KindBare {
		out = append(out, "no origin")
	}
	return out
}

func pluralize(n int, singular, plural string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, singular)
	}
	return fmt.Sprintf("%d %s", n, plural)
}
