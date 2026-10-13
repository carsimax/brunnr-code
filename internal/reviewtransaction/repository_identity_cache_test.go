package reviewtransaction

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// countRevParse counts every `git rev-parse` this package spawns from here on.
func countRevParse(t *testing.T) *atomic.Int64 {
	t.Helper()
	var count atomic.Int64
	original := gitCommandContext
	gitCommandContext = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		if slicesContain(args, "rev-parse") {
			count.Add(1)
		}
		return original(ctx, name, args...)
	}
	t.Cleanup(func() { gitCommandContext = original })
	return &count
}

// forgetReviewGitAnswers empties the process-wide Git answer caches, for tests
// that inject a failure into a Git query a warm cache would skip.
func forgetReviewGitAnswers() {
	reviewRepositoryRootCache.mu.Lock()
	reviewRepositoryRootCache.entries = nil
	reviewRepositoryRootCache.mu.Unlock()
	reviewRepositoryDirectoriesCache.mu.Lock()
	reviewRepositoryDirectoriesCache.entries = nil
	reviewRepositoryDirectoriesCache.mu.Unlock()
	reviewObjectFormatCache.mu.Lock()
	reviewObjectFormatCache.entries = nil
	reviewObjectFormatCache.mu.Unlock()
}

func TestRepositoryIdentityCacheSkipsRevParseOnUnchangedRepository(t *testing.T) {
	requireSnapshotGit(t)
	repo := initSnapshotRepo(t)
	nested := filepath.Join(repo, "nested", "path")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	first, err := OpenRepositoryIdentityLease(ctx, nested)
	if err != nil {
		t.Fatal(err)
	}
	_, cleanup, err := isolatedImmutableTreeGit(ctx, nested)
	if err != nil {
		t.Fatal(err)
	}
	_ = cleanup()

	count := countRevParse(t)
	second, err := OpenRepositoryIdentityLease(ctx, nested)
	if err != nil {
		t.Fatal(err)
	}
	if err := second.Validate(ctx); err != nil {
		t.Fatal(err)
	}
	if root, err := (SnapshotBuilder{Repo: nested}).ResolveRepositoryRoot(ctx); err != nil || root != repo {
		t.Fatalf("ResolveRepositoryRoot() = %q, %v; want %q", root, err, repo)
	}
	_, cleanup, err = isolatedImmutableTreeGit(ctx, nested)
	if err != nil {
		t.Fatal(err)
	}
	_ = cleanup()
	if got := count.Load(); got != 0 {
		t.Fatalf("unchanged repository spawned %d rev-parse processes, want 0", got)
	}
	if first.Identity() != second.Identity() {
		t.Fatalf("cached identity changed: %#v != %#v", first.Identity(), second.Identity())
	}
}

func TestRepositoryIdentityCacheRefreshesChangedGitfilePayload(t *testing.T) {
	primary, linked := initRepositoryIdentityLeaseWorktree(t)
	other := filepath.Join(t.TempDir(), "other")
	if err := runSnapshotGit(primary, "worktree", "add", "-b", "identity-cache-other", other, "HEAD"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runSnapshotGit(primary, "worktree", "remove", "--force", other) })
	ctx := context.Background()
	before, err := OpenRepositoryIdentityLease(ctx, linked)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := os.ReadFile(filepath.Join(other, ".git"))
	if err != nil {
		t.Fatal(err)
	}
	// Rewrite in place so only the payload, not the entry's inode, changes.
	file, err := os.OpenFile(filepath.Join(linked, ".git"), os.O_WRONLY|os.O_TRUNC, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	count := countRevParse(t)
	after, err := OpenRepositoryIdentityLease(ctx, linked)
	if err != nil {
		t.Fatal(err)
	}
	if count.Load() == 0 {
		t.Fatal("changed gitfile payload reused the cached Git answer")
	}
	if after.Identity().GitDir == before.Identity().GitDir {
		t.Fatalf("GitDir stayed %q after the gitfile was repointed", after.Identity().GitDir)
	}
	if !strings.Contains(after.Identity().GitDir, "other") {
		t.Fatalf("GitDir = %q, want the repointed worktree directory", after.Identity().GitDir)
	}
}

func TestRepositoryIdentityCacheRefreshesRootForNestedRepository(t *testing.T) {
	requireSnapshotGit(t)
	repo := initSnapshotRepo(t)
	nested := filepath.Join(repo, "vendor", "inner")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if root, err := (SnapshotBuilder{Repo: nested}).ResolveRepositoryRoot(ctx); err != nil || root != repo {
		t.Fatalf("ResolveRepositoryRoot() = %q, %v; want %q", root, err, repo)
	}
	inner := filepath.Join(repo, "vendor")
	if err := runSnapshotGit(inner, "init"); err != nil {
		t.Fatal(err)
	}
	root, err := (SnapshotBuilder{Repo: nested}).ResolveRepositoryRoot(ctx)
	if err != nil || root != inner {
		t.Fatalf("ResolveRepositoryRoot() after nesting = %q, %v; want %q", root, err, inner)
	}
}

func TestRepositoryIdentityCacheRefreshesReplacedRepository(t *testing.T) {
	requireSnapshotGit(t)
	repo := filepath.Join(canonicalTempDir(t), "repo")
	initAt := func(format string) {
		t.Helper()
		if err := os.MkdirAll(repo, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := runSnapshotGit(repo, "init", "--object-format="+format); err != nil {
			t.Skipf("git cannot create a %s repository: %v", format, err)
		}
	}
	ctx := context.Background()
	initAt("sha1")
	if _, err := OpenRepositoryIdentityLease(ctx, repo); err != nil {
		t.Fatal(err)
	}
	identity, err := reviewRepositoryIdentity(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	if format, err := repositoryObjectFormat(ctx, identity); err != nil || format != "sha1" {
		t.Fatalf("repositoryObjectFormat() = %q, %v; want sha1", format, err)
	}

	if err := os.RemoveAll(repo); err != nil {
		t.Fatal(err)
	}
	initAt("sha256")
	lease, err := OpenRepositoryIdentityLease(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	for _, directory := range []reviewRepositoryDirectoryIdentity{lease.root, lease.commonDir, lease.gitDir} {
		current, err := os.Stat(directory.path)
		if err != nil || !os.SameFile(directory.info, current) {
			t.Fatalf("lease kept a stale identity for %q: %v", directory.path, err)
		}
	}
	if err := lease.Validate(ctx); err != nil {
		t.Fatal(err)
	}
	identity = reviewRepositoryIdentityRecordFromLease(lease)
	if format, err := repositoryObjectFormat(ctx, identity); err != nil || format != "sha256" {
		t.Fatalf("repositoryObjectFormat() after replacement = %q, %v; want sha256", format, err)
	}
}
