package repo

import "time"

// ActivityTierOrder is the canonical display order for activity tiers
// (newest activity first). Consumers that bucket by tier — the CLI
// export and the TUI group-by-activity view — render groups in this
// order so the user sees newer work on top regardless of the primary
// sort. Tiers not present in this slice should sort to the end.
var ActivityTierOrder = []string{"recent", "active", "cold", "dormant", "empty"}

// ClassifyActivity returns the activity tier label for a repo based on the
// age of its last commit, the user's configured stale-days threshold, and
// the current time. The mapping:
//
//	nil last commit              → "empty"
//	age < 14 days                → "recent"
//	age < cfg.StaleDays days     → "active"
//	age < 365 days               → "cold"
//	age >= 365 days              → "dormant"
//
// The active/cold boundary is configurable so it stays aligned with the
// `Stale` flag (which uses the same threshold) and with M1's `▲ stale`
// glyph. The 14-day "recent" bucket and 365-day "dormant" cutoff are
// hardcoded because they map to common "this past fortnight" / "this
// past year" mental categories independent of the user's stale
// tolerance.
//
// `now` is taken as an arg so tests don't need clock injection plumbing.
func ClassifyActivity(lastCommit *time.Time, now time.Time, staleDays int) string {
	if lastCommit == nil {
		return "empty"
	}
	age := now.Sub(*lastCommit)
	staleCutoff := time.Duration(staleDays) * 24 * time.Hour
	switch {
	case age < 14*24*time.Hour:
		return "recent"
	case age < staleCutoff:
		return "active"
	case age < 365*24*time.Hour:
		return "cold"
	default:
		return "dormant"
	}
}
