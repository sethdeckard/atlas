package onboard_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sethdeckard/atlas/internal/config"
	"github.com/sethdeckard/atlas/internal/onboard"
)

// makeTTY returns a Prompter wired against the given input with HomeDir
// pointing at a tempdir we control. Tests that need the suggestion path
// also call setHomeEnv so config.ExpandHome's tilde expansion lands in
// the same dir HomeDir advertises.
func makeTTY(in io.Reader, out *bytes.Buffer, homeDir string) onboard.Prompter {
	return onboard.Prompter{
		In:         in,
		Out:        out,
		IsTerminal: func() bool { return true },
		HomeDir:    homeDir,
	}
}

func setHomeEnv(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("HOME", dir)
}

func TestEnsureRoot_NoTTYReturnsConfiguredErrorMessage(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	p := onboard.Prompter{
		In:         &bytes.Buffer{},
		Out:        &bytes.Buffer{},
		IsTerminal: func() bool { return false },
		HomeDir:    "",
	}
	_, err := p.EnsureRoot(context.Background(), cfgPath, config.Defaults())
	if err == nil {
		t.Fatal("expected error in no-TTY context")
	}
	msg := err.Error()
	if !strings.Contains(msg, `no projects root configured`) ||
		!strings.Contains(msg, `atlas init`) ||
		!strings.Contains(msg, cfgPath) {
		t.Errorf("error message missing expected pieces: %s", msg)
	}
}

func TestEnsureRoot_AcceptsTypedPath(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "code")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(tmp, "config.toml")

	in := bytes.NewBufferString(target + "\n")
	out := &bytes.Buffer{}
	p := makeTTY(in, out, "")

	got, err := p.EnsureRoot(context.Background(), cfgPath, config.Defaults())
	if err != nil {
		t.Fatalf("EnsureRoot: %v", err)
	}
	want, _ := filepath.Abs(target)
	if got != want {
		t.Errorf("returned root: got %q, want %q", got, want)
	}

	cfg, _, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Root != want {
		t.Errorf("persisted root: got %q, want %q", cfg.Root, want)
	}
	if !strings.Contains(out.String(), "saved root to "+cfgPath) {
		t.Errorf("expected save confirmation in output; got %q", out.String())
	}
}

func TestEnsureRoot_AcceptsSuggestionWhenProjectsExists(t *testing.T) {
	tmp := t.TempDir()
	setHomeEnv(t, tmp)
	if err := os.Mkdir(filepath.Join(tmp, "projects"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(tmp, "config.toml")

	// Empty line accepts the suggestion.
	in := bytes.NewBufferString("\n")
	out := &bytes.Buffer{}
	p := makeTTY(in, out, tmp)

	got, err := p.EnsureRoot(context.Background(), cfgPath, config.Defaults())
	if err != nil {
		t.Fatalf("EnsureRoot: %v", err)
	}
	wantAbs := filepath.Join(tmp, "projects")
	if got != wantAbs {
		t.Errorf("returned root: got %q, want %q", got, wantAbs)
	}
	if !strings.Contains(out.String(), "[~/projects]") {
		t.Errorf("expected ~/projects suggestion in output; got %q", out.String())
	}
}

func TestEnsureRoot_NoSuggestionWhenProjectsMissing(t *testing.T) {
	tmp := t.TempDir() // no `projects` subdir
	setHomeEnv(t, tmp)
	cfgPath := filepath.Join(tmp, "config.toml")

	// Empty line should NOT be accepted (no suggestion); next line provides
	// a real directory.
	target := filepath.Join(tmp, "elsewhere")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	in := bytes.NewBufferString("\n" + target + "\n")
	out := &bytes.Buffer{}
	p := makeTTY(in, out, tmp)

	if _, err := p.EnsureRoot(context.Background(), cfgPath, config.Defaults()); err != nil {
		t.Fatalf("EnsureRoot: %v", err)
	}
	if !strings.Contains(out.String(), "a path is required") {
		t.Errorf("expected re-prompt message; got %q", out.String())
	}
	// The first prompt should NOT carry [~/projects] when the dir is missing.
	if strings.Contains(out.String(), "[~/projects]") {
		t.Errorf("unexpected ~/projects suggestion when dir missing; got %q", out.String())
	}
}

func TestEnsureRoot_RejectsBadPathThenAccepts(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "good")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(tmp, "config.toml")
	bogus := filepath.Join(tmp, "does-not-exist")

	in := bytes.NewBufferString(bogus + "\n" + target + "\n")
	out := &bytes.Buffer{}
	p := makeTTY(in, out, "")

	got, err := p.EnsureRoot(context.Background(), cfgPath, config.Defaults())
	if err != nil {
		t.Fatalf("EnsureRoot: %v", err)
	}
	if got != target {
		t.Errorf("returned root: got %q, want %q", got, target)
	}
	if !strings.Contains(out.String(), "directory does not exist: "+bogus) {
		t.Errorf("expected re-prompt message naming the bad path; got %q", out.String())
	}
}

func TestPromptRoot_AlwaysPromptsAndSeedsFromCfgRoot(t *testing.T) {
	tmp := t.TempDir()
	current := filepath.Join(tmp, "current")
	replacement := filepath.Join(tmp, "replacement")
	for _, d := range []string{current, replacement} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cfgPath := filepath.Join(tmp, "config.toml")
	cfg := config.Defaults()
	cfg.Root = current

	in := bytes.NewBufferString(replacement + "\n")
	out := &bytes.Buffer{}
	p := makeTTY(in, out, "")

	got, err := p.PromptRoot(context.Background(), cfgPath, cfg)
	if err != nil {
		t.Fatalf("PromptRoot: %v", err)
	}
	if got != replacement {
		t.Errorf("returned root: got %q, want %q", got, replacement)
	}
	if !strings.Contains(out.String(), "["+current+"]") {
		t.Errorf("expected suggestion %q in prompt; got %q", current, out.String())
	}

	loaded, _, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Root != replacement {
		t.Errorf("persisted root: got %q, want %q", loaded.Root, replacement)
	}
}

func TestPromptRoot_NoTTYReturnsError(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	p := onboard.Prompter{
		In:         &bytes.Buffer{},
		Out:        &bytes.Buffer{},
		IsTerminal: func() bool { return false },
		HomeDir:    "",
	}
	_, err := p.PromptRoot(context.Background(), cfgPath, config.Defaults())
	if err == nil {
		t.Fatal("expected error in no-TTY context")
	}
}

// TestEnsureRoot_CtxCancelAbortsRead guards the contract that SIGINT
// during the prompt cancels the blocking stdin read instead of being
// ignored. The reader is an io.Pipe with no writer activity so
// scanner.Scan blocks forever; cancelling ctx must unblock the loop
// and surface the wrapped ctx error.
func TestEnsureRoot_CtxCancelAbortsRead(t *testing.T) {
	pr, pw := io.Pipe()
	t.Cleanup(func() { _ = pw.Close() })

	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	p := makeTTY(pr, &bytes.Buffer{}, "")

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	_, err := p.EnsureRoot(ctx, cfgPath, config.Defaults())
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled; got %v", err)
	}
}
