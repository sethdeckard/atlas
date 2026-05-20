// Package repo defines the Repo aggregate that the rest of atlas operates on
// (cache entries, CLI rows, TUI table rows). The reader that populates it
// from disk + git lives in reader.go.
package repo

import "time"

// Kind classifies a discovered git location.
type Kind int

const (
	KindUnknown Kind = iota
	KindRepo
	KindWorktree
	KindBare
)

// String renders Kind for display in tables and JSON.
func (k Kind) String() string {
	switch k {
	case KindRepo:
		return "repo"
	case KindWorktree:
		return "worktree"
	case KindBare:
		return "bare"
	default:
		return "unknown"
	}
}

// MarshalJSON encodes Kind as its display string so cache.json stays
// human-readable across schema versions.
func (k Kind) MarshalJSON() ([]byte, error) {
	return []byte(`"` + k.String() + `"`), nil
}

// UnmarshalJSON accepts either the display string or the legacy integer form.
func (k *Kind) UnmarshalJSON(data []byte) error {
	s := string(data)
	switch s {
	case `"repo"`:
		*k = KindRepo
	case `"worktree"`:
		*k = KindWorktree
	case `"bare"`:
		*k = KindBare
	case `"unknown"`, `null`, `""`:
		*k = KindUnknown
	default:
		// Best-effort: treat any unrecognized payload as Unknown rather than
		// failing the whole cache load.
		*k = KindUnknown
	}
	return nil
}

// Repo is the metadata snapshot of a single repository / worktree / bare
// repo. The reader produces it; the cache stores it; the CLI/TUI render it.
//
// Cache keys are absolute worktree paths (or the bare-repo path), so each
// linked worktree of a project gets its own entry. CommonGitDir is the shared
// project-identity key — equal across all worktrees of one project — and is
// what M3 grouping keys on.
//
// atlas is observability-only: every field is derived from local git or
// filesystem state. There is no per-repo curated metadata.
type Repo struct {
	Name                string     `json:"name"`
	Path                string     `json:"path"`            // absolute worktree path (or bare repo path)
	RelPath             string     `json:"rel_path"`        // ~-relative for display
	Kind                Kind       `json:"kind"`
	Branch              string     `json:"branch"`          // "" if detached or bare
	DetachedHead        bool       `json:"detached_head"`
	HeadSHA             string     `json:"head_sha"`        // short SHA
	Dirty               bool       `json:"dirty"`
	UntrackedOnly       bool       `json:"untracked_only"`  // dirty but only ?? entries
	LastCommitAt        *time.Time `json:"last_commit_at,omitempty"`
	OriginURL           string     `json:"origin_url"`
	DefaultBranch       string     `json:"default_branch"`
	GitDir              string     `json:"git_dir"`               // resolved gitdir (per-worktree for worktrees, equal to CommonGitDir for normal/bare)
	CommonGitDir        string     `json:"common_git_dir"`        // project-identity key
	PrimaryWorktreePath string     `json:"primary_worktree_path"` // when known
	Err                 string     `json:"err,omitempty"`         // per-record read failure

	// M4 derived signals.
	//
	// Three design buckets:
	//
	//   1. Persisted + git-fingerprinted: BehindOrigin, AheadOrigin,
	//      UpstreamRef. These depend only on git refs, and the
	//      RefsRemotes/UpstreamRef/PackedRefs fingerprints below catch
	//      every change that affects them.
	//
	//   2. Persisted as last-known: Languages, StashCount, BranchCount,
	//      CommitsLast30d. These don't fit the mtime-fingerprint model
	//      (filesystem manifests, nested refs, wall-clock drift), so
	//      the warm path always recomputes them via repo.UpdateStatus.
	//      They're still persisted so `atlas list --cached` — which by
	//      contract skips the warm pass for speed — has *some* value to
	//      render. Stale-by-design on --cached, fresh on the warm path.
	//
	//   3. Transient (json:"-"), recomputed every launch:
	//      ActivityTier/Stale/WorktreeCount plus the worktree-relative
	//      signals below. These are pure functions of LastCommitAt +
	//      cfg.StaleDays + now (plus the scoped repo set for the
	//      worktree signals), so persisting them would just create
	//      cache-vs-config drift. Filled by repo.AnnotateDerived.
	BehindOrigin   int      `json:"behind_origin"`            // commits behind upstream; -1 if no upstream
	AheadOrigin    int      `json:"ahead_origin"`             // commits ahead of upstream; -1 if no upstream
	UpstreamRef    string   `json:"upstream_ref,omitempty"`   // resolved upstream ref path under CommonDir, e.g. "refs/remotes/origin/main"
	Languages      []string `json:"languages,omitempty"`      // bucket 2: last-known
	StashCount     int      `json:"stash_count"`              // bucket 2: last-known
	BranchCount    int      `json:"branch_count"`             // bucket 2: last-known
	CommitsLast30d int      `json:"commits_last_30d"`         // bucket 2: last-known

	ActivityTier  string `json:"-"` // "recent"|"active"|"cold"|"dormant"|"empty"
	Stale         bool   `json:"-"`
	WorktreeCount int    `json:"-"`

	// Worktree-relative transient signals (bucket 3). Only meaningful
	// when WorktreeCount > 1; computed by AnnotateDerived over the
	// scoped repo set, so "lagging" is judged against the worktrees
	// atlas can currently see (same scoping WorktreeCount already has).
	//
	// LaggingWorktree: this worktree's LastCommitAt is >= stale_days
	// behind the project's freshest worktree (or it has no commits
	// while a sibling does). The "you forgot this checkout" signal,
	// independent of the absolute Stale flag.
	//
	// PrimaryWorktree: this row is the project's primary checkout
	// (Path == PrimaryWorktreePath). The subtree root in the TUI
	// worktree grouping mode; false for every solo repo.
	//
	// WorktreeHasLaggingChild: set on the primary row when any other
	// worktree in its project is LaggingWorktree. Absolute-stale
	// children that aren't relatively lagging don't count — a
	// uniformly-old project means "this project is cold," not "you
	// forgot something." Drives the rolled-up ⊘ on the anchor.
	LaggingWorktree         bool `json:"-"`
	PrimaryWorktree         bool `json:"-"`
	WorktreeHasLaggingChild bool `json:"-"`

	// Mtime fingerprints for cache invalidation. Zero values are stable
	// for comparison when a file is missing. These fingerprints exist
	// only to invalidate bucket-1 fields (BehindOrigin / AheadOrigin /
	// UpstreamRef). Bucket-2 fields don't need fingerprinting because
	// the warm-path status pass always recomputes them; bucket-3
	// transient fields are pure functions of bucket-1 + config and
	// never persisted.
	HeadMtime        time.Time `json:"head_mtime"`
	IndexMtime       time.Time `json:"index_mtime"`
	ConfigMtime      time.Time `json:"config_mtime"`
	RefsRemotesMtime time.Time `json:"refs_remotes_mtime"`
	UpstreamRefMtime time.Time `json:"upstream_ref_mtime"`
	PackedRefsMtime  time.Time `json:"packed_refs_mtime"`
}
