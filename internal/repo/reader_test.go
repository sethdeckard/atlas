package repo_test

import (
	"context"
	"testing"

	"github.com/sethdeckard/atlas/internal/gitfixture"
	"github.com/sethdeckard/atlas/internal/repo"
)

func TestRead_NormalRepo(t *testing.T) {
	dir := gitfixture.Repo(t, gitfixture.WithCommits(2), gitfixture.WithOrigin("git@example.com:foo/bar.git"))
	r := repo.Read(context.Background(), dir)

	if r.Err != "" {
		t.Errorf("unexpected Err: %s", r.Err)
	}
	if r.Kind != repo.KindRepo {
		t.Errorf("Kind = %v; want KindRepo", r.Kind)
	}
	if r.Branch != "main" {
		t.Errorf("Branch = %q; want main", r.Branch)
	}
	if r.Dirty {
		t.Errorf("expected clean")
	}
	if r.OriginURL != "git@example.com:foo/bar.git" {
		t.Errorf("OriginURL = %q", r.OriginURL)
	}
	if r.LastCommitAt == nil {
		t.Errorf("expected non-nil LastCommitAt")
	}
	if r.HeadMtime.IsZero() {
		t.Errorf("expected non-zero HeadMtime")
	}
	if r.IndexMtime.IsZero() {
		t.Errorf("expected non-zero IndexMtime")
	}
	if r.ConfigMtime.IsZero() {
		t.Errorf("expected non-zero ConfigMtime")
	}
	if r.CommonGitDir == "" {
		t.Errorf("expected non-empty CommonGitDir")
	}
}

func TestRead_DirtyAndUntrackedOnly(t *testing.T) {
	dirDirty := gitfixture.Repo(t, gitfixture.Dirty())
	r := repo.Read(context.Background(), dirDirty)
	if !r.Dirty {
		t.Errorf("expected Dirty=true")
	}
	if r.UntrackedOnly {
		t.Errorf("expected UntrackedOnly=false (modified tracked file)")
	}

	dirUntracked := gitfixture.Repo(t, gitfixture.UntrackedOnly())
	r2 := repo.Read(context.Background(), dirUntracked)
	if !r2.Dirty || !r2.UntrackedOnly {
		t.Errorf("expected dirty+untrackedOnly; got dirty=%v untrackedOnly=%v", r2.Dirty, r2.UntrackedOnly)
	}
}

func TestRead_DetachedHead(t *testing.T) {
	dir := gitfixture.Repo(t, gitfixture.WithCommits(2), gitfixture.Detached())
	r := repo.Read(context.Background(), dir)
	if !r.DetachedHead {
		t.Errorf("expected DetachedHead=true")
	}
	if r.Branch != "" {
		t.Errorf("expected empty Branch on detached HEAD; got %q", r.Branch)
	}
	if r.HeadSHA == "" {
		t.Errorf("expected non-empty HeadSHA")
	}
}

func TestRead_EmptyRepo(t *testing.T) {
	dir := gitfixture.Repo(t, gitfixture.Empty())
	r := repo.Read(context.Background(), dir)
	if r.LastCommitAt != nil {
		t.Errorf("expected nil LastCommitAt for empty repo")
	}
	if r.Err != "" {
		t.Errorf("unexpected Err for empty repo: %s", r.Err)
	}
}

func TestRead_NoOrigin(t *testing.T) {
	dir := gitfixture.Repo(t)
	r := repo.Read(context.Background(), dir)
	if r.OriginURL != "" {
		t.Errorf("expected empty OriginURL when no origin configured; got %q", r.OriginURL)
	}
}

func TestRead_BareRepo(t *testing.T) {
	dir := gitfixture.Repo(t, gitfixture.Bare())
	r := repo.Read(context.Background(), dir)
	if r.Kind != repo.KindBare {
		t.Errorf("Kind = %v; want KindBare", r.Kind)
	}
	if r.Dirty {
		t.Errorf("bare should never be dirty")
	}
}

func TestRead_NotARepo(t *testing.T) {
	dir := t.TempDir()
	r := repo.Read(context.Background(), dir)
	if r.Err == "" {
		t.Errorf("expected Err for non-repo dir")
	}
}

func TestRead_Worktree(t *testing.T) {
	primary := gitfixture.Repo(t)
	wt := gitfixture.Repo(t, gitfixture.WorktreeOf(primary), gitfixture.WithWorktreeName("feat"))
	r := repo.Read(context.Background(), wt)
	if r.Err != "" {
		t.Errorf("unexpected Err: %s", r.Err)
	}
	if r.Kind != repo.KindWorktree {
		t.Errorf("Kind = %v; want KindWorktree", r.Kind)
	}
	if r.Branch != "feat" {
		t.Errorf("Branch = %q; want feat", r.Branch)
	}
	if r.CommonGitDir == "" {
		t.Errorf("expected non-empty CommonGitDir for worktree")
	}
}
