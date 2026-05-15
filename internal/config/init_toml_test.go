package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sethdeckard/atlas/internal/scan"
)

func TestSkipDirsGroupsMatchBuiltin(t *testing.T) {
	var flat []string
	for _, g := range skipDirsGroups {
		flat = append(flat, g.entries...)
	}
	if !stringSlicesEqual(flat, scan.BuiltinSkipDirs) {
		t.Errorf("skipDirsGroups has drifted from scan.BuiltinSkipDirs.\n  flat (groups): %v\n  builtin:       %v", flat, scan.BuiltinSkipDirs)
	}
}

func TestRenderInitTOML_AllDefaultsAreCommented(t *testing.T) {
	defaults := Defaults()
	cfg := defaults
	cfg.Root = "/users/test/projects"

	out := string(RenderInitTOML(cfg, defaults))

	// Root is uncommented when set.
	if !strings.Contains(out, `root = "/users/test/projects"`) {
		t.Errorf("expected uncommented root line; got:\n%s", out)
	}
	// Defaultable keys are commented atlas:default blocks.
	for _, k := range managedKeys {
		open := sentinelOpenPrefix + string(k)
		if !strings.Contains(out, open) {
			t.Errorf("expected %q sentinel for key %s; got:\n%s", open, k, out)
		}
	}
	// max_depth value appears, but only inside a comment.
	if !strings.Contains(out, "# max_depth = 6") {
		t.Errorf("expected commented `# max_depth = 6`; got:\n%s", out)
	}
	if strings.Contains(out, "\nmax_depth = ") {
		t.Errorf("expected max_depth to be commented out (at default); got:\n%s", out)
	}
	// skip_dirs commented and includes a sample of the builtin list.
	if !strings.Contains(out, `#   "node_modules",`) {
		t.Errorf("expected commented node_modules entry in skip_dirs block; got:\n%s", out)
	}
	if !strings.Contains(out, `#   "~/Library",`) {
		t.Errorf("expected commented ~/Library entry in skip_dirs block; got:\n%s", out)
	}
	// Sentinel pairs match.
	if strings.Count(out, sentinelOpenPrefix) != strings.Count(out, sentinelClose) {
		t.Errorf("mismatched sentinel pairs in output:\n%s", out)
	}
}

func TestRenderInitTOML_UncommentsUserSetKey(t *testing.T) {
	defaults := Defaults()
	cfg := defaults
	cfg.Root = "/p"
	cfg.MaxDepth = 12 // user-set, differs from default

	out := string(RenderInitTOML(cfg, defaults))

	if !strings.Contains(out, "\nmax_depth = 12\n") {
		t.Errorf("expected uncommented max_depth = 12 line; got:\n%s", out)
	}
	if strings.Contains(out, "atlas:default max_depth") {
		t.Errorf("did not expect atlas:default block for user-set max_depth; got:\n%s", out)
	}
	// skip_dirs is still at default → still a commented block.
	if !strings.Contains(out, "atlas:default skip_dirs") {
		t.Errorf("expected atlas:default skip_dirs block; got:\n%s", out)
	}
}

func TestRenderInitTOML_PreservesUserSetTheme(t *testing.T) {
	defaults := Defaults()
	cfg := defaults
	cfg.Root = "/p"
	cfg.Theme = ThemeANSI

	out := string(RenderInitTOML(cfg, defaults))

	if !strings.Contains(out, "\ntheme = \"ansi\"\n") {
		t.Errorf("expected uncommented theme = %q line; got:\n%s", ThemeANSI, out)
	}
	if strings.Contains(out, "atlas:default theme") {
		t.Errorf("did not expect atlas:default block for user-set theme; got:\n%s", out)
	}
}

func TestRefreshManagedDefaults_RewritesStaleBlock(t *testing.T) {
	// Pre-existing config with a stale skip_dirs block (only one entry).
	stale := `# atlas configuration.
root = "/users/test"

# atlas:default skip_dirs — uncomment to override
# skip_dirs = [
#   "old-only",
# ]
# atlas:end
`

	newRaw, changed := refreshManagedDefaults([]byte(stale), Defaults(), nil)
	if !changed {
		t.Fatal("expected refresh to detect stale block and rewrite")
	}
	got := string(newRaw)
	if !strings.Contains(got, `#   "node_modules",`) {
		t.Errorf("expected refreshed block to contain node_modules; got:\n%s", got)
	}
	if strings.Contains(got, `#   "old-only",`) {
		t.Errorf("expected stale entry old-only to be removed; got:\n%s", got)
	}
	// Idempotent on second call.
	_, changed2 := refreshManagedDefaults(newRaw, Defaults(), nil)
	if changed2 {
		t.Errorf("expected second refresh to be a no-op; got changed=true")
	}
}

func TestRefreshManagedDefaults_LeavesUserSetKeyAlone(t *testing.T) {
	body := `root = "/p"
skip_dirs = ["custom-only"]

# atlas:default skip_dirs — uncomment to override
# skip_dirs = [
#   "old-only",
# ]
# atlas:end
`
	userSet := map[managedKey]bool{keySkipDirs: true}
	out, changed := refreshManagedDefaults([]byte(body), Defaults(), userSet)
	if changed {
		t.Errorf("expected no rewrite when key is user-set; got changed=true\n%s", string(out))
	}
}

func TestRefreshManagedDefaults_DeletedSentinelLeftAlone(t *testing.T) {
	body := `root = "/p"
# user removed all atlas:default blocks; just keep going
`
	_, changed := refreshManagedDefaults([]byte(body), Defaults(), nil)
	if changed {
		t.Errorf("expected no rewrite when no sentinels exist; got changed=true")
	}
}

func TestLoad_RefreshesStaleManagedDefaults(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")

	stale := `root = "/some/place"

# atlas:default skip_dirs — uncomment to override
# skip_dirs = [
#   "outdated",
# ]
# atlas:end
`
	if err := os.WriteFile(cfgPath, []byte(stale), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, _, err := Load(cfgPath); err != nil {
		t.Fatalf("Load: %v", err)
	}

	got, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), `#   "node_modules",`) {
		t.Errorf("expected file to be refreshed with current builtins; got:\n%s", got)
	}
	if strings.Contains(string(got), `"outdated"`) {
		t.Errorf("expected outdated stale entry to be replaced; got:\n%s", got)
	}

	// Second Load is a no-op (mtime should not advance further; content stable).
	before, _ := os.Stat(cfgPath)
	if _, _, err := Load(cfgPath); err != nil {
		t.Fatalf("Load (2nd): %v", err)
	}
	after, _ := os.ReadFile(cfgPath)
	if string(got) != string(after) {
		t.Errorf("expected idempotent refresh; content changed:\nbefore: %s\nafter:  %s", got, after)
	}
	_ = before
}

func TestLoad_DoesNotTouchUserSetSkipDirs(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")

	body := `root = "/p"
skip_dirs = ["my-custom"]

# atlas:default skip_dirs — uncomment to override
# skip_dirs = [
#   "stale-default",
# ]
# atlas:end
`
	if err := os.WriteFile(cfgPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, _, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok := cfg.SkipBaseNames["my-custom"]; !ok {
		t.Errorf("expected my-custom in SkipBaseNames (user override applied); got %v", cfg.SkipBaseNames)
	}

	got, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	// User's uncommented skip_dirs is untouched.
	if !strings.Contains(string(got), `skip_dirs = ["my-custom"]`) {
		t.Errorf("expected user's uncommented skip_dirs preserved verbatim; got:\n%s", got)
	}
	// And the sentinel block in the file is left alone too — even though
	// it's stale, the user-set key wins (per refresh contract).
	if !strings.Contains(string(got), `"stale-default"`) {
		t.Errorf("expected sentinel block left alone when key is user-set; got:\n%s", got)
	}
}
