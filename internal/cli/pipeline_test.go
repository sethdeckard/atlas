package cli_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sethdeckard/atlas/internal/cli"
	"github.com/sethdeckard/atlas/internal/config"
)

func TestPipeline_RunAnnotatesDerivedSignals(t *testing.T) {
	tree, _ := buildTreeA(t)
	defer cleanupCacheEnv(t)

	pipe, err := cli.NewPipeline(context.Background(), cli.PipelineOpts{
		PathArg: tree,
	}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}
	repos, walkErr := pipe.Run(context.Background())
	if walkErr != nil {
		t.Fatalf("Run walkErr: %v", walkErr)
	}
	if len(repos) == 0 {
		t.Fatalf("expected repos under %s; got none", tree)
	}
	// AnnotateDerived sets ActivityTier from LastCommitAt + StaleDays.
	// All buildTreeA repos have a real commit, so none should be empty.
	for _, r := range repos {
		if r.ActivityTier == "" {
			t.Errorf("%s: ActivityTier unset — AnnotateDerived not invoked?", r.Name)
		}
		if r.WorktreeCount < 1 {
			t.Errorf("%s: WorktreeCount = %d; want >= 1", r.Name, r.WorktreeCount)
		}
	}
	if err := pipe.Save(); err != nil {
		t.Errorf("Save: %v", err)
	}
}

func TestPipeline_UseCachedOnlySkipsDiscovery(t *testing.T) {
	tree, _ := buildTreeA(t)
	defer cleanupCacheEnv(t)

	// Warm: populate cache.
	warm, err := cli.NewPipeline(context.Background(), cli.PipelineOpts{
		PathArg: tree,
	}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := warm.Run(context.Background()); err != nil {
		t.Fatalf("warm Run: %v", err)
	}
	if err := warm.Save(); err != nil {
		t.Fatal(err)
	}

	// --cached: should not require the directory to exist on disk.
	// Move the tree aside to simulate a vanished root, then assert
	// --cached still returns the previously-cached repos and zero
	// walk errors (no Discover call).
	gone := tree + ".moved"
	if err := os.Rename(tree, gone); err != nil {
		t.Fatalf("rename: %v", err)
	}
	t.Cleanup(func() { _ = os.Rename(gone, tree) })

	cached, err := cli.NewPipeline(context.Background(), cli.PipelineOpts{
		PathArg:       tree,
		UseCachedOnly: true,
	}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("NewPipeline cached: %v", err)
	}
	repos, walkErr := cached.Run(context.Background())
	if walkErr != nil {
		t.Errorf("cached Run should not surface walk errors; got %v", walkErr)
	}
	if cached.WalkErrors() != 0 {
		t.Errorf("cached pipeline should report 0 walk errors; got %d", cached.WalkErrors())
	}
	if len(repos) == 0 {
		t.Errorf("expected cached repos to surface; got none")
	}
}

func TestPipeline_FreshRequiresDirectory(t *testing.T) {
	defer cleanupCacheEnv(t)
	dir := t.TempDir()
	bogus := filepath.Join(dir, "no-such-dir")
	_, err := cli.NewPipeline(context.Background(), cli.PipelineOpts{
		PathArg: bogus,
		Fresh:   true,
	}, &bytes.Buffer{})
	if err == nil {
		t.Fatalf("expected an error for non-existent root; got nil")
	}
	if !strings.Contains(err.Error(), bogus) {
		t.Errorf("error should mention the bad root; got: %v", err)
	}
}

// TestPipeline_FreshPrunesMissingRepos guards the contract that
// reconcileCache always deletes gone-from-disk entries — even on
// Fresh runs. atlas refresh always sets Fresh: true, so without this
// guarantee a deleted repo would persist in the cache indefinitely.
func TestPipeline_FreshPrunesMissingRepos(t *testing.T) {
	root, repos := buildTreeA(t)
	defer cleanupCacheEnv(t)

	// Warm: populate cache.
	warm, err := cli.NewPipeline(context.Background(), cli.PipelineOpts{
		PathArg: root,
	}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := warm.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := warm.Save(); err != nil {
		t.Fatal(err)
	}

	// Delete a repo from disk.
	doomed := repos[0]
	if err := os.RemoveAll(doomed); err != nil {
		t.Fatal(err)
	}

	// Fresh pipeline: should prune the gone repo from the cache as
	// part of reconcile, even though Fresh skips the stale-detection
	// path that normally surfaces the gone list.
	fresh, err := cli.NewPipeline(context.Background(), cli.PipelineOpts{
		PathArg: root,
		Fresh:   true,
	}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	post, _ := fresh.Run(context.Background())
	if err := fresh.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	for _, r := range post {
		if r.Path == doomed {
			t.Errorf("fresh refresh did not prune deleted repo %s; still in scope", doomed)
		}
	}
	if _, present := fresh.Cache.Repos[doomed]; present {
		t.Errorf("deleted repo %s should be gone from cache; still present", doomed)
	}
}

func TestPipeline_CachedAndFreshAreMutuallyExclusive(t *testing.T) {
	defer cleanupCacheEnv(t)
	_, err := cli.NewPipeline(context.Background(), cli.PipelineOpts{
		PathArg:       t.TempDir(),
		UseCachedOnly: true,
		Fresh:         true,
	}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("expected mutually-exclusive error; got %v", err)
	}
}

// TestPipeline_OnboardsWhenNoRootSourceProvided guards the contract that
// when no [PATH], no --root, and no `root:` config value is present,
// resolvePipelineRoot routes through the onboarding seam — the prompt's
// returned path becomes the pipeline root.
func TestPipeline_OnboardsWhenNoRootSourceProvided(t *testing.T) {
	tree, _ := buildTreeA(t)
	defer cleanupCacheEnv(t)

	called := 0
	restore := cli.SetPromptForRoot(func(_ context.Context, configPath string, _ config.Config) (string, error) {
		called++
		return tree, nil
	})
	defer restore()

	pipe, err := cli.NewPipeline(context.Background(), cli.PipelineOpts{}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}
	if called != 1 {
		t.Errorf("expected onboarding to be invoked once; got %d", called)
	}
	wantAbs, _ := filepath.Abs(tree)
	if pipe.Root != wantAbs {
		t.Errorf("pipe.Root = %q; want %q", pipe.Root, wantAbs)
	}
}

// TestPipeline_PathArgSkipsOnboard confirms the onboarding seam is NOT
// invoked when a positional [PATH] is supplied — the arg is treated as a
// one-off override.
func TestPipeline_PathArgSkipsOnboard(t *testing.T) {
	tree, _ := buildTreeA(t)
	defer cleanupCacheEnv(t)

	called := 0
	restore := cli.SetPromptForRoot(func(_ context.Context, configPath string, _ config.Config) (string, error) {
		called++
		return "", nil
	})
	defer restore()

	if _, err := cli.NewPipeline(context.Background(), cli.PipelineOpts{
		PathArg: tree,
	}, &bytes.Buffer{}); err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}
	if called != 0 {
		t.Errorf("PathArg supplied — onboarding should NOT fire; called %d times", called)
	}
}
