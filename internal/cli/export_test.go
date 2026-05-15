package cli_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sethdeckard/atlas/internal/cli"
)

// TestExport_MarkdownProducesActivitySections is the high-level
// happy-path: export a tree of go + ruby repos, expect activity-tier
// headers, top-dir sub-sections, and at least one bullet per repo.
func TestExport_MarkdownProducesActivitySections(t *testing.T) {
	root := t.TempDir()
	defer cleanupCacheEnv(t)

	goRepo := filepath.Join(root, "svc", "go-thing")
	mustMkdir(t, goRepo)
	gitInit(t, goRepo)
	mustCommit(t, goRepo, "go.mod", "module x\n")

	rubyRepo := filepath.Join(root, "svc", "ruby-thing")
	mustMkdir(t, rubyRepo)
	gitInit(t, rubyRepo)
	mustCommit(t, rubyRepo, "Gemfile", "source 'https://rubygems.org'\n")

	out := filepath.Join(t.TempDir(), "export.md")
	cmd := cli.NewExportCommand()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{root, "--markdown", out})
	cmd.SetContext(context.Background())
	if err := cmd.Execute(); err != nil {
		t.Fatalf("export: %v", err)
	}

	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read export: %v", err)
	}
	body := string(got)
	for _, want := range []string{
		"# Repos under ",
		"## ", // some activity tier header
		"### svc",
		"go-thing",
		"ruby-thing",
		"languages: go",
		"languages: ruby",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in export body:\n%s", want, body)
		}
	}
}

// TestExport_LargeSectionWrapsInDetails confirms the > 20 repo
// section uses <details><summary>.
func TestExport_LargeSectionWrapsInDetails(t *testing.T) {
	root := t.TempDir()
	defer cleanupCacheEnv(t)

	for i := 0; i < 22; i++ {
		dir := filepath.Join(root, "many", fmt.Sprintf("r%02d", i))
		mustMkdir(t, dir)
		gitInit(t, dir)
		mustCommit(t, dir, "x", "x")
	}

	out := filepath.Join(t.TempDir(), "export.md")
	cmd := cli.NewExportCommand()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{root, "--markdown", out})
	cmd.SetContext(context.Background())
	if err := cmd.Execute(); err != nil {
		t.Fatalf("export: %v", err)
	}
	body, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(body), "<details>") {
		t.Errorf("expected <details> wrap for 22-repo section; got:\n%s", body)
	}
}

// TestExport_EmptyTreeProducesPlaceholder verifies graceful empty
// state — no panic, a sensible placeholder line.
func TestExport_EmptyTreeProducesPlaceholder(t *testing.T) {
	root := t.TempDir()
	defer cleanupCacheEnv(t)

	out := filepath.Join(t.TempDir(), "export.md")
	cmd := cli.NewExportCommand()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{root, "--markdown", out})
	cmd.SetContext(context.Background())
	if err := cmd.Execute(); err != nil {
		t.Fatalf("export: %v", err)
	}
	body, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "No repositories") {
		t.Errorf("expected placeholder for empty tree; got:\n%s", body)
	}
}

// TestExport_RequiresMarkdownFlag confirms cobra flags the missing
// required flag rather than silently producing no output.
func TestExport_RequiresMarkdownFlag(t *testing.T) {
	defer cleanupCacheEnv(t)
	cmd := cli.NewExportCommand()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{t.TempDir()})
	cmd.SetContext(context.Background())
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "markdown") {
		t.Errorf("expected required-flag error; got %v", err)
	}
}
