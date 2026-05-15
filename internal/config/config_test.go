package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sethdeckard/atlas/internal/config"
	"github.com/sethdeckard/atlas/internal/scan"
)

func TestLoad_MissingFileReturnsDefaults(t *testing.T) {
	cfg, warnings, err := config.Load(filepath.Join(t.TempDir(), "no-such-file.toml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("expected no warnings on missing file; got %v", warnings)
	}
	def := config.Defaults()
	if cfg.MaxDepth != def.MaxDepth {
		t.Errorf("MaxDepth = %d; want %d", cfg.MaxDepth, def.MaxDepth)
	}
	// Root has no implicit default — empty signals "no root configured",
	// which routes callers through internal/onboard.
	if cfg.Root != "" {
		t.Errorf("Root should be empty by default; got %q", cfg.Root)
	}
}

func TestLoad_UserSkipDirsReplacesDefaults(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	body := []byte(`skip_dirs = ["my-vendor", "tmp", "~/Pictures"]
`)
	if err := os.WriteFile(cfgPath, body, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	gotMap := map[string]bool{}
	for _, d := range cfg.SkipDirs {
		gotMap[d] = true
	}
	if gotMap[scan.BuiltinSkipDirs[0]] {
		t.Errorf("expected built-in %q to be REPLACED (not appended) when user sets skip_dirs; got %v", scan.BuiltinSkipDirs[0], cfg.SkipDirs)
	}
	if !gotMap["my-vendor"] || !gotMap["tmp"] {
		t.Errorf("expected user basename entries my-vendor + tmp in SkipDirs; got %v", cfg.SkipDirs)
	}
	if _, ok := cfg.SkipBaseNames["my-vendor"]; !ok {
		t.Errorf("expected my-vendor in SkipBaseNames; got %v", cfg.SkipBaseNames)
	}
	if len(cfg.SkipAbsPaths) != 1 {
		t.Errorf("expected one home-anchored entry in SkipAbsPaths; got %v", cfg.SkipAbsPaths)
	}
}

func TestLoad_AbsentSkipDirsUsesDefaults(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(cfgPath, []byte("max_depth = 8\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok := cfg.SkipBaseNames["node_modules"]; !ok {
		t.Errorf("expected built-in node_modules in SkipBaseNames when skip_dirs absent; got %v", cfg.SkipBaseNames)
	}
	if _, ok := cfg.SkipBaseNames[".cargo"]; !ok {
		t.Errorf("expected built-in .cargo in SkipBaseNames; got %v", cfg.SkipBaseNames)
	}
	if len(cfg.SkipAbsPaths) == 0 {
		t.Errorf("expected home-anchored built-ins (~/Library etc.) in SkipAbsPaths; got empty")
	}
}

func TestLoad_RejectsInvalidSkipEntries(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	body := []byte(`skip_dirs = ["foo/bar", "ok"]` + "\n")
	if err := os.WriteFile(cfgPath, body, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, warnings, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok := cfg.SkipBaseNames["ok"]; !ok {
		t.Errorf("expected valid entry %q in SkipBaseNames; got %v", "ok", cfg.SkipBaseNames)
	}
	if _, ok := cfg.SkipBaseNames["foo/bar"]; ok {
		t.Errorf("invalid entry foo/bar should be dropped; got %v", cfg.SkipBaseNames)
	}
	matched := false
	for _, w := range warnings {
		if strings.Contains(w, "foo/bar") {
			matched = true
			break
		}
	}
	if !matched {
		t.Errorf("expected warning naming foo/bar; got %v", warnings)
	}
}

func TestLoad_OverridesScalarFields(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	body := []byte(`max_depth = 12
stale_days = 7
`)
	if err := os.WriteFile(cfgPath, body, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.MaxDepth != 12 {
		t.Errorf("MaxDepth = %d; want 12", cfg.MaxDepth)
	}
	if cfg.StaleDays != 7 {
		t.Errorf("StaleDays = %d; want 7", cfg.StaleDays)
	}
}

func TestExpandHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("UserHomeDir unavailable: %v", err)
	}
	cases := []struct {
		in   string
		want string
	}{
		{"~", home},
		{"~/projects", filepath.Join(home, "projects")},
		{"/abs/path", "/abs/path"},
		{"", ""},
	}
	for _, c := range cases {
		got, err := config.ExpandHome(c.in)
		if err != nil {
			t.Errorf("ExpandHome(%q): %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("ExpandHome(%q) = %q; want %q", c.in, got, c.want)
		}
	}
}

func TestContractHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("UserHomeDir unavailable: %v", err)
	}
	sep := string(filepath.Separator)
	cases := []struct {
		in   string
		want string
	}{
		{"", ""},
		{home, "~"},
		{filepath.Join(home, "code"), "~" + sep + "code"},
		{filepath.Join(home, ".config", "atlas", "config.toml"), "~" + sep + ".config" + sep + "atlas" + sep + "config.toml"},
		{"/etc/passwd", "/etc/passwd"}, // not under home → unchanged
		{home + "anything", home + "anything"}, // sibling-prefix safety
	}
	for _, c := range cases {
		got := config.ContractHome(c.in)
		if got != c.want {
			t.Errorf("ContractHome(%q) = %q; want %q", c.in, got, c.want)
		}
	}
}

func TestSave_RoundTrip(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	want := config.Defaults()
	want.Root = "~/code"
	want.MaxDepth = 9
	want.StaleDays = 45

	if err := config.Save(cfgPath, want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, _, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Root comes back ~-expanded by Load.
	wantRoot, _ := config.ExpandHome(want.Root)
	if got.Root != wantRoot {
		t.Errorf("Root: got %q, want %q", got.Root, wantRoot)
	}
	if got.MaxDepth != want.MaxDepth {
		t.Errorf("MaxDepth: got %d, want %d", got.MaxDepth, want.MaxDepth)
	}
	if got.StaleDays != want.StaleDays {
		t.Errorf("StaleDays: got %d, want %d", got.StaleDays, want.StaleDays)
	}
}

func TestSave_CreatesParentDir(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "nested", "subdir", "config.toml")
	cfg := config.Defaults()
	cfg.Root = "/somewhere"
	if err := config.Save(cfgPath, cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := os.Stat(cfgPath); err != nil {
		t.Fatalf("config file should exist: %v", err)
	}
}

func TestSave_OverwritesExisting(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	// Pre-populate with an oversized file so a short overwrite is detectable.
	prior := []byte("# huge prior content\n# " + strings.Repeat("x", 4096) + "\n")
	if err := os.WriteFile(cfgPath, prior, 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := config.Defaults()
	cfg.Root = "/short"
	if err := config.Save(cfgPath, cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) >= len(prior) {
		t.Errorf("expected smaller file after overwrite; got %d bytes (prior %d)", len(got), len(prior))
	}
	if strings.Contains(string(got), "huge prior content") {
		t.Errorf("prior content leaked into rewritten file")
	}
	if !strings.Contains(string(got), "/short") {
		t.Errorf("expected new root in saved file; got %s", got)
	}
}

func TestDefaultPath_HonorsXDGConfigHome(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/custom/xdg")
	got, err := config.DefaultPath()
	if err != nil {
		t.Fatalf("DefaultPath: %v", err)
	}
	want := filepath.Join("/custom/xdg", "atlas", "config.toml")
	if got != want {
		t.Errorf("DefaultPath = %q; want %q", got, want)
	}
}

func TestDefaultPath_FallsBackToHomeDotConfig(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "/fake/home")
	got, err := config.DefaultPath()
	if err != nil {
		t.Fatalf("DefaultPath: %v", err)
	}
	want := filepath.Join("/fake/home", ".config", "atlas", "config.toml")
	if got != want {
		t.Errorf("DefaultPath = %q; want %q", got, want)
	}
}

func TestLoad_DefaultTheme(t *testing.T) {
	cfg, warnings, err := config.Load(filepath.Join(t.TempDir(), "no-such-file.toml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Theme != config.ThemeDefault {
		t.Errorf("Theme = %q; want %q", cfg.Theme, config.ThemeDefault)
	}
	for _, w := range warnings {
		if strings.Contains(w, "theme") {
			t.Errorf("unexpected theme warning on missing file: %q", w)
		}
	}
}

func TestLoad_AcceptsKnownThemes(t *testing.T) {
	cases := []string{
		config.ThemeDefault,
		config.ThemeANSI,
	}
	for _, theme := range cases {
		t.Run(theme, func(t *testing.T) {
			dir := t.TempDir()
			cfgPath := filepath.Join(dir, "config.toml")
			body := []byte("theme = \"" + theme + "\"\n")
			if err := os.WriteFile(cfgPath, body, 0o644); err != nil {
				t.Fatal(err)
			}
			cfg, warnings, err := config.Load(cfgPath)
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if cfg.Theme != theme {
				t.Errorf("Theme = %q; want %q", cfg.Theme, theme)
			}
			for _, w := range warnings {
				if strings.Contains(w, "theme") {
					t.Errorf("unexpected theme warning for %q: %v", theme, w)
				}
			}
		})
	}
}

func TestLoad_UnknownThemeWarns(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	body := []byte(`theme = "garbage"` + "\n")
	if err := os.WriteFile(cfgPath, body, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, warnings, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Theme != config.ThemeDefault {
		t.Errorf("Theme = %q; want fallback %q", cfg.Theme, config.ThemeDefault)
	}
	matched := false
	for _, w := range warnings {
		if strings.Contains(w, "theme") && strings.Contains(w, "garbage") {
			matched = true
			break
		}
	}
	if !matched {
		t.Errorf("expected warning naming theme + garbage; got %v", warnings)
	}
}

func TestNormalizeTheme(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"default", "default"},
		{"ansi", "ansi"},
		{"  Default  ", "default"},
		{"ANSI", "ansi"},
		{"", ""},
		{"garbage", ""},
		// Hard cut from the prior name; "verdigris-night" is no longer
		// recognized and must fall through to "" so callers warn + reset.
		{"verdigris-night", ""},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			if got := config.NormalizeTheme(tc.in); got != tc.want {
				t.Errorf("NormalizeTheme(%q) = %q; want %q", tc.in, got, tc.want)
			}
		})
	}
}
