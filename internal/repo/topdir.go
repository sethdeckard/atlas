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
