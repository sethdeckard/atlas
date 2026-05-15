package tui

import (
	"strings"

	"github.com/sahilm/fuzzy"

	"github.com/sethdeckard/atlas/internal/repo"
)

// filterRepos returns the subset of repos whose REPO column value (the
// thing the user actually sees) fuzzy-matches query. An empty query
// returns the input slice unmodified.
//
// The haystack is `repo.DisplayPath(root, r)` — same string the table
// renders. Earlier we also indexed Languages and the absolute Path,
// but that produced too-permissive matches: typing `go` lit up every
// Ruby repo with a Go neighbor (path-letter collision) and every
// non-Go repo whose languages slice mentioned Go anywhere. Now what
// you see is what you can match.
//
// Fuzzy match is filter-only: we discard the rank scores `sahilm/fuzzy`
// returns and use the result as a set so the caller's existing sort stays
// the primary order. Pressing `s` mid-filter cycles sort within the visible
// rows rather than the rank-by-typo order changing the layout.
func filterRepos(repos []repo.Repo, query, root string) []repo.Repo {
	query = strings.TrimSpace(query)
	if query == "" {
		return repos
	}
	haystack := make([]string, len(repos))
	for i, r := range repos {
		haystack[i] = repo.DisplayPath(root, r)
	}
	matches := fuzzy.Find(query, haystack)
	out := make([]repo.Repo, 0, len(matches))
	keep := make(map[int]struct{}, len(matches))
	for _, m := range matches {
		keep[m.Index] = struct{}{}
	}
	// Preserve input order — caller's sort wins over fuzzy rank.
	for i, r := range repos {
		if _, ok := keep[i]; ok {
			out = append(out, r)
		}
	}
	return out
}
