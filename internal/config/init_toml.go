package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// managedKey identifies a Config field whose default value is rendered as a
// commented-out documentation block in the on-disk config (an
// "atlas:default <key> ... atlas:end" region). Keys NOT in this set are
// either always-uncommented (root) or have no baked-in default worth
// surfacing.
type managedKey string

const (
	keyMaxDepth  managedKey = "max_depth"
	keySkipDirs  managedKey = "skip_dirs"
	keyStaleDays managedKey = "stale_days"
	keyTheme     managedKey = "theme"
)

// managedKeys is the order keys are emitted in the rendered config.
var managedKeys = []managedKey{keyMaxDepth, keySkipDirs, keyStaleDays, keyTheme}

const (
	sentinelOpenPrefix = "# atlas:default "
	sentinelOpenSuffix = " — uncomment to override"
	sentinelClose      = "# atlas:end"
)

// skipDirsGroups is the human-readable, grouped rendering of the default
// skip list used inside the commented skip_dirs block. The flattened
// concatenation must equal scan.BuiltinSkipDirs — TestSkipDirsGroupsMatchBuiltin
// guards against drift.
var skipDirsGroups = []struct {
	label   string
	entries []string
}{
	{"Per-project artifacts", []string{
		"node_modules", "vendor", "target", "build",
		"__pycache__", ".venv", "venv", ".direnv", ".bundle",
		"Pods", "Carthage", "DerivedData",
		".cache", ".Trash",
	}},
	{"Toolchain / package caches", []string{
		".cargo", ".rustup",
		".gem",
		".npm", ".yarn", ".pnpm-store",
		".m2", ".gradle", ".ivy2", ".sbt",
		".nuget",
		".cocoapods", ".swiftpm", ".pub-cache",
	}},
	{"Editor / shell / AI-tool config dirs", []string{
		".vim", ".emacs.d", ".tmux", ".oh-my-zsh",
		".claude", ".codex",
	}},
	{"Language runtime version managers", []string{
		".rbenv", ".pyenv",
		".nvm", ".fnm", ".nodenv", ".volta",
		".goenv",
		".jenv", ".sdkman",
		".asdf", ".mise",
		".tfenv", ".tgenv",
	}},
	{"XDG", []string{".local"}},
	{"JS framework / tooling caches", []string{
		".next", ".nuxt", ".svelte-kit", ".turbo", ".angular",
		".parcel-cache", "bower_components",
	}},
	{"Python tool caches", []string{
		".mypy_cache", ".pytest_cache", ".ruff_cache", ".tox",
	}},
	{"Dart / mobile", []string{".dart_tool", ".expo"}},
	{".NET", []string{"obj"}},
	{"Infra", []string{".terraform", ".terragrunt-cache"}},
	{"macOS user folders (home-anchored — only skipped at $HOME/<name>)", []string{
		"~/Library", "~/Applications",
		"~/Pictures", "~/Movies", "~/Music",
	}},
}

// WriteInitTOML renders cfg via RenderInitTOML and writes the result to
// path, creating parent dirs as needed. Atomic via tempfile + rename.
// This is the fresh-config write path used by `atlas init` and onboarding;
// for re-persisting an already-edited config, use Save.
func WriteInitTOML(path string, cfg, defaults Config) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir config dir: %w", err)
	}
	return atomicWrite(path, RenderInitTOML(cfg, defaults))
}

// RenderInitTOML produces a fresh config file body from c. Defaultable keys
// whose value matches defaults are emitted as commented-out atlas:default
// blocks (so the user discovers what's tunable without us keeping README
// in sync); keys the user has set are emitted uncommented. Root is always
// uncommented when set and omitted when empty.
//
// Use this from `atlas init` and onboarding when first creating a config.
// For re-persisting an already-edited config (where comments would be
// lost), use Save.
func RenderInitTOML(c, defaults Config) []byte {
	var b bytes.Buffer
	b.WriteString("# atlas configuration. Uncomment any key below to override its default.\n\n")

	if c.Root != "" {
		b.WriteString("# Where atlas scans for repos.\n")
		fmt.Fprintf(&b, "root = %q\n\n", c.Root)
	}

	for _, k := range managedKeys {
		if isManagedKeyAtDefault(k, c, defaults) {
			b.WriteString(renderManagedBlock(k, defaults))
		} else {
			b.WriteString(renderManagedUncommented(k, c))
		}
		b.WriteString("\n")
	}
	return b.Bytes()
}

// refreshManagedDefaults rebuilds atlas:default blocks for keys still on
// their built-in default value (i.e. not in userSet). Returns the new
// bytes and whether they differ from raw. Idempotent: calling twice with
// the same inputs is a no-op on the second call.
//
// Blocks the user has deleted (no sentinel) are not regenerated — respect
// the deletion. User-set keys (userSet[key] == true) leave their sentinel
// block alone too, since the value above it is what matters.
func refreshManagedDefaults(raw []byte, defaults Config, userSet map[managedKey]bool) ([]byte, bool) {
	text := string(raw)
	out := text
	for _, k := range managedKeys {
		if userSet[k] {
			continue
		}
		// Trim trailing newline so we don't double-up on the gap line that
		// follows each block in the file.
		replacement := strings.TrimRight(renderManagedBlock(k, defaults), "\n")
		out = replaceManagedBlock(out, k, replacement)
	}
	if out == text {
		return raw, false
	}
	return []byte(out), true
}

// renderManagedBlock returns the full sentinel-bracketed block for key,
// including the opening and closing sentinel lines plus a trailing newline.
func renderManagedBlock(k managedKey, defaults Config) string {
	var b bytes.Buffer
	fmt.Fprintf(&b, "%s%s%s\n", sentinelOpenPrefix, k, sentinelOpenSuffix)
	switch k {
	case keyMaxDepth:
		b.WriteString("# How deep to walk from root.\n")
		fmt.Fprintf(&b, "# max_depth = %d\n", defaults.MaxDepth)
	case keySkipDirs:
		b.WriteString("# Directories to skip. Two entry shapes:\n")
		b.WriteString("#   \"name\"              — match any directory with this basename, anywhere\n")
		b.WriteString("#   \"~/path\" or \"/path\" — match exactly one directory at this path\n")
		b.WriteString("# Uncommenting skip_dirs replaces the defaults below — start by copying\n")
		b.WriteString("# them and edit to taste. While commented, the defaults shown apply.\n")
		b.WriteString("# skip_dirs = [\n")
		for i, g := range skipDirsGroups {
			if i > 0 {
				b.WriteString("#\n")
			}
			fmt.Fprintf(&b, "#   # %s\n", g.label)
			for _, e := range g.entries {
				fmt.Fprintf(&b, "#   %q,\n", e)
			}
		}
		b.WriteString("# ]\n")
	case keyStaleDays:
		b.WriteString("# Days since last commit before a repo is considered cold.\n")
		fmt.Fprintf(&b, "# stale_days = %d\n", defaults.StaleDays)
	case keyTheme:
		b.WriteString("# Color theme: \"default\" or \"ansi\".\n")
		fmt.Fprintf(&b, "# theme = %q\n", defaults.Theme)
	}
	b.WriteString(sentinelClose + "\n")
	return b.String()
}

// renderManagedUncommented emits the active form of a managed key, used
// when the user's value differs from the default. No sentinels — sentinel
// blocks are only for documenting defaults.
func renderManagedUncommented(k managedKey, c Config) string {
	var b bytes.Buffer
	switch k {
	case keyMaxDepth:
		b.WriteString("# How deep to walk from root.\n")
		fmt.Fprintf(&b, "max_depth = %d\n", c.MaxDepth)
	case keySkipDirs:
		b.WriteString("# Directories to skip (replaces atlas's built-in defaults).\n")
		b.WriteString("skip_dirs = [\n")
		for _, e := range c.SkipDirs {
			fmt.Fprintf(&b, "  %q,\n", e)
		}
		b.WriteString("]\n")
	case keyStaleDays:
		b.WriteString("# Days since last commit before a repo is considered cold.\n")
		fmt.Fprintf(&b, "stale_days = %d\n", c.StaleDays)
	case keyTheme:
		b.WriteString("# Color theme: \"default\" or \"ansi\".\n")
		fmt.Fprintf(&b, "theme = %q\n", c.Theme)
	}
	return b.String()
}

func isManagedKeyAtDefault(k managedKey, c, defaults Config) bool {
	switch k {
	case keyMaxDepth:
		return c.MaxDepth == defaults.MaxDepth
	case keySkipDirs:
		return stringSlicesEqual(c.SkipDirs, defaults.SkipDirs)
	case keyStaleDays:
		return c.StaleDays == defaults.StaleDays
	case keyTheme:
		return c.Theme == defaults.Theme
	}
	return false
}

// replaceManagedBlock finds an atlas:default block for key in src and
// replaces the entire block (open sentinel through close sentinel,
// inclusive) with replacement. Returns src unchanged if no matching open
// sentinel exists or no atlas:end follows it.
func replaceManagedBlock(src string, key managedKey, replacement string) string {
	openNeedle := sentinelOpenPrefix + string(key)
	startIdx := indexAtLineStart(src, openNeedle)
	if startIdx < 0 {
		return src
	}
	// Find atlas:end after the open sentinel, also at line start.
	closeIdx := indexAtLineStart(src[startIdx:], sentinelClose)
	if closeIdx < 0 {
		return src
	}
	absCloseStart := startIdx + closeIdx
	closeLineEnd := absCloseStart + len(sentinelClose)
	// Consume the trailing newline of the close sentinel if present.
	if closeLineEnd < len(src) && src[closeLineEnd] == '\n' {
		closeLineEnd++
	}
	// Preserve a single trailing newline after the replacement so the gap
	// between blocks stays consistent.
	rep := replacement
	if !strings.HasSuffix(rep, "\n") {
		rep += "\n"
	}
	return src[:startIdx] + rep + src[closeLineEnd:]
}

func indexAtLineStart(src, needle string) int {
	pos := 0
	for {
		idx := strings.Index(src[pos:], needle)
		if idx < 0 {
			return -1
		}
		abs := pos + idx
		if abs == 0 || src[abs-1] == '\n' {
			return abs
		}
		pos = abs + 1
	}
}

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
