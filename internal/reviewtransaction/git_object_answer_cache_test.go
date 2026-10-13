package reviewtransaction

import (
	"context"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
)

// countGitSubcommand counts every git child this package spawns from here on
// whose argument list names subcommand.
func countGitSubcommand(t *testing.T, subcommand string) *atomic.Int64 {
	t.Helper()
	var count atomic.Int64
	original := gitCommandContext
	gitCommandContext = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		if slicesContain(args, subcommand) {
			count.Add(1)
		}
		return original(ctx, name, args...)
	}
	t.Cleanup(func() { gitCommandContext = original })
	return &count
}

func TestGitObjectAnswerCacheSkipsRepeatedTreeQueries(t *testing.T) {
	requireSnapshotGit(t)
	repo, snapshot := preparedCandidateSnapshot(t)
	builder := SnapshotBuilder{Repo: repo}
	ctx := t.Context()
	first, err := builder.changedPaths(ctx, snapshot.BaseTree, snapshot.CandidateTree)
	if err != nil {
		t.Fatal(err)
	}
	firstStats, err := builder.DiffStats(ctx, snapshot)
	if err != nil {
		t.Fatal(err)
	}

	diffTree, diff := countGitSubcommand(t, "diff-tree"), countGitSubcommand(t, "diff")
	second, err := builder.changedPaths(ctx, snapshot.BaseTree, snapshot.CandidateTree)
	if err != nil {
		t.Fatal(err)
	}
	secondStats, err := builder.DiffStats(ctx, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if got := diffTree.Load() + diff.Load(); got != 0 {
		t.Fatalf("repeated tree queries spawned %d git diff processes, want 0", got)
	}
	if strings.Join(first, "\x00") != strings.Join(second, "\x00") || len(firstStats) != len(secondStats) {
		t.Fatalf("cached answer differs: %v/%v vs %v/%v", first, firstStats, second, secondStats)
	}
	for index := range firstStats {
		if firstStats[index] != secondStats[index] {
			t.Fatalf("cached diff stat %d = %#v, want %#v", index, secondStats[index], firstStats[index])
		}
	}
}

func TestGitObjectAnswerCacheMissesDifferentTrees(t *testing.T) {
	requireSnapshotGit(t)
	repo, snapshot := preparedCandidateSnapshot(t)
	builder := SnapshotBuilder{Repo: repo}
	if _, err := builder.changedPaths(t.Context(), snapshot.BaseTree, snapshot.CandidateTree); err != nil {
		t.Fatal(err)
	}
	count := countGitSubcommand(t, "diff-tree")
	if _, err := builder.changedPaths(t.Context(), snapshot.CandidateTree, snapshot.BaseTree); err != nil {
		t.Fatal(err)
	}
	if count.Load() != 1 {
		t.Fatalf("different trees spawned %d diff-tree processes, want 1", count.Load())
	}
}

func TestGitObjectAnswerCacheMissesOtherObjectStore(t *testing.T) {
	requireSnapshotGit(t)
	// Every fixture repository is copied from one template, so both hold the
	// same tree IDs in different object stores.
	repo, other := initSnapshotRepo(t), initSnapshotRepo(t)
	tree := strings.TrimSpace(gitSnapshot(t, repo, "rev-parse", "HEAD^{tree}"))
	if got := strings.TrimSpace(gitSnapshot(t, other, "rev-parse", "HEAD^{tree}")); got != tree {
		t.Fatalf("fixture trees differ: %s != %s", got, tree)
	}
	if _, err := runGit(t.Context(), repo, nil, nil, "ls-tree", "-r", "-z", tree); err != nil {
		t.Fatal(err)
	}
	count := countGitSubcommand(t, "ls-tree")
	if _, err := runGit(t.Context(), other, nil, nil, "ls-tree", "-r", "-z", tree); err != nil {
		t.Fatal(err)
	}
	if count.Load() != 1 {
		t.Fatalf("other object store spawned %d ls-tree processes, want 1", count.Load())
	}
}

func TestGitObjectAnswerCacheSkipsFailedAndTruncatedAnswers(t *testing.T) {
	requireSnapshotGit(t)
	repo, snapshot := preparedCandidateSnapshot(t)
	missing := strings.Repeat("0", len(snapshot.BaseTree))
	isolation, cleanup, err := isolatedImmutableTreeGit(t.Context(), repo)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cleanup() })
	count := countGitSubcommand(t, "diff")
	for range 2 {
		if _, err := runGitIsolated(t.Context(), repo, isolation, nil, "diff", "--raw", "-z", missing, snapshot.CandidateTree, "--"); err == nil {
			t.Fatal("diff over a missing tree succeeded")
		}
		if _, err := runGitLimited(t.Context(), repo, isolation, nil, 1, "diff", "--raw", "-z", snapshot.BaseTree, snapshot.CandidateTree, "--"); err == nil {
			t.Fatal("diff over the output limit succeeded")
		}
	}
	if count.Load() != 4 {
		t.Fatalf("failed and truncated answers spawned %d diff processes, want 4", count.Load())
	}
}

func TestGitObjectAnswerCacheBypassesNonObjectArguments(t *testing.T) {
	requireSnapshotGit(t)
	repo := initSnapshotRepo(t)
	count := countGitSubcommand(t, "ls-tree")
	for range 2 {
		if _, err := runGit(t.Context(), repo, nil, nil, "ls-tree", "-r", "-z", "HEAD"); err != nil {
			t.Fatal(err)
		}
	}
	if count.Load() != 2 {
		t.Fatalf("symbolic revision spawned %d ls-tree processes, want 2", count.Load())
	}
}
