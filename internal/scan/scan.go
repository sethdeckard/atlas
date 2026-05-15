// Package scan walks a filesystem tree and emits the absolute paths of every
// git repository (normal, worktree, or bare) it finds. Symlinks are never
// followed, the first `.git` wins (so submodules and nested repos don't
// double-count), and a hardcoded skip set keeps the walk fast on real trees.
package scan

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// BuiltinSkipDirs is the always-applied default skip list. Entries are
// interpreted by syntax:
//
//   - No path separator:  basename match — skip any directory with this
//     name, anywhere in the walk.
//   - Starts with "~/":   home-anchored — expanded to $HOME/... and skipped
//     at exactly that path.
//   - Starts with "/":    absolute path — skipped at exactly that path.
//
// User config replaces this list when skip_dirs is set; while skip_dirs is
// commented out (or absent), this is the authoritative default.
//
// Grouped by ecosystem rather than alphabetized — keep new entries in the
// most natural section.
var BuiltinSkipDirs = []string{
	// Per-project artifacts
	"node_modules",
	"vendor",
	"target",
	"build",
	"__pycache__",
	".venv",
	"venv",
	".direnv",
	".bundle",
	"Pods",
	"Carthage",
	"DerivedData",
	".cache",
	".Trash",

	// Toolchain / package caches (often contain real .git checkouts as deps)
	".cargo",
	".rustup",
	".gem",
	".npm",
	".yarn",
	".pnpm-store",
	".m2",
	".gradle",
	".ivy2",
	".sbt",
	".nuget",
	".cocoapods",
	".swiftpm",
	".pub-cache",

	// Editor / shell / AI-tool config dirs (plugin/skill managers clone as .git)
	".vim",
	".emacs.d",
	".tmux",
	".oh-my-zsh",
	".claude",
	".codex",

	// Language runtime version managers
	".rbenv",
	".pyenv",
	".nvm",
	".fnm",
	".nodenv",
	".volta",
	".goenv",
	".jenv",
	".sdkman",
	".asdf",
	".mise",
	".tfenv",
	".tgenv",

	// XDG
	".local",

	// JS framework / tooling caches
	".next",
	".nuxt",
	".svelte-kit",
	".turbo",
	".angular",
	".parcel-cache",
	"bower_components",

	// Python tool caches
	".mypy_cache",
	".pytest_cache",
	".ruff_cache",
	".tox",

	// Dart / mobile
	".dart_tool",
	".expo",

	// .NET
	"obj",

	// Infra
	".terraform",
	".terragrunt-cache",

	// macOS user folders (home-anchored — only skipped at $HOME/<name>)
	"~/Library",
	"~/Applications",
	"~/Pictures",
	"~/Movies",
	"~/Music",
}

// Options controls a Discover walk. SkipBaseNames matches directory
// basenames anywhere; SkipAbsPaths matches resolved absolute paths
// exactly. Config layer parses BuiltinSkipDirs (or the user's override)
// into these two sets via config.parseSkipEntries. MaxDepth of 0 disables
// the limit.
type Options struct {
	SkipBaseNames map[string]struct{}
	SkipAbsPaths  map[string]struct{}
	MaxDepth      int
}

// Discover walks root and returns absolute paths to every git repository
// found. Walk errors (permission denied on a subdirectory, broken symlink,
// etc.) are collected and returned alongside the slice — they don't abort
// the scan. The returned error is nil on full success and a joined error
// otherwise; callers that just want to know "did anything go wrong" can
// check err != nil.
//
// ctx cancellation is honored at every entry: if the parent ctx is
// cancelled (e.g. SIGINT during `atlas | cat`), the walk terminates early
// and ctx.Err() is included in the returned error.
func Discover(ctx context.Context, root string, opts Options) ([]string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}

	var (
		repos    []string
		walkErrs []error
	)

	rootDepth := strings.Count(abs, string(filepath.Separator))

	walkErr := filepath.WalkDir(abs, func(path string, d fs.DirEntry, err error) error {
		// Cancellation check first: if the user has signalled shutdown,
		// stop the walk before doing any further work.
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		if err != nil {
			// Don't abort on permission denied or transient errors — record
			// and skip into the offending entry.
			walkErrs = append(walkErrs, err)
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}

		// Skip symlinks — never follow.
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}

		if !d.IsDir() {
			return nil
		}

		// Depth check (relative to root).
		if opts.MaxDepth > 0 {
			depth := strings.Count(path, string(filepath.Separator)) - rootDepth
			if depth > opts.MaxDepth {
				return fs.SkipDir
			}
		}

		// Skip-dir check, but always allow the root itself.
		if path != abs {
			if _, skipped := opts.SkipBaseNames[d.Name()]; skipped {
				return fs.SkipDir
			}
			if len(opts.SkipAbsPaths) > 0 {
				if _, skipped := opts.SkipAbsPaths[path]; skipped {
					return fs.SkipDir
				}
			}
		}

		// Bare repo? Path basename ends in .git AND has HEAD/objects/refs.
		if strings.HasSuffix(d.Name(), ".git") && hasBareLayout(path) {
			repos = append(repos, path)
			return fs.SkipDir
		}

		// Normal repo / worktree? `.git` (file or dir) inside path.
		if hasGitMarker(path) {
			repos = append(repos, path)
			return fs.SkipDir
		}

		return nil
	})

	if walkErr != nil {
		walkErrs = append(walkErrs, walkErr)
	}
	if len(walkErrs) > 0 {
		return repos, errors.Join(walkErrs...)
	}
	return repos, nil
}

func hasGitMarker(dir string) bool {
	info, err := os.Lstat(filepath.Join(dir, ".git"))
	if err != nil {
		return false
	}
	// A regular file (worktree) or a directory both count.
	return info.Mode().IsRegular() || info.IsDir()
}

func hasBareLayout(dir string) bool {
	for _, name := range []string{"HEAD", "objects", "refs"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			return false
		}
	}
	return true
}
