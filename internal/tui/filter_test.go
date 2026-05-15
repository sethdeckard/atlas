package tui

import (
	"testing"

	"github.com/sethdeckard/atlas/internal/repo"
)

func TestFilterRepos_EmptyQueryReturnsAll(t *testing.T) {
	repos := []repo.Repo{
		{Name: "alpha", Path: "/p/alpha"},
		{Name: "beta", Path: "/p/beta"},
	}
	out := filterRepos(repos, "", "/p")
	if len(out) != 2 {
		t.Errorf("empty query should return all; got %d", len(out))
	}
}

func TestFilterRepos_MatchesByName(t *testing.T) {
	repos := []repo.Repo{
		{Name: "alpha", Path: "/p/alpha"},
		{Name: "atlas", Path: "/p/atlas"},
		{Name: "beta", Path: "/p/beta"},
	}
	out := filterRepos(repos, "atl", "/p")
	if len(out) != 1 || out[0].Name != "atlas" {
		t.Errorf("expected just atlas; got %v", out)
	}
}

// TestFilterRepos_MatchesByTopDir confirms a top-dir prefix in the
// REPO column (e.g. `go/svc-a`) is a valid haystack entry — typing
// `go` matches anything that displays as `go/...`.
func TestFilterRepos_MatchesByTopDir(t *testing.T) {
	repos := []repo.Repo{
		{Name: "svc-a", Path: "/projects/go/svc-a"},
		{Name: "svc-b", Path: "/projects/ruby/svc-b"},
	}
	out := filterRepos(repos, "go", "/projects")
	if len(out) != 1 || out[0].Path != "/projects/go/svc-a" {
		t.Errorf("expected path-matched repo; got %v", out)
	}
}

func TestFilterRepos_PreservesInputOrder(t *testing.T) {
	// fuzzy.Find returns ranked results; filterRepos must discard rank and
	// preserve input order so callers' explicit sort wins.
	repos := []repo.Repo{
		{Name: "zoo-a", Path: "/p/zoo-a"},
		{Name: "alpha", Path: "/p/alpha"},
	}
	out := filterRepos(repos, "a", "/p")
	if len(out) != 2 {
		t.Fatalf("expected both to match; got %v", out)
	}
	if out[0].Name != "zoo-a" || out[1].Name != "alpha" {
		t.Errorf("input order not preserved: %v", out)
	}
}

// TestFilterRepos_LanguageDoesNotLeakIntoHaystack guards the
// haystack-tightening: the earlier implementation indexed the
// detected Languages slice, so a Ruby repo with `go` in Languages
// would match a `go` query. We now index only the visible REPO
// column, so that false positive is gone.
func TestFilterRepos_LanguageDoesNotLeakIntoHaystack(t *testing.T) {
	repos := []repo.Repo{
		{Name: "ruby-svc", Path: "/p/ruby-svc", Languages: []string{"ruby", "go"}},
	}
	out := filterRepos(repos, "go", "/p")
	if len(out) != 0 {
		t.Errorf("expected language tag NOT to match; got %v", out)
	}
}

// TestFilterRepos_AbsPathDoesNotLeakIntoHaystack guards the same
// tightening on the path side: a token that appears only in the
// absolute-path prefix above the scan root shouldn't match every
// repo just because every repo's path shares that prefix.
func TestFilterRepos_AbsPathDoesNotLeakIntoHaystack(t *testing.T) {
	repos := []repo.Repo{
		{Name: "alpha", Path: "/abs/root/alpha"},
	}
	out := filterRepos(repos, "abs", "/abs/root")
	if len(out) != 0 {
		t.Errorf("expected abs-path letters NOT to match; got %v", out)
	}
}

func TestFilterRepos_ZeroMatch(t *testing.T) {
	repos := []repo.Repo{{Name: "alpha", Path: "/p/alpha"}}
	out := filterRepos(repos, "xyz", "/p")
	if len(out) != 0 {
		t.Errorf("expected empty result on zero match; got %v", out)
	}
}
