package tui

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/sethdeckard/atlas/internal/config"
)

// TestResolveRoot_OnboardsWhenAllSourcesEmpty guards the contract that
// resolveRoot routes through promptForRoot when no rootArg and no
// cfg.Root is supplied. The test injects a stub via SetPromptForRoot so
// no real TTY interaction happens.
func TestResolveRoot_OnboardsWhenAllSourcesEmpty(t *testing.T) {
	tmp := t.TempDir()
	called := 0
	restore := SetPromptForRoot(func(_ context.Context, configPath string, _ config.Config) (string, error) {
		called++
		return tmp, nil
	})
	defer restore()

	got, err := resolveRoot(context.Background(), "", config.Config{}, "/any/config.toml")
	if err != nil {
		t.Fatalf("resolveRoot: %v", err)
	}
	if called != 1 {
		t.Errorf("expected onboarding to be called once; got %d", called)
	}
	wantAbs, _ := filepath.Abs(tmp)
	if got != wantAbs {
		t.Errorf("resolveRoot returned %q; want %q", got, wantAbs)
	}
}

// TestResolveRoot_RootArgSkipsOnboard confirms the rootArg path skips
// the onboarding seam — it's a one-off override.
func TestResolveRoot_RootArgSkipsOnboard(t *testing.T) {
	tmp := t.TempDir()
	called := 0
	restore := SetPromptForRoot(func(_ context.Context, configPath string, _ config.Config) (string, error) {
		called++
		return "", nil
	})
	defer restore()

	got, err := resolveRoot(context.Background(), tmp, config.Config{}, "/any/config.toml")
	if err != nil {
		t.Fatalf("resolveRoot: %v", err)
	}
	if called != 0 {
		t.Errorf("rootArg supplied — onboarding should NOT fire; called %d times", called)
	}
	wantAbs, _ := filepath.Abs(tmp)
	if got != wantAbs {
		t.Errorf("resolveRoot returned %q; want %q", got, wantAbs)
	}
}

// TestResolveRoot_CfgRootSkipsOnboard confirms a non-empty cfg.Root
// short-circuits onboarding.
func TestResolveRoot_CfgRootSkipsOnboard(t *testing.T) {
	tmp := t.TempDir()
	called := 0
	restore := SetPromptForRoot(func(_ context.Context, configPath string, _ config.Config) (string, error) {
		called++
		return "", nil
	})
	defer restore()

	got, err := resolveRoot(context.Background(), "", config.Config{Root: tmp}, "/any/config.toml")
	if err != nil {
		t.Fatalf("resolveRoot: %v", err)
	}
	if called != 0 {
		t.Errorf("cfg.Root set — onboarding should NOT fire; called %d times", called)
	}
	wantAbs, _ := filepath.Abs(tmp)
	if got != wantAbs {
		t.Errorf("resolveRoot returned %q; want %q", got, wantAbs)
	}
}
