package repo_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sethdeckard/atlas/internal/repo"
)

func TestDetectLanguages_NoManifestsReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	got := repo.DetectLanguages(dir)
	if len(got) != 0 {
		t.Errorf("expected empty langs; got %v", got)
	}
}

func TestDetectLanguages_GoMod(t *testing.T) {
	dir := t.TempDir()
	mustTouch(t, dir, "go.mod")
	got := repo.DetectLanguages(dir)
	if len(got) != 1 || got[0] != "go" {
		t.Errorf("want [go]; got %v", got)
	}
}

func TestDetectLanguages_PolyglotPrimaryGoSecondaryDocker(t *testing.T) {
	dir := t.TempDir()
	mustTouch(t, dir, "go.mod")
	mustTouch(t, dir, "Dockerfile")
	got := repo.DetectLanguages(dir)
	if len(got) != 2 || got[0] != "go" || got[1] != "docker" {
		t.Errorf("want [go docker]; got %v", got)
	}
}

func TestDetectLanguages_PythonAnyOfThree(t *testing.T) {
	for _, manifest := range []string{"pyproject.toml", "requirements.txt", "setup.py"} {
		t.Run(manifest, func(t *testing.T) {
			dir := t.TempDir()
			mustTouch(t, dir, manifest)
			got := repo.DetectLanguages(dir)
			if len(got) != 1 || got[0] != "python" {
				t.Errorf("want [python] for %s; got %v", manifest, got)
			}
		})
	}
}

func TestDetectLanguages_DotnetGlobMatch(t *testing.T) {
	dir := t.TempDir()
	mustTouch(t, dir, "MyService.csproj")
	got := repo.DetectLanguages(dir)
	if len(got) != 1 || got[0] != "dotnet" {
		t.Errorf("want [dotnet]; got %v", got)
	}
}

func TestDetectLanguages_EmptyWorktreeBareRepo(t *testing.T) {
	got := repo.DetectLanguages("")
	if got != nil {
		t.Errorf("empty worktree should return nil; got %v", got)
	}
}

func mustTouch(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}
}
