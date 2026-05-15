package repo

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sethdeckard/atlas/internal/git"
)

// Read produces a Repo for the given path. Per-record failures (permission
// denied, malformed git layout, transient errors) are recorded in Repo.Err
// rather than returned as a hard error — callers always get a Repo back.
//
// ctx cancellation is treated the same way: it shows up in Err so the cache
// can decide whether to keep stale data. Read never returns an error value.
func Read(ctx context.Context, path string) Repo {
	abs, err := filepath.Abs(path)
	if err != nil {
		return Repo{
			Path:    path,
			Name:    filepath.Base(path),
			RelPath: relHome(path),
			Err:     fmt.Sprintf("abs path: %v", err),
		}
	}

	r := Repo{
		Path:    abs,
		Name:    deriveName(abs),
		RelPath: relHome(abs),
	}

	paths, err := git.ResolvePaths(abs)
	if err != nil {
		r.Err = err.Error()
		return r
	}

	r.Kind = kindFromPaths(paths)
	r.GitDir = paths.GitDir
	r.CommonGitDir = paths.CommonDir
	r.PrimaryWorktreePath = paths.PrimaryWorktreePath
	if r.Kind == KindBare {
		// For a bare repo, the "name" looks better with the .git suffix
		// stripped if present.
		r.Name = strings.TrimSuffix(filepath.Base(abs), ".git")
	}

	var problems []string

	// HEAD: branch, sha, detached.
	if branch, sha, detached, err := git.Head(ctx, paths); err != nil {
		problems = append(problems, fmt.Sprintf("head: %v", err))
	} else {
		r.Branch = branch
		r.HeadSHA = sha
		r.DetachedHead = detached
	}

	// Status: dirty, untrackedOnly. Skipped for bare.
	if !paths.Bare {
		if dirty, untrackedOnly, err := git.Status(ctx, paths); err != nil {
			problems = append(problems, fmt.Sprintf("status: %v", err))
		} else {
			r.Dirty = dirty
			r.UntrackedOnly = untrackedOnly
		}
	}

	// LastCommit.
	if t, err := git.LastCommit(ctx, paths); err != nil {
		problems = append(problems, fmt.Sprintf("last commit: %v", err))
	} else {
		r.LastCommitAt = t
	}

	// OriginURL.
	if url, err := git.OriginURL(ctx, paths); err != nil {
		problems = append(problems, fmt.Sprintf("origin url: %v", err))
	} else {
		r.OriginURL = url
	}

	// DefaultBranch.
	if def, err := git.DefaultBranch(ctx, paths); err != nil {
		problems = append(problems, fmt.Sprintf("default branch: %v", err))
	} else {
		r.DefaultBranch = def
	}

	// M4 derived signals — appended after the M1 reads so a failure here
	// can't blank out existing fields.
	r.Languages = DetectLanguages(paths.WorktreePath)
	if upstream, err := git.ResolveUpstream(ctx, paths); err != nil {
		problems = append(problems, fmt.Sprintf("upstream: %v", err))
		r.UpstreamRef = ""
	} else {
		r.UpstreamRef = upstream
	}
	// Pass the already-resolved upstream so BehindAheadFor doesn't pay
	// for a second `git rev-parse --symbolic-full-name @{u}` shellout.
	if behind, ahead, err := git.BehindAheadFor(ctx, paths, r.UpstreamRef); err != nil {
		problems = append(problems, fmt.Sprintf("behind/ahead: %v", err))
		r.BehindOrigin = -1
		r.AheadOrigin = -1
	} else {
		r.BehindOrigin = behind
		r.AheadOrigin = ahead
	}
	if n, err := git.StashCount(ctx, paths); err != nil {
		problems = append(problems, fmt.Sprintf("stash count: %v", err))
	} else {
		r.StashCount = n
	}
	if n, err := git.BranchCount(ctx, paths); err != nil {
		problems = append(problems, fmt.Sprintf("branch count: %v", err))
	} else {
		r.BranchCount = n
	}
	if n, err := git.CommitsLast30d(ctx, paths); err != nil {
		problems = append(problems, fmt.Sprintf("commits last 30d: %v", err))
	} else {
		r.CommitsLast30d = n
	}

	// Mtime fingerprints — best effort. Missing files leave zero time, which
	// compares stably across runs. We only stamp fingerprints for bucket
	// 1 fields (BehindOrigin/AheadOrigin/UpstreamRef — see the Repo
	// struct doc); bucket 2 fields (Languages/StashCount/BranchCount/
	// CommitsLast30d) don't need fingerprinting because the warm-path
	// status pass always recomputes them.
	r.HeadMtime = mtimeOrZero(filepath.Join(paths.GitDir, "HEAD"))
	r.IndexMtime = mtimeOrZero(filepath.Join(paths.GitDir, "index"))
	r.ConfigMtime = mtimeOrZero(filepath.Join(paths.CommonDir, "config"))
	r.RefsRemotesMtime = mtimeOrZero(filepath.Join(paths.CommonDir, "refs", "remotes"))
	r.PackedRefsMtime = mtimeOrZero(filepath.Join(paths.CommonDir, "packed-refs"))
	if r.UpstreamRef != "" {
		r.UpstreamRefMtime = mtimeOrZero(filepath.Join(paths.CommonDir, r.UpstreamRef))
	}

	if len(problems) > 0 {
		r.Err = strings.Join(problems, "; ")
	}
	return r
}

func kindFromPaths(p git.Paths) Kind {
	switch {
	case p.Bare:
		return KindBare
	case p.GitDir != p.CommonDir:
		return KindWorktree
	default:
		return KindRepo
	}
}

func mtimeOrZero(path string) time.Time {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return time.Time{}
		}
		return time.Time{}
	}
	return info.ModTime().UTC()
}

func deriveName(path string) string {
	base := filepath.Base(path)
	if base == "." || base == "/" || base == "" {
		return path
	}
	return base
}

func relHome(path string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	if path == home {
		return "~"
	}
	if rel := strings.TrimPrefix(path, home+string(filepath.Separator)); rel != path {
		return "~" + string(filepath.Separator) + rel
	}
	return path
}
