// Package cache reads and writes atlas's on-disk JSON cache. The schema is a
// global, abs-path-keyed map of repos; CLI/TUI filter by the active root at
// read time so multiple roots share one cache.
package cache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/sethdeckard/atlas/internal/atomicfile"
	"github.com/sethdeckard/atlas/internal/repo"
)

// CurrentVersion is the schema version atlas writes today. A cache file with
// a different version is treated as missing (drop and rebuild) so old/new
// binaries don't corrupt each other's state.
//
// Version history:
//   1 — M1 initial schema.
//   2 — E.0 cleanup: dropped Repo.Meta + Repo.MetaMtime when curated metadata
//       was removed from the design.
//   3 — M4 derived signals: added Languages/BehindOrigin/AheadOrigin/
//       StashCount/BranchCount/CommitsLast30d/UpstreamRef plus four new
//       CommonDir-relative mtime fingerprints (RefsHeads/RefsStash/
//       RefsRemotes/UpstreamRef/PackedRefs).
const CurrentVersion = 3

// Cache is the on-disk shape. The map is keyed by absolute repo or worktree
// path; values are the most recently read snapshot.
//
// Session is the TUI's last-applied sort/group state. It's optional and
// purely additive — the JSON `omitempty` keeps old binaries (which
// don't know about it) parsing without surprises, and a fresh cache
// just starts with Session == nil. Adding it does not warrant a Version
// bump.
type Cache struct {
	Version int                  `json:"version"`
	Session *Session             `json:"session,omitempty"`
	Repos   map[string]repo.Repo `json:"repos"`
}

// Session captures the user's last-applied TUI preferences so the next
// launch can resume them. Empty fields fall back to the user's
// config.toml defaults.
type Session struct {
	SortBy    string `json:"sort_by,omitempty"`
	SortOrder string `json:"sort_order,omitempty"` // asc | desc
	GroupBy   string `json:"group_by,omitempty"`
}

// Reader is the per-repo reader signature consumed by Refresh. Matches
// repo.Read so callers can pass it directly.
type Reader func(ctx context.Context, path string) repo.Repo

// StatusUpdater is the signature consumed by RefreshStatus. It takes a
// cached Repo and returns it with at least Dirty/UntrackedOnly updated to
// reflect the current worktree state. Other fields should be preserved.
type StatusUpdater func(ctx context.Context, r repo.Repo) repo.Repo

// New returns an empty Cache at the current schema version.
func New() *Cache {
	return &Cache{
		Version: CurrentVersion,
		Repos:   make(map[string]repo.Repo),
	}
}

// DefaultPath returns the path atlas reads/writes by default. $XDG_CACHE_HOME
// is honored when set; otherwise we use ~/.cache/atlas/cache.json on every
// OS — atlas is XDG-style end-to-end and deliberately ignores
// os.UserCacheDir, which on macOS returns ~/Library/Caches.
func DefaultPath() (string, error) {
	if xdg := os.Getenv("XDG_CACHE_HOME"); xdg != "" {
		return filepath.Join(xdg, "atlas", "cache.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate home dir: %w", err)
	}
	return filepath.Join(home, ".cache", "atlas", "cache.json"), nil
}

// Load reads a cache file. A missing file is not an error — Load returns an
// empty cache. A schema-version mismatch is treated the same way (drop and
// rebuild).
func Load(path string) (*Cache, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return New(), nil
		}
		return nil, fmt.Errorf("read cache: %w", err)
	}
	var c Cache
	if err := json.Unmarshal(data, &c); err != nil {
		// Corrupt cache — start fresh rather than fail. Cache loss is
		// always recoverable; refusing to launch is not.
		return New(), nil
	}
	if c.Version != CurrentVersion {
		return New(), nil
	}
	if c.Repos == nil {
		c.Repos = make(map[string]repo.Repo)
	}
	return &c, nil
}

// Snapshot returns a deep-copy of the cache safe to hand to a goroutine
// (e.g. an async Save) while the original is still being mutated by other
// callers. The Repos map is copied; Repo values are themselves value types
// so the copy is fully independent. Session is also cloned so concurrent
// TUI mutations to the live cache don't race the snapshotted marshal.
func (c *Cache) Snapshot() *Cache {
	if c == nil {
		return nil
	}
	out := &Cache{
		Version: c.Version,
		Repos:   make(map[string]repo.Repo, len(c.Repos)),
	}
	if c.Session != nil {
		clone := *c.Session
		out.Session = &clone
	}
	for k, v := range c.Repos {
		out.Repos[k] = v
	}
	return out
}

// Save writes the cache atomically (tempfile in the same dir, then rename).
func Save(path string, c *Cache) error {
	if c == nil {
		return errors.New("cannot save nil cache")
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal cache: %w", err)
	}
	return atomicfile.Write(path, data, atomicfile.Options{
		TempPattern: "cache-*.json",
		MkdirParent: true,
	})
}

// Validate inspects cache entries scoped to root and reports which ones need
// refreshing (stale: in cache + mtime mismatch, OR in paths but not cache),
// and which should be dropped (gone: in cache but not in paths).
//
// Out-of-scope entries (cache entries not rooted under root) are ignored.
// This is what makes a global cache safe across multiple roots — a stale
// entry from another subtree doesn't get pruned just because it isn't under
// the current scan.
func Validate(c *Cache, root string, paths []string) (stale, gone []string) {
	if c == nil {
		return paths, nil
	}
	pathSet := make(map[string]struct{}, len(paths))
	for _, p := range paths {
		pathSet[p] = struct{}{}
	}

	for cachedPath, cached := range c.Repos {
		if !pathUnderRoot(cachedPath, root) {
			continue
		}
		if _, ok := pathSet[cachedPath]; !ok {
			gone = append(gone, cachedPath)
			continue
		}
		if isStale(cached) {
			stale = append(stale, cachedPath)
		}
	}

	for _, p := range paths {
		if _, ok := c.Repos[p]; !ok {
			stale = append(stale, p)
		}
	}
	return stale, gone
}

func pathUnderRoot(path, root string) bool {
	return repo.PathUnderRoot(path, root)
}

// Reconcile classifies discovered paths against c and prunes
// gone-from-disk entries from c.Repos. The classification is the same
// the CLI pipeline and TUI both need on every discover:
//
//   - pathsToRead: not in cache yet OR mtime fingerprints say stale —
//     caller should run a full repo.Read on these.
//   - statusOnly:  already cached and fresh — caller only needs to run
//     repo.UpdateStatus to catch worktree edits the fingerprints miss.
//
// fresh=true forces every discovered path into pathsToRead but does
// NOT skip gone-pruning — otherwise `atlas refresh --fresh` would keep
// deleted repos in the cache across runs.
//
// The cache map is mutated (gone entries are deleted) so callers don't
// have to repeat the loop. Single-writer invariant: callers must not
// be racing other writers to c.Repos.
func (c *Cache) Reconcile(root string, paths []string, fresh bool) (pathsToRead, statusOnly []string) {
	stale, gone := Validate(c, root, paths)
	for _, p := range gone {
		delete(c.Repos, p)
	}
	if fresh {
		return paths, nil
	}
	staleSet := make(map[string]struct{}, len(stale))
	for _, p := range stale {
		staleSet[p] = struct{}{}
	}
	for _, p := range paths {
		if _, isStale := staleSet[p]; isStale {
			continue
		}
		if _, ok := c.Repos[p]; ok {
			statusOnly = append(statusOnly, p)
		}
	}
	return stale, statusOnly
}

func isStale(r repo.Repo) bool {
	gitDir, commonDir := fingerprintDirs(r)
	if mtimeChanged(filepath.Join(gitDir, "HEAD"), r.HeadMtime) ||
		mtimeChanged(filepath.Join(gitDir, "index"), r.IndexMtime) ||
		mtimeChanged(filepath.Join(commonDir, "config"), r.ConfigMtime) {
		return true
	}
	// M4 fingerprints exist only to invalidate the *persisted* upstream
	// fields (BehindOrigin / AheadOrigin / UpstreamRef). Transient
	// fields (Languages / StashCount / BranchCount / CommitsLast30d)
	// don't need fingerprinting — they're recomputed every load via
	// repo.UpdateStatus.
	if mtimeChanged(filepath.Join(commonDir, "refs", "remotes"), r.RefsRemotesMtime) ||
		mtimeChanged(filepath.Join(commonDir, "packed-refs"), r.PackedRefsMtime) {
		return true
	}
	// Upstream ref: the resolved path is stored on the Repo so we don't
	// have to re-shellout `@{u}` on every validate. When UpstreamRef is
	// empty (no upstream) the fingerprint is skipped — it's noise. If
	// the loose ref file is gone (got packed), UpstreamRefMtime is zero
	// by design and PackedRefsMtime above catches the relevant change.
	if r.UpstreamRef != "" {
		if mtimeChanged(filepath.Join(commonDir, r.UpstreamRef), r.UpstreamRefMtime) {
			return true
		}
	}
	return false
}

// fingerprintDirs reads the gitdir + common dir off Repo, falling back to
// path-relative defaults for cache entries written before those fields
// existed. Path layout knowledge stays in internal/git via repo.Read.
func fingerprintDirs(r repo.Repo) (gitDir, commonDir string) {
	gitDir = r.GitDir
	commonDir = r.CommonGitDir
	if gitDir == "" {
		if r.Kind == repo.KindBare {
			gitDir = r.Path
		} else {
			gitDir = filepath.Join(r.Path, ".git")
		}
	}
	if commonDir == "" {
		commonDir = gitDir
	}
	return gitDir, commonDir
}

func mtimeChanged(path string, want time.Time) bool {
	info, err := os.Stat(path)
	if err != nil {
		// Missing file — only "changed" if we previously had a non-zero mtime.
		return !want.IsZero()
	}
	return !info.ModTime().UTC().Equal(want)
}

// Refresh fans out reader calls across a worker pool and streams the results
// on the returned channel. The channel is closed once every path has been
// processed (or ctx is cancelled). Callers drain the channel and write into
// the cache themselves — the cache map is never written to from inside this
// package, which keeps it single-writer-safe.
func Refresh(ctx context.Context, paths []string, reader Reader, workers int) <-chan repo.Repo {
	return runWorkerPool(ctx, paths, reader, workers)
}

// RefreshStatus fans a status-only updater across cached entries and streams
// the updated Repos out. Used to catch dirty/untracked changes that the
// stat-based mtime fingerprints miss (plain worktree edits don't bump
// .git/HEAD, .git/index, or .git/config).
//
// Mirrors Refresh's worker-pool + single-writer-caller pattern.
func RefreshStatus(ctx context.Context, cached []repo.Repo, updater StatusUpdater, workers int) <-chan repo.Repo {
	return runWorkerPool(ctx, cached, updater, workers)
}

// runWorkerPool is the shared engine behind Refresh and RefreshStatus. It
// fans `items` across `workers` goroutines, calling `work` on each and
// streaming the results on the returned channel. The channel is closed when
// every item has been processed or ctx is cancelled.
func runWorkerPool[In any, Out any](ctx context.Context, items []In, work func(context.Context, In) Out, workers int) <-chan Out {
	if workers <= 0 {
		workers = 8
	}
	if workers > len(items) && len(items) > 0 {
		workers = len(items)
	}

	out := make(chan Out, workers)
	if len(items) == 0 {
		close(out)
		return out
	}

	in := make(chan In, len(items))
	for _, item := range items {
		in <- item
	}
	close(in)

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range in {
				select {
				case <-ctx.Done():
					return
				default:
				}
				result := work(ctx, item)
				select {
				case out <- result:
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	go func() {
		wg.Wait()
		close(out)
	}()
	return out
}
