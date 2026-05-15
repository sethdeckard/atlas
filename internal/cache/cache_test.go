package cache_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sethdeckard/atlas/internal/cache"
	"github.com/sethdeckard/atlas/internal/gitfixture"
	"github.com/sethdeckard/atlas/internal/repo"
)

func TestLoad_MissingFile(t *testing.T) {
	c, err := cache.Load(filepath.Join(t.TempDir(), "does-not-exist.json"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c == nil {
		t.Fatal("expected non-nil cache")
	}
	if len(c.Repos) != 0 {
		t.Errorf("expected empty repos map; got %d entries", len(c.Repos))
	}
}

func TestSaveLoad_Roundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cache.json")

	now := time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)
	c := cache.New()
	c.Repos["/abs/path/foo"] = repo.Repo{
		Name:         "foo",
		Path:         "/abs/path/foo",
		Kind:         repo.KindRepo,
		Branch:       "main",
		LastCommitAt: &now,
	}

	if err := cache.Save(path, c); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := cache.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Version != cache.CurrentVersion {
		t.Errorf("expected cache version %d; got %d", cache.CurrentVersion, loaded.Version)
	}
	if len(loaded.Repos) != 1 {
		t.Fatalf("expected 1 repo; got %d", len(loaded.Repos))
	}
	got := loaded.Repos["/abs/path/foo"]
	if got.Branch != "main" || got.Kind != repo.KindRepo {
		t.Errorf("roundtripped repo mismatch: %+v", got)
	}
}

// TestSaveLoad_SessionRoundtrip guards the sticky-session contract:
// a populated Session round-trips through Save/Load so the next TUI
// launch can resume the user's last sort/group state.
func TestSaveLoad_SessionRoundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cache.json")

	c := cache.New()
	c.Session = &cache.Session{
		SortBy:    "repo",
		SortOrder: "asc",
		GroupBy:   "top_dir",
	}

	if err := cache.Save(path, c); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := cache.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Session == nil {
		t.Fatalf("expected Session after roundtrip; got nil")
	}
	if got := *loaded.Session; got != *c.Session {
		t.Errorf("Session roundtrip mismatch: got %+v; want %+v", got, *c.Session)
	}
}

// TestSnapshot_CopiesSession guards against the snapshot-then-marshal
// race: a goroutine marshalling the snapshot must see Session as it
// was at request time, even if the live cache mutates after.
func TestSnapshot_CopiesSession(t *testing.T) {
	c := cache.New()
	c.Session = &cache.Session{SortBy: "repo", GroupBy: "top_dir", SortOrder: "asc"}
	snap := c.Snapshot()

	// Mutate live cache — snapshot must be unaffected.
	c.Session.SortBy = "last_commit_at"
	c.Session.GroupBy = "none"

	if snap.Session == nil {
		t.Fatalf("snapshot Session is nil")
	}
	if snap.Session.SortBy != "repo" || snap.Session.GroupBy != "top_dir" {
		t.Errorf("snapshot Session mutated by live writes: %+v", *snap.Session)
	}
}

// TestLoad_DropsOutdatedVersion ensures that when the on-disk schema is
// older than CurrentVersion, Load returns an empty cache (forcing a cold
// rebuild) rather than leaking stale fields with new field semantics.
// Each prior schema version is tested to make sure none of them sneak
// through.
func TestLoad_DropsOutdatedVersion(t *testing.T) {
	prior := []int{1, 2}
	for _, v := range prior {
		t.Run(fmt.Sprintf("v%d", v), func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "cache.json")
			payload := fmt.Sprintf(`{
				"version": %d,
				"repos": {
					"/abs/path/foo": {"name": "foo", "path": "/abs/path/foo"}
				}
			}`, v)
			if err := os.WriteFile(path, []byte(payload), 0o644); err != nil {
				t.Fatal(err)
			}
			loaded, err := cache.Load(path)
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if len(loaded.Repos) != 0 {
				t.Errorf("expected empty cache after version downgrade; got %d entries", len(loaded.Repos))
			}
			if loaded.Version != cache.CurrentVersion {
				t.Errorf("expected fresh cache at version %d; got %d", cache.CurrentVersion, loaded.Version)
			}
		})
	}
}

func TestSave_AtomicReplacesExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cache.json")
	if err := os.WriteFile(path, []byte("garbage"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := cache.New()
	c.Repos["/x"] = repo.Repo{Path: "/x", Name: "x"}
	if err := cache.Save(path, c); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := cache.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded.Repos) != 1 || loaded.Repos["/x"].Name != "x" {
		t.Errorf("unexpected loaded cache: %+v", loaded)
	}
}

func TestValidate_ScopesByRoot(t *testing.T) {
	c := cache.New()
	c.Repos["/root-a/foo"] = repo.Repo{Path: "/root-a/foo"}
	c.Repos["/root-b/bar"] = repo.Repo{Path: "/root-b/bar"} // out of scope

	stale, gone := cache.Validate(c, "/root-a", []string{"/root-a/foo"})
	// /root-a/foo is in cache and in paths, so neither stale nor gone (as
	// long as fingerprints don't mismatch — for this synthetic entry all
	// mtimes are zero and the on-disk paths don't exist, so isStale returns
	// false because want is also zero).
	if len(stale) != 0 {
		t.Errorf("expected no stale; got %v", stale)
	}
	if len(gone) != 0 {
		t.Errorf("expected no gone (out-of-scope ignored); got %v", gone)
	}
}

func TestValidate_DetectsGoneAndNew(t *testing.T) {
	c := cache.New()
	c.Repos["/root/old"] = repo.Repo{Path: "/root/old"}
	stale, gone := cache.Validate(c, "/root", []string{"/root/new"})
	if len(stale) != 1 || stale[0] != "/root/new" {
		t.Errorf("stale = %v; want [/root/new]", stale)
	}
	if len(gone) != 1 || gone[0] != "/root/old" {
		t.Errorf("gone = %v; want [/root/old]", gone)
	}
}

func TestValidate_MtimeChangeMarksStale(t *testing.T) {
	repoDir := gitfixture.Repo(t)
	r := repo.Read(context.Background(), repoDir)
	c := cache.New()
	c.Repos[r.Path] = r

	// First Validate: nothing stale (fingerprints match disk).
	stale, _ := cache.Validate(c, r.Path, []string{r.Path})
	if len(stale) != 0 {
		t.Errorf("first Validate stale = %v; want []", stale)
	}

	// Touch HEAD to bump its mtime past the cached value.
	headPath := filepath.Join(r.Path, ".git", "HEAD")
	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(headPath, future, future); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	stale, _ = cache.Validate(c, r.Path, []string{r.Path})
	if len(stale) != 1 || stale[0] != r.Path {
		t.Errorf("after mtime bump: stale = %v; want [%s]", stale, r.Path)
	}
}

func TestReconcile_ClassifiesAndPrunesGone(t *testing.T) {
	// Use t.TempDir() + filepath.Join so paths use OS-native separators —
	// repo.PathUnderRoot anchors its prefix check on filepath.Separator,
	// so synthetic forward-slash paths get treated as out-of-scope on
	// Windows and gone-pruning would silently no-op.
	root := t.TempDir()
	deadPath := filepath.Join(root, "dead")
	cachedPath := filepath.Join(root, "cached")
	newPath := filepath.Join(root, "new")

	c := cache.New()
	c.Repos[deadPath] = repo.Repo{Path: deadPath}
	c.Repos[cachedPath] = repo.Repo{Path: cachedPath}

	paths := []string{cachedPath, newPath}
	pathsToRead, statusOnly := c.Reconcile(root, paths, false)

	// newPath is uncached → full read. cachedPath's fingerprint is zero
	// against a non-existent on-disk repo so isStale returns false →
	// status-only. deadPath is in cache + under root + not in paths → gone.
	gotRead := stringSet(pathsToRead)
	gotStatus := stringSet(statusOnly)
	if _, ok := gotRead[newPath]; !ok {
		t.Errorf("expected %s in pathsToRead; got %v", newPath, pathsToRead)
	}
	if _, ok := gotStatus[cachedPath]; !ok {
		t.Errorf("expected %s in statusOnly; got %v", cachedPath, statusOnly)
	}
	if _, stillThere := c.Repos[deadPath]; stillThere {
		t.Errorf("expected %s pruned from cache", deadPath)
	}
}

func TestReconcile_FreshForcesEveryPathToRead(t *testing.T) {
	root := t.TempDir()
	knownPath := filepath.Join(root, "known")
	gonePath := filepath.Join(root, "gone")
	newPath := filepath.Join(root, "new")

	c := cache.New()
	c.Repos[knownPath] = repo.Repo{Path: knownPath}
	c.Repos[gonePath] = repo.Repo{Path: gonePath}

	paths := []string{knownPath, newPath}
	pathsToRead, statusOnly := c.Reconcile(root, paths, true)

	if len(pathsToRead) != 2 {
		t.Errorf("fresh: pathsToRead = %v; want both paths", pathsToRead)
	}
	if len(statusOnly) != 0 {
		t.Errorf("fresh: statusOnly = %v; want empty", statusOnly)
	}
	// fresh still prunes gone-from-disk.
	if _, stillThere := c.Repos[gonePath]; stillThere {
		t.Errorf("fresh should still prune gone entries")
	}
}

func stringSet(ss []string) map[string]struct{} {
	out := make(map[string]struct{}, len(ss))
	for _, s := range ss {
		out[s] = struct{}{}
	}
	return out
}

func TestRefresh_StreamsAllPaths(t *testing.T) {
	dir1 := gitfixture.Repo(t)
	dir2 := gitfixture.Repo(t)
	dir3 := gitfixture.Repo(t)

	paths := []string{dir1, dir2, dir3}
	ch := cache.Refresh(context.Background(), paths, repo.Read, 2)

	got := map[string]bool{}
	for r := range ch {
		got[r.Path] = true
	}
	if len(got) != 3 {
		t.Errorf("expected 3 repos streamed; got %d", len(got))
	}
}

// TestRefresh_ContextCancelUnblocksWorkers guards the property the TUI
// relies on: when a refresh's context is cancelled (e.g. because the user
// pressed R and superseded the previous phase), workers must exit instead
// of blocking on a buffer-full out-channel that nobody is reading.
func TestRefresh_ContextCancelUnblocksWorkers(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	// Reader that blocks until ctx is done — simulates a slow git
	// shellout. It returns a zero Repo on cancel, but the key behavior
	// is that runWorkerPool's `out <- result` send respects ctx.Done()
	// even when the model has stopped reading.
	blocking := func(ctx context.Context, p string) repo.Repo {
		<-ctx.Done()
		return repo.Repo{Path: p}
	}

	paths := []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l"}
	ch := cache.Refresh(ctx, paths, blocking, 4)

	// Cancel and confirm the channel closes (workers exit) within a
	// generous timeout. A leak would block this read forever.
	cancel()
	deadline := time.After(2 * time.Second)
	done := make(chan struct{})
	go func() {
		for range ch {
		}
		close(done)
	}()
	select {
	case <-done:
	case <-deadline:
		t.Fatal("workers did not exit after context cancel — leak risk")
	}
}

func TestRefresh_EmptyPaths(t *testing.T) {
	ch := cache.Refresh(context.Background(), nil, repo.Read, 4)
	count := 0
	for range ch {
		count++
	}
	if count != 0 {
		t.Errorf("expected zero values; got %d", count)
	}
}
