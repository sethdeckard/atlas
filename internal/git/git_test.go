package git_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sethdeckard/atlas/internal/git"
	"github.com/sethdeckard/atlas/internal/gitfixture"
)

func TestMain(m *testing.M) {
	// Skip the whole package if git isn't installed (CI sanity check).
	if _, err := exec.LookPath("git"); err != nil {
		return
	}
	m.Run()
}

func TestResolvePaths_NormalRepo(t *testing.T) {
	dir := gitfixture.Repo(t)
	p, err := git.ResolvePaths(dir)
	if err != nil {
		t.Fatalf("ResolvePaths: %v", err)
	}
	if p.Bare {
		t.Errorf("expected Bare=false")
	}
	if p.WorktreePath != dir {
		t.Errorf("WorktreePath = %s; want %s", p.WorktreePath, dir)
	}
	if p.GitDir != dir+"/.git" {
		t.Errorf("GitDir = %s; want %s/.git", p.GitDir, dir)
	}
	if p.GitDir != p.CommonDir {
		t.Errorf("normal repo should have GitDir == CommonDir")
	}
}

func TestResolvePaths_BareRepo(t *testing.T) {
	dir := gitfixture.Repo(t, gitfixture.Bare())
	p, err := git.ResolvePaths(dir)
	if err != nil {
		t.Fatalf("ResolvePaths: %v", err)
	}
	if !p.Bare {
		t.Errorf("expected Bare=true")
	}
	if p.WorktreePath != "" {
		t.Errorf("WorktreePath = %q; want empty for bare", p.WorktreePath)
	}
	if p.GitDir != p.CommonDir || p.GitDir != dir {
		t.Errorf("bare GitDir/CommonDir should equal dir; got %s / %s", p.GitDir, p.CommonDir)
	}
}

func TestResolvePaths_Worktree(t *testing.T) {
	primary := gitfixture.Repo(t)
	wt := gitfixture.Repo(t, gitfixture.WorktreeOf(primary), gitfixture.WithWorktreeName("feature"))
	p, err := git.ResolvePaths(wt)
	if err != nil {
		t.Fatalf("ResolvePaths: %v", err)
	}
	if p.Bare {
		t.Errorf("expected Bare=false for worktree")
	}
	if p.WorktreePath != wt {
		t.Errorf("WorktreePath = %s; want %s", p.WorktreePath, wt)
	}
	if p.GitDir == p.CommonDir {
		t.Errorf("worktree should have GitDir != CommonDir; got both = %s", p.GitDir)
	}
	// CommonDir should resolve back to the primary's .git. Use EvalSymlinks
	// because git resolves /var → /private/var on macOS.
	resolvedPrimary, err := filepath.EvalSymlinks(primary)
	if err != nil {
		t.Fatalf("EvalSymlinks(primary): %v", err)
	}
	if !strings.HasPrefix(p.CommonDir, resolvedPrimary) {
		t.Errorf("CommonDir = %s; expected to be under primary %s", p.CommonDir, resolvedPrimary)
	}
}

func TestResolvePaths_NotARepo(t *testing.T) {
	dir := t.TempDir()
	if _, err := git.ResolvePaths(dir); err == nil {
		t.Errorf("expected error for non-repo dir")
	}
}

func TestHead_Branch(t *testing.T) {
	dir := gitfixture.Repo(t, gitfixture.WithBranch("trunk"))
	p, err := git.ResolvePaths(dir)
	if err != nil {
		t.Fatalf("ResolvePaths: %v", err)
	}
	branch, sha, detached, err := git.Head(context.Background(), p)
	if err != nil {
		t.Fatalf("Head: %v", err)
	}
	if branch != "trunk" {
		t.Errorf("branch = %q; want trunk", branch)
	}
	if detached {
		t.Errorf("expected detached=false")
	}
	if len(sha) == 0 {
		t.Errorf("expected non-empty short SHA")
	}
}

func TestHead_Detached(t *testing.T) {
	dir := gitfixture.Repo(t, gitfixture.WithCommits(2), gitfixture.Detached())
	p, _ := git.ResolvePaths(dir)
	branch, sha, detached, err := git.Head(context.Background(), p)
	if err != nil {
		t.Fatalf("Head: %v", err)
	}
	if !detached {
		t.Errorf("expected detached=true")
	}
	if branch != "" {
		t.Errorf("expected empty branch for detached HEAD; got %q", branch)
	}
	if len(sha) < 7 {
		t.Errorf("expected at least 7 chars of SHA; got %q", sha)
	}
}

func TestHead_EmptyRepo(t *testing.T) {
	dir := gitfixture.Repo(t, gitfixture.Empty(), gitfixture.WithBranch("main"))
	p, _ := git.ResolvePaths(dir)
	branch, sha, detached, err := git.Head(context.Background(), p)
	if err != nil {
		t.Fatalf("Head on empty: %v", err)
	}
	if branch != "main" {
		t.Errorf("expected branch=main on unborn HEAD; got %q", branch)
	}
	if detached {
		t.Errorf("expected detached=false on unborn HEAD")
	}
	if sha != "" {
		t.Errorf("expected empty SHA on unborn HEAD; got %q", sha)
	}
}

func TestStatus_Clean(t *testing.T) {
	dir := gitfixture.Repo(t)
	p, _ := git.ResolvePaths(dir)
	dirty, untrackedOnly, err := git.Status(context.Background(), p)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if dirty || untrackedOnly {
		t.Errorf("expected clean; got dirty=%v untrackedOnly=%v", dirty, untrackedOnly)
	}
}

func TestStatus_Dirty(t *testing.T) {
	dir := gitfixture.Repo(t, gitfixture.Dirty())
	p, _ := git.ResolvePaths(dir)
	dirty, untrackedOnly, err := git.Status(context.Background(), p)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !dirty {
		t.Errorf("expected dirty=true")
	}
	if untrackedOnly {
		t.Errorf("expected untrackedOnly=false (modified tracked file)")
	}
}

func TestStatus_UntrackedOnly(t *testing.T) {
	dir := gitfixture.Repo(t, gitfixture.UntrackedOnly())
	p, _ := git.ResolvePaths(dir)
	dirty, untrackedOnly, err := git.Status(context.Background(), p)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !dirty || !untrackedOnly {
		t.Errorf("expected dirty+untrackedOnly; got dirty=%v untrackedOnly=%v", dirty, untrackedOnly)
	}
}

func TestStatus_Bare(t *testing.T) {
	dir := gitfixture.Repo(t, gitfixture.Bare())
	p, _ := git.ResolvePaths(dir)
	dirty, _, err := git.Status(context.Background(), p)
	if err != nil {
		t.Fatalf("Status on bare: %v", err)
	}
	if dirty {
		t.Errorf("bare should always be clean")
	}
}

// TestStatus_DoesNotWriteIndex guards the observability-only invariant:
// `git status` must not rewrite .git/index even when the working tree's
// stat info has drifted from what the index recorded. The drift would
// normally trigger an opportunistic stat-cache refresh; GIT_OPTIONAL_LOCKS=0
// in attemptGit's env should suppress it.
func TestStatus_DoesNotWriteIndex(t *testing.T) {
	dir := gitfixture.Repo(t)
	p, _ := git.ResolvePaths(dir)

	indexPath := filepath.Join(p.GitDir, "index")

	// Bump the tracked file's mtime so the stat info in the index no longer
	// matches the working tree — the condition under which git status would
	// rewrite the index without GIT_OPTIONAL_LOCKS=0.
	tracked := filepath.Join(dir, "file-1.txt")
	future := time.Now().Add(2 * time.Hour)
	if err := os.Chtimes(tracked, future, future); err != nil {
		t.Fatalf("chtimes %s: %v", tracked, err)
	}

	before, err := os.Stat(indexPath)
	if err != nil {
		t.Fatalf("stat index: %v", err)
	}

	if _, _, err := git.Status(context.Background(), p); err != nil {
		t.Fatalf("Status: %v", err)
	}

	after, err := os.Stat(indexPath)
	if err != nil {
		t.Fatalf("stat index after: %v", err)
	}
	if !before.ModTime().Equal(after.ModTime()) {
		t.Errorf("git.Status rewrote .git/index (before=%v after=%v); GIT_OPTIONAL_LOCKS=0 should prevent this",
			before.ModTime(), after.ModTime())
	}
}

func TestLastCommit(t *testing.T) {
	dir := gitfixture.Repo(t, gitfixture.WithCommits(1))
	p, _ := git.ResolvePaths(dir)
	tm, err := git.LastCommit(context.Background(), p)
	if err != nil {
		t.Fatalf("LastCommit: %v", err)
	}
	if tm == nil {
		t.Fatalf("expected timestamp; got nil")
	}
	if !tm.Equal(gitfixture.FixedTime) {
		t.Errorf("LastCommitAt = %v; want %v", tm.UTC(), gitfixture.FixedTime)
	}
}

func TestLastCommit_EmptyRepo(t *testing.T) {
	dir := gitfixture.Repo(t, gitfixture.Empty())
	p, _ := git.ResolvePaths(dir)
	tm, err := git.LastCommit(context.Background(), p)
	if err != nil {
		t.Fatalf("LastCommit on empty: %v", err)
	}
	if tm != nil {
		t.Errorf("expected nil timestamp on empty repo; got %v", tm)
	}
}

func TestOriginURL_Set(t *testing.T) {
	const url = "git@github.com:sethdeckard/atlas.git"
	dir := gitfixture.Repo(t, gitfixture.WithOrigin(url))
	p, _ := git.ResolvePaths(dir)
	got, err := git.OriginURL(context.Background(), p)
	if err != nil {
		t.Fatalf("OriginURL: %v", err)
	}
	if got != url {
		t.Errorf("OriginURL = %q; want %q", got, url)
	}
}

func TestOriginURL_Missing(t *testing.T) {
	dir := gitfixture.Repo(t)
	p, _ := git.ResolvePaths(dir)
	got, err := git.OriginURL(context.Background(), p)
	if err != nil {
		t.Fatalf("OriginURL: %v", err)
	}
	if got != "" {
		t.Errorf("OriginURL = %q; want empty when no remote configured", got)
	}
}
