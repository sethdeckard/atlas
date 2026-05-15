package repo

import (
	"slices"
	"strings"
)

// Sort orders rs in place by the given primary key. `descending` flips the
// primary axis without scrambling repos that share a key — equal primaries
// fall through to a stable name (then path) tiebreak that is *always*
// ascending. CLI and TUI both call into this so atlas's display order
// stays consistent across surfaces.
//
// The "last_commit_at" mode treats nil-valued LastCommitAt as sorting last
// regardless of direction (a repo with no commits stays at the bottom).
//
// The "repo" mode sorts by DisplayPath (the visible REPO column value)
// case-insensitively, so the order on screen matches the order the sort
// produces. The absolute Path tiebreaks deterministically when two repos
// at different depths render to the same DisplayPath. `root` is required
// here because DisplayPath is root-relative.
//
// Recognized keys: "repo", "last_commit_at" (default).
func Sort(rs []Repo, by string, descending bool, root string) {
	slices.SortFunc(rs, comparator(by, descending, root))
}

func comparator(by string, descending bool, root string) func(Repo, Repo) int {
	primarySign := 1
	if descending {
		primarySign = -1
	}
	nameTie := func(a, b Repo) int {
		if c := strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)); c != 0 {
			return c
		}
		return strings.Compare(a.Path, b.Path)
	}

	switch by {
	case "repo":
		return func(a, b Repo) int {
			da := strings.ToLower(DisplayPath(root, a))
			db := strings.ToLower(DisplayPath(root, b))
			if c := primarySign * strings.Compare(da, db); c != 0 {
				return c
			}
			return strings.Compare(a.Path, b.Path)
		}
	default: // "last_commit_at" or unrecognized
		return func(a, b Repo) int {
			switch {
			case a.LastCommitAt == nil && b.LastCommitAt == nil:
				return nameTie(a, b)
			case a.LastCommitAt == nil:
				return 1 // nil sorts last regardless of direction
			case b.LastCommitAt == nil:
				return -1
			}
			if c := a.LastCommitAt.Compare(*b.LastCommitAt); c != 0 {
				return primarySign * c
			}
			return nameTie(a, b)
		}
	}
}
