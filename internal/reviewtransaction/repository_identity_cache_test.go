package reviewtransaction

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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
	reviewGitObjectAnswerCache.mu.Lock()
	reviewGitObjectAnswerCache.entries = nil
	reviewGitObjectAnswerCache.mu.Unlock()
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

// countGitChildren counts every git child this package spawns from here on
// whose argument list matches.
func countGitChildren(t *testing.T, match func(args []string) bool) *atomic.Int64 {
	t.Helper()
	var count atomic.Int64
	original := gitCommandContext
	gitCommandContext = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		if match(args) {
			count.Add(1)
		}
		return original(ctx, name, args...)
	}
	t.Cleanup(func() { gitCommandContext = original })
	return &count
}

// reviewGitAnswerQueries counts the root, directories and object-format
// rev-parse queries separately.
type reviewGitAnswerQueries struct{ root, directories, format *atomic.Int64 }

func countReviewGitAnswerQueries(t *testing.T) reviewGitAnswerQueries {
	t.Helper()
	return reviewGitAnswerQueries{
		root: countGitChildren(t, func(args []string) bool {
			return slicesContain(args, "rev-parse") && args[len(args)-1] == "--show-toplevel"
		}),
		directories: countGitChildren(t, func(args []string) bool { return slicesContain(args, "--git-common-dir") }),
		format:      countGitChildren(t, func(args []string) bool { return slicesContain(args, "--show-object-format") }),
	}
}

// askReviewGitAnswers runs the root, directories and object-format queries
// for repo, answered from the caches when they are warm.
func askReviewGitAnswers(t *testing.T, repo string) {
	t.Helper()
	ctx := t.Context()
	if _, err := (SnapshotBuilder{Repo: repo}).ResolveRepositoryRoot(ctx); err != nil {
		t.Fatal(err)
	}
	lease, err := OpenRepositoryIdentityLease(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repositoryObjectFormat(ctx, reviewRepositoryIdentityRecordFromLease(lease)); err != nil {
		t.Fatal(err)
	}
}

func appendReviewGitConfig(t *testing.T, path string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("[brunnr]\n\tedit = true\n"); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestReviewGitAnswerCachesRefreshAfterConfigChanges(t *testing.T) {
	requireSnapshotGit(t)
	for _, test := range []struct {
		name  string
		setup func(t *testing.T, repo string) string
	}{
		{"repository config", func(t *testing.T, repo string) string {
			return filepath.Join(repo, ".git", "config")
		}},
		{"GIT_CONFIG_GLOBAL file", func(t *testing.T, repo string) string {
			path := filepath.Join(t.TempDir(), "global.gitconfig")
			appendReviewGitConfig(t, path)
			t.Setenv("GIT_CONFIG_GLOBAL", path)
			return path
		}},
		{"gitconfig created in HOME", func(t *testing.T, repo string) string {
			home := canonicalTempDir(t)
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
			return filepath.Join(home, ".gitconfig")
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo := initSnapshotRepo(t)
			config := test.setup(t, repo)
			askReviewGitAnswers(t, repo)
			queries := countReviewGitAnswerQueries(t)
			askReviewGitAnswers(t, repo)
			if got := queries.root.Load() + queries.directories.Load() + queries.format.Load(); got != 0 {
				t.Fatalf("unchanged config spawned %d rev-parse queries, want 0", got)
			}
			appendReviewGitConfig(t, config)
			askReviewGitAnswers(t, repo)
			if queries.root.Load() == 0 || queries.directories.Load() == 0 || queries.format.Load() == 0 {
				t.Fatalf("after editing %s: root=%d directories=%d format=%d queries, want each asked again",
					config, queries.root.Load(), queries.directories.Load(), queries.format.Load())
			}
		})
	}
}

func TestReviewGitAnswerCachesRejectConfigEditDuringQuery(t *testing.T) {
	requireSnapshotGit(t)
	repo := initSnapshotRepo(t)
	config := filepath.Join(repo, ".git", "config")
	forgetReviewGitAnswers()
	// The edit lands after Git read the config and before the answer is
	// stored, so only stamps taken before the query can see it.
	reviewGitAnswerHook = func() { appendReviewGitConfig(t, config) }
	askReviewGitAnswers(t, repo)
	reviewGitAnswerHook = nil
	queries := countReviewGitAnswerQueries(t)
	askReviewGitAnswers(t, repo)
	if queries.root.Load() == 0 || queries.directories.Load() == 0 {
		t.Fatalf("config edited during the query: root=%d directories=%d queries, want each asked again",
			queries.root.Load(), queries.directories.Load())
	}
}

func TestReviewGitAnswerCacheBoundsBytes(t *testing.T) {
	var cache reviewGitAnswerCache[int]
	cache.store("oversized", 1, reviewGitAnswerCacheByteLimit/4+1)
	if _, ok := cache.load("oversized"); ok {
		t.Fatal("an answer above a quarter of the byte limit was cached")
	}
	quarter := reviewGitAnswerCacheByteLimit / 4
	for index := range 5 {
		cache.store(strconv.Itoa(index), index, quarter)
		if cache.bytes > reviewGitAnswerCacheByteLimit {
			t.Fatalf("cache holds %d bytes, limit %d", cache.bytes, reviewGitAnswerCacheByteLimit)
		}
	}
	if _, ok := cache.load("4"); !ok {
		t.Fatal("the newest answer was not cached after the byte bound cleared the cache")
	}
}
