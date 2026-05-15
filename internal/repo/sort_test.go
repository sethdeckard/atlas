package repo_test

import (
	"testing"
	"time"

	"github.com/sethdeckard/atlas/internal/repo"
)

func mkRepo(name, path string, lastCommit *time.Time) repo.Repo {
	return repo.Repo{Name: name, Path: path, LastCommitAt: lastCommit}
}

func TestSort_LastCommitDescNilLast(t *testing.T) {
	t1 := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	rs := []repo.Repo{
		mkRepo("alpha", "/a", &t2),
		mkRepo("beta", "/b", nil),
		mkRepo("gamma", "/g", &t1),
	}
	repo.Sort(rs, "last_commit_at", true, "")
	want := []string{"gamma", "alpha", "beta"}
	for i, w := range want {
		if rs[i].Name != w {
			t.Errorf("desc[%d] = %q; want %q", i, rs[i].Name, w)
		}
	}
}

func TestSort_LastCommitAscNilLast(t *testing.T) {
	t1 := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	rs := []repo.Repo{
		mkRepo("alpha", "/a", &t2),
		mkRepo("beta", "/b", nil),
		mkRepo("gamma", "/g", &t1),
	}
	repo.Sort(rs, "last_commit_at", false, "")
	// Asc: oldest first; nil still last.
	want := []string{"alpha", "gamma", "beta"}
	for i, w := range want {
		if rs[i].Name != w {
			t.Errorf("asc[%d] = %q; want %q", i, rs[i].Name, w)
		}
	}
}

func TestSort_NameTiebreakStaysAscending(t *testing.T) {
	// Two repos with identical LastCommitAt: name tiebreak is always
	// ascending regardless of primary direction.
	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	rs := []repo.Repo{
		mkRepo("zoo", "/z", &now),
		mkRepo("alpha", "/a", &now),
	}
	repo.Sort(rs, "last_commit_at", true, "")
	if rs[0].Name != "alpha" || rs[1].Name != "zoo" {
		t.Errorf("desc primary tie: got %s,%s; want alpha,zoo", rs[0].Name, rs[1].Name)
	}
	repo.Sort(rs, "last_commit_at", false, "")
	if rs[0].Name != "alpha" || rs[1].Name != "zoo" {
		t.Errorf("asc primary tie: got %s,%s; want alpha,zoo", rs[0].Name, rs[1].Name)
	}
}

// TestSort_ByRepo covers the unified "repo" sort: it orders by
// DisplayPath (the visible REPO column), case-insensitive, with
// abs Path as the deterministic tiebreak.
func TestSort_ByRepo(t *testing.T) {
	root := "/root"

	t.Run("ascending mixes nested and root-level by display string", func(t *testing.T) {
		rs := []repo.Repo{
			mkRepo("zoo", "/root/zoo", nil),         // displays as "zoo"
			mkRepo("api", "/root/work/api", nil),    // displays as "work/api"
			mkRepo("api", "/root/backend/api", nil), // displays as "backend/api"
		}
		repo.Sort(rs, "repo", false, root)
		want := []string{"/root/backend/api", "/root/work/api", "/root/zoo"}
		for i, w := range want {
			if rs[i].Path != w {
				t.Errorf("asc[%d] = %q; want %q", i, rs[i].Path, w)
			}
		}
	})

	t.Run("descending flips primary order", func(t *testing.T) {
		rs := []repo.Repo{
			mkRepo("alpha", "/root/alpha", nil),
			mkRepo("zoo", "/root/zoo", nil),
		}
		repo.Sort(rs, "repo", true, root)
		if rs[0].Name != "zoo" {
			t.Errorf("desc first = %q; want zoo", rs[0].Name)
		}
	})

	t.Run("case-insensitive comparison", func(t *testing.T) {
		rs := []repo.Repo{
			mkRepo("Zeta", "/root/Zeta", nil),
			mkRepo("alpha", "/root/alpha", nil),
		}
		repo.Sort(rs, "repo", false, root)
		if rs[0].Name != "alpha" {
			t.Errorf("case-insensitive asc: first = %q; want alpha", rs[0].Name)
		}
	})

	t.Run("abs path tiebreak when display strings collide", func(t *testing.T) {
		// Two repos with the same parent-dir name and repo name render to
		// the same display string ("api/foo"); abs path is the stable
		// tiebreak.
		rs := []repo.Repo{
			mkRepo("foo", "/root/work/api/foo", nil),
			mkRepo("foo", "/root/api/foo", nil),
		}
		repo.Sort(rs, "repo", false, root)
		// Lex-sort of abs path: /root/api/foo < /root/work/api/foo
		if rs[0].Path != "/root/api/foo" {
			t.Errorf("tiebreak asc: first = %q; want /root/api/foo", rs[0].Path)
		}
	})
}
