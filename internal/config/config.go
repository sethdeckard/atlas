// Package config loads atlas's TOML user config. Missing config returns
// defaults; the caller doesn't need to handle "no config" specially.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/sethdeckard/atlas/internal/atomicfile"
	"github.com/sethdeckard/atlas/internal/scan"
)

// Config is the parsed user config plus defaults applied. SkipDirs holds
// the effective skip list: scan.BuiltinSkipDirs when the user hasn't set
// skip_dirs, or the user's list (which replaces the defaults entirely)
// when they have. SkipBaseNames and SkipAbsPaths are the parsed forms
// passed to scan.Discover; consumers should prefer those over re-parsing
// SkipDirs.
type Config struct {
	Root          string              `toml:"root"`
	MaxDepth      int                 `toml:"max_depth"`
	SkipDirs      []string            `toml:"skip_dirs"`
	StaleDays     int                 `toml:"stale_days"`
	Theme         string              `toml:"theme"`
	SkipBaseNames map[string]struct{} `toml:"-"`
	SkipAbsPaths  map[string]struct{} `toml:"-"`
}

// Defaults returns a Config populated with atlas's defaults. SkipDirs is a
// fresh copy of scan.BuiltinSkipDirs; the parsed sets are nil and get
// populated by Load (which has access to $HOME for tilde expansion).
//
// Root is intentionally empty — atlas has no implicit projects root. When
// no [PATH] arg, --root flag, or `root:` config value is supplied, callers
// route through internal/onboard to prompt the user (or error in no-TTY
// contexts).
func Defaults() Config {
	skip := append([]string(nil), scan.BuiltinSkipDirs...)
	return Config{
		Root:      "",
		MaxDepth:  6,
		SkipDirs:  skip,
		StaleDays: 60,
		Theme:     defaultTheme,
	}
}

// Theme names. Used as both config values and the dispatch key
// for internal/tui/styles.go's per-theme style constructors.
const (
	ThemeDefault = "default"
	ThemeANSI    = "ansi"
	defaultTheme = ThemeDefault
)

// NormalizeTheme canonicalizes a theme name to a known value or the
// empty string (which callers treat as "unknown"). Whitespace and
// case are tolerated. Known names are ThemeDefault and ThemeANSI;
// anything else returns "".
func NormalizeTheme(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case ThemeDefault, ThemeANSI:
		return s
	}
	return ""
}

// homeDir is wired through a package var so ContractHome / ExpandHome /
// DefaultPath can be unit-tested without depending on the real $HOME.
var homeDir = os.UserHomeDir

// DefaultPath returns the path Load reads by default. $XDG_CONFIG_HOME is
// honored when set; otherwise we use ~/.config/atlas/config.toml on every
// OS — atlas is XDG-style end-to-end and deliberately ignores
// os.UserConfigDir, which on macOS returns ~/Library/Application Support.
func DefaultPath() (string, error) {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "atlas", "config.toml"), nil
	}
	home, err := homeDir()
	if err != nil {
		return "", fmt.Errorf("locate home dir: %w", err)
	}
	return filepath.Join(home, ".config", "atlas", "config.toml"), nil
}

// Load reads the config at path, layering it on top of Defaults(). A missing
// file is not an error — Load returns the defaults unchanged.
//
// User skip_dirs *replaces* scan.BuiltinSkipDirs entirely when set; while
// it's commented out (or absent), defaults apply. Other user-provided
// fields override defaults; unset fields keep the default value.
//
// Warnings are returned alongside the Config (and a nil error) for
// non-fatal validation issues — callers route them to stderr (CLI) or
// the TUI status bar.
//
// On every successful Load, any "atlas:default <key> ... atlas:end"
// blocks for keys still using their default value are refreshed against
// the current built-in defaults; if the file content changes the rewrite
// is atomic. Refresh failures surface as warnings, never as errors.
func Load(path string) (Config, []string, error) {
	c := Defaults()
	var warnings []string

	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			c, parseWarnings, ferr := finalize(c)
			warnings = append(warnings, parseWarnings...)
			return c, warnings, ferr
		}
		return c, warnings, fmt.Errorf("read config: %w", err)
	}

	// Parse into a partial overlay so unset fields don't zero defaults.
	// BurntSushi/toml leaves *T at nil when the key is absent, so we can
	// distinguish "not set" from "set to zero value". skip_dirs is *[]string
	// for the same reason — replace semantics need to know whether the user
	// uncommented the key at all.
	var overlay struct {
		Root      *string   `toml:"root"`
		MaxDepth  *int      `toml:"max_depth"`
		SkipDirs  *[]string `toml:"skip_dirs"`
		StaleDays *int      `toml:"stale_days"`
		Theme     *string   `toml:"theme"`
	}
	if _, err := toml.Decode(string(data), &overlay); err != nil {
		return c, warnings, fmt.Errorf("parse config: %w", err)
	}

	if overlay.Root != nil {
		c.Root = *overlay.Root
	}
	if overlay.MaxDepth != nil {
		c.MaxDepth = *overlay.MaxDepth
	}
	if overlay.SkipDirs != nil {
		// Replace, not append — the user's list is authoritative when set.
		c.SkipDirs = append([]string(nil), (*overlay.SkipDirs)...)
	}
	if overlay.StaleDays != nil {
		c.StaleDays = *overlay.StaleDays
	}
	if overlay.Theme != nil {
		c.Theme = *overlay.Theme
	}

	c, parseWarnings, ferr := finalize(c)
	warnings = append(warnings, parseWarnings...)
	if ferr != nil {
		return c, warnings, ferr
	}

	// Refresh atlas:default blocks for keys still on their default value
	// so a future uncomment lands on current values. Idempotent — no write
	// when the rendered text matches what's on disk.
	userSet := map[managedKey]bool{
		keyMaxDepth:  overlay.MaxDepth != nil,
		keySkipDirs:  overlay.SkipDirs != nil,
		keyStaleDays: overlay.StaleDays != nil,
		keyTheme:     overlay.Theme != nil,
	}
	if newRaw, changed := refreshManagedDefaults(data, Defaults(), userSet); changed {
		if werr := atomicWrite(path, newRaw); werr != nil {
			warnings = append(warnings, fmt.Sprintf("refresh config defaults: %v", werr))
		}
	}

	return c, warnings, nil
}

// finalize expands Root, parses SkipDirs into the sets scan.Discover
// consumes, and normalizes Theme. Returned warnings come from
// skip-entry parsing and theme validation; the error is from $HOME
// resolution failures only.
func finalize(c Config) (Config, []string, error) {
	expanded, err := ExpandHome(c.Root)
	if err != nil {
		return c, nil, err
	}
	c.Root = expanded
	home, _ := homeDir()
	bases, abs, warnings := parseSkipEntries(c.SkipDirs, home)
	c.SkipBaseNames = bases
	c.SkipAbsPaths = abs

	// Theme: empty falls back to default silently; non-empty unknown
	// values produce a warning and reset to the default.
	if normalized := NormalizeTheme(c.Theme); normalized == "" {
		if c.Theme != "" {
			warnings = append(warnings, fmt.Sprintf("theme: %q is unknown, using %q", c.Theme, defaultTheme))
		}
		c.Theme = defaultTheme
	} else {
		c.Theme = normalized
	}

	return c, warnings, nil
}

// parseSkipEntries splits raw skip_dirs entries into a basename set
// (matched against d.Name() during walk) and an absolute-path set
// (matched against the dir's absolute path). Returns warnings for
// malformed entries (relative paths containing a separator).
//
// Entry rules:
//   - No path separator               → basename match anywhere
//   - Starts with "~/" or equals "~"  → home-anchored, expanded against home
//   - Starts with "/"                 → absolute path
//   - Otherwise                       → invalid (warning, dropped)
func parseSkipEntries(entries []string, home string) (
	basenames map[string]struct{},
	absPaths map[string]struct{},
	warnings []string,
) {
	basenames = make(map[string]struct{})
	absPaths = make(map[string]struct{})
	for _, raw := range entries {
		entry := strings.TrimSpace(raw)
		if entry == "" {
			continue
		}
		switch {
		case entry == "~" || strings.HasPrefix(entry, "~/"):
			if home == "" {
				warnings = append(warnings, fmt.Sprintf("skip_dirs: %q references home but $HOME is unknown", raw))
				continue
			}
			expanded := home
			if entry != "~" {
				expanded = filepath.Join(home, strings.TrimPrefix(entry, "~/"))
			}
			absPaths[filepath.Clean(expanded)] = struct{}{}
		case strings.HasPrefix(entry, "/"):
			absPaths[filepath.Clean(entry)] = struct{}{}
		case !strings.ContainsRune(entry, '/') && !strings.ContainsRune(entry, filepath.Separator):
			basenames[entry] = struct{}{}
		default:
			warnings = append(warnings, fmt.Sprintf("skip_dirs: %q is a relative path (must be a bare name, ~/path, or /path)", raw))
		}
	}
	return basenames, absPaths, warnings
}

// atomicWrite writes data to path atomically. Parent dir is assumed to
// exist (Save handles MkdirAll on the create path).
func atomicWrite(path string, data []byte) error {
	return atomicfile.Write(path, data, atomicfile.Options{TempPattern: "config-*.toml"})
}

// Save writes cfg to path as TOML, creating parent directories as needed.
// Atomic via tempfile + rename in the same dir. Existing files are
// overwritten — comments are not preserved, but every key in cfg is written.
//
// Save is for re-persisting an already-edited config. Fresh-config writes
// (atlas init / onboarding) go through RenderInitTOML so commented-out
// defaults are surfaced.
func Save(path string, cfg Config) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir config dir: %w", err)
	}
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(cfg); err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	return atomicWrite(path, buf.Bytes())
}

// ExpandHome expands a leading "~/" or bare "~" against $HOME. Other paths
// are returned as-is.
func ExpandHome(path string) (string, error) {
	if path == "" {
		return path, nil
	}
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := homeDir()
		if err != nil {
			return path, fmt.Errorf("expand home: %w", err)
		}
		if path == "~" {
			return home, nil
		}
		return filepath.Join(home, strings.TrimPrefix(path, "~/")), nil
	}
	return path, nil
}

// ContractHome is the inverse of ExpandHome: it returns path with a leading
// $HOME replaced by "~". Returns path unchanged when it isn't under $HOME,
// when $HOME isn't readable, or when path is empty.
func ContractHome(path string) string {
	if path == "" {
		return path
	}
	home, err := homeDir()
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
