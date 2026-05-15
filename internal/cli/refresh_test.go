package cli_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sethdeckard/atlas/internal/cli"
)

// TestRefresh_VerbosePrintsDiffsForChangedRepos guards the per-repo
// diff output: dirty + and last_commit_at + are the most common
// transitions in real usage. We seed the cache via a warm run, then
// dirty one repo on disk and add a brand-new repo, then run
// `atlas refresh --verbose` and assert both transitions surface.
func TestRefresh_VerbosePrintsDiffsForChangedRepos(t *testing.T) {
	root, repos := buildTreeA(t)
	defer cleanupCacheEnv(t)

	// Warm: seed cache so the diff has a baseline.
	warm := cli.NewListCommand()
	warm.SetOut(&bytes.Buffer{})
	warm.SetErr(&bytes.Buffer{})
	warm.SetArgs([]string{root, "--format=name"})
	warm.SetContext(context.Background())
	if err := warm.Execute(); err != nil {
		t.Fatalf("warm list: %v", err)
	}

	// Dirty alpha (which was clean in the warm run).
	if err := os.WriteFile(filepath.Join(repos[0], "stage.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Add a brand-new repo so we exercise the (new) diff path.
	newRepo := filepath.Join(root, "delta")
	mustMkdir(t, newRepo)
	gitInit(t, newRepo)
	mustCommit(t, newRepo, "d.txt", "hi")

	stdout := &bytes.Buffer{}
	r := cli.NewRefreshCommand()
	r.SetOut(stdout)
	r.SetErr(&bytes.Buffer{})
	r.SetArgs([]string{root, "--verbose"})
	r.SetContext(context.Background())
	if err := r.Execute(); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, repos[0]+":") || !strings.Contains(out, "dirty") {
		t.Errorf("expected dirty diff for %s; got:\n%s", repos[0], out)
	}
	if !strings.Contains(out, newRepo+": (new)") {
		t.Errorf("expected (new) diff for %s; got:\n%s", newRepo, out)
	}
}

// TestRefresh_VerboseReportsRemovedRepos guards the (removed) diff
// path: a cached repo that no longer exists on disk must be pruned
// from the cache during the refresh and surfaced in the verbose
// output. Earlier the Fresh path skipped the gone-deletion step
// entirely, so deleted repos lingered in the cache forever.
func TestRefresh_VerboseReportsRemovedRepos(t *testing.T) {
	root, repos := buildTreeA(t)
	defer cleanupCacheEnv(t)

	// Warm: seed cache so all three repos are recorded.
	warm := cli.NewListCommand()
	warm.SetOut(&bytes.Buffer{})
	warm.SetErr(&bytes.Buffer{})
	warm.SetArgs([]string{root, "--format=name"})
	warm.SetContext(context.Background())
	if err := warm.Execute(); err != nil {
		t.Fatalf("warm list: %v", err)
	}

	// Delete one of the repos from disk.
	doomed := repos[0]
	if err := os.RemoveAll(doomed); err != nil {
		t.Fatalf("remove %s: %v", doomed, err)
	}

	stdout := &bytes.Buffer{}
	r := cli.NewRefreshCommand()
	r.SetOut(stdout)
	r.SetErr(&bytes.Buffer{})
	r.SetArgs([]string{root, "--verbose"})
	r.SetContext(context.Background())
	if err := r.Execute(); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, doomed+": (removed)") {
		t.Errorf("expected (removed) line for %s; got:\n%s", doomed, out)
	}
}

// TestRefresh_NoVerboseIsSilent verifies the default mode prints
// nothing — refresh is for cron / launchd, output would be noise.
func TestRefresh_NoVerboseIsSilent(t *testing.T) {
	root, _ := buildTreeA(t)
	defer cleanupCacheEnv(t)

	stdout := &bytes.Buffer{}
	r := cli.NewRefreshCommand()
	r.SetOut(stdout)
	r.SetErr(&bytes.Buffer{})
	r.SetArgs([]string{root})
	r.SetContext(context.Background())
	if err := r.Execute(); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if stdout.String() != "" {
		t.Errorf("default refresh should be silent; got:\n%s", stdout.String())
	}
}
