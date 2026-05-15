package repo_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/sethdeckard/atlas/internal/gitfixture"
	"github.com/sethdeckard/atlas/internal/repo"
)

func TestUpdateStatus_BareRepoNoOp(t *testing.T) {
	dir := gitfixture.Repo(t, gitfixture.Bare())
	r := repo.Read(context.Background(), dir)
	r.Dirty = true // pretend stale cached state
	r.UntrackedOnly = true
	updated := repo.UpdateStatus(context.Background(), r)
	if updated.Dirty != true || updated.UntrackedOnly != true {
		t.Errorf("bare repo UpdateStatus must not touch dirty fields")
	}
}

func TestUpdateStatus_MissingPathNoOp(t *testing.T) {
	r := repo.Repo{
		Name:          "ghost",
		Path:          "/definitely/not/a/real/path",
		Kind:          repo.KindRepo,
		Dirty:         true,
		UntrackedOnly: true,
	}
	updated := repo.UpdateStatus(context.Background(), r)
	if updated.Dirty != true || updated.UntrackedOnly != true {
		t.Errorf("UpdateStatus on missing path should leave fields unchanged")
	}
}

func TestUpdateStatus_DirtyWorktreeUpdates(t *testing.T) {
	dir := gitfixture.Repo(t)
	r := repo.Read(context.Background(), dir)
	if r.Dirty {
		t.Fatalf("fresh fixture should be clean; got Dirty=%v", r.Dirty)
	}

	// Stage-skipping edit: write to a tracked file directly.
	if err := os.WriteFile(filepath.Join(dir, "file-1.txt"), []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}

	updated := repo.UpdateStatus(context.Background(), r)
	if !updated.Dirty {
		t.Errorf("expected Dirty=true after worktree edit")
	}
	if updated.UntrackedOnly {
		t.Errorf("expected UntrackedOnly=false (modified tracked file)")
	}
}

func TestUpdateStatus_UntrackedOnlyDetected(t *testing.T) {
	dir := gitfixture.Repo(t)
	r := repo.Read(context.Background(), dir)

	// Drop a brand-new untracked file.
	if err := os.WriteFile(filepath.Join(dir, "new.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	updated := repo.UpdateStatus(context.Background(), r)
	if !updated.Dirty || !updated.UntrackedOnly {
		t.Errorf("expected dirty+untrackedOnly; got dirty=%v untrackedOnly=%v",
			updated.Dirty, updated.UntrackedOnly)
	}
}
