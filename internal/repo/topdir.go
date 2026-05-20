package repo

import (
	"path/filepath"
	"strings"
)

// TopDir returns the first path component of `path` relative to `root`. Used
// as a grouping key by the TUI's `top_dir` group mode and by the CLI's
// `--top-dir` filter. Returns "" when `path` is at the root or when the
// path can't be made relative.
//
// Example: TopDir("/home/u/projects", "/home/u/projects/go/atlas")
// → "go".
func TopDir(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." {
		return ""
	}
	parts := strings.Split(rel, string(filepath.Separator))
	if len(parts) <= 1 {
		return ""
	}
	return parts[0]
}

// DisplayPath returns the relative path the table renders in the REPO
// column: `parent/name` (the immediate parent dir + repo name) for repos
// nested under root, or just `name` for repos sitting directly at root.
// Sort, filter, and grouping all use this so the visible label, sort
// order, and group keys agree.
//
// We use the immediate parent rather than the top-level dir under root so
// the column stays informative when root is high-up. With root=~ a repo
// at ~/projects/go/atlas displays as "go/atlas" (its actual neighborhood)
// instead of "projects/atlas" (which would be uniform-and-uninformative
// for everything under ~/projects). Use TopDir for grouping/filtering by
// the top-level category — those callers want the root-relative top dir
// directly.
func DisplayPath(root string, r Repo) string {
	rel, err := filepath.Rel(root, r.Path)
	if err != nil || rel == "." {
		return r.Name
	}
	parts := strings.Split(rel, string(filepath.Separator))
	if len(parts) <= 1 {
		return r.Name
	}
	return parts[len(parts)-2] + string(filepath.Separator) + r.Name
}

// ProjectLabel returns a human-readable name for the project a worktree
// belongs to. It's used only by the TUI's `worktree` grouping mode when
// a project's primary checkout can't be resolved among the scoped repos
// (primary outside the active root, or a bare-backed setup) and the
// cluster needs a synthetic header instead of a subtree root.
//
// Preference order:
//  1. The primary worktree's display path, when PrimaryWorktreePath is
//     known — the most recognizable name for the project.
//  2. The basename of the directory holding CommonGitDir, with a
//     trailing ".git" stripped (covers `/x/proj/.git` → "proj" and
//     bare `/x/proj.git` → "proj").
//  3. This row's own DisplayPath, as a last resort so the header is
//     never empty.
func ProjectLabel(root string, r Repo) string {
	if r.PrimaryWorktreePath != "" {
		rel, err := filepath.Rel(root, r.PrimaryWorktreePath)
		// A primary outside the active root (the common no-primary
		// case — that's *why* there's no subtree root in scope) makes
		// Rel return a "../…" escape; that's not a usable label, so
		// fall back to the project's own basename.
		if err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
			parts := strings.Split(rel, string(filepath.Separator))
			if len(parts) >= 2 {
				return parts[len(parts)-2] + string(filepath.Separator) + parts[len(parts)-1]
			}
			return parts[len(parts)-1]
		}
		return filepath.Base(r.PrimaryWorktreePath)
	}
	if r.CommonGitDir != "" {
		dir := filepath.Dir(r.CommonGitDir)
		base := filepath.Base(r.CommonGitDir)
		if base == ".git" {
			return filepath.Base(dir)
		}
		return strings.TrimSuffix(base, ".git")
	}
	return DisplayPath(root, r)
}

// PathUnderRoot reports whether `path` is `root` itself or a descendant
// of `root`. The check uses filepath.Separator so it works on Windows
// (`\`) as well as Unix (`/`); naive `root + "/"` checks miss the
// Windows case and report nested cached repos as out-of-scope.
//
// This is the canonical "under-root" predicate — cache.Validate,
// cli.scopedRepos, and refresh's pre-snapshot all agree on the same
// semantics by going through this one function.
//
// Sibling-prefix safety: a path like `/rootless/x` is correctly
// reported as NOT under `/root` because the separator-anchored prefix
// is `/root/`, not `/root`.
func PathUnderRoot(path, root string) bool {
	if path == root {
		return true
	}
	return strings.HasPrefix(path, root+string(filepath.Separator))
}
