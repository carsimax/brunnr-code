package reviewtransaction

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
)

// Git's path answers are cached process-wide only to skip re-spawning
// `git rev-parse`; they are never trusted on their own. Every hit is re-proved
// with the same filesystem identity checks an uncached answer must pass, and a
// hit that fails them is dropped and Git is asked again.
var (
	reviewRepositoryRootCache        reviewGitAnswerCache[reviewRepositoryRootAnswer]
	reviewRepositoryDirectoriesCache reviewGitAnswerCache[reviewRepositoryDirectoriesAnswer]
	reviewObjectFormatCache          reviewGitAnswerCache[reviewObjectFormatAnswer]
)

// reviewGitAnswerCacheLimit bounds each cache. Production runs one command per
// process, so only long-lived processes such as test binaries ever reach it.
const reviewGitAnswerCacheLimit = 256

type reviewGitAnswerCache[V any] struct {
	mu      sync.Mutex
	entries map[string]V
}

// reviewGitAnswerKey binds a path to the environment Git would run with: a
// different git on PATH or a different global config (safe.directory,
// core.worktree) may answer differently for the same path.
func reviewGitAnswerKey(path string) string {
	digest := sha256.New()
	for _, entry := range sanitizedGitEnvironment(os.Environ(), nil) {
		digest.Write([]byte(entry))
		digest.Write([]byte{0})
	}
	digest.Write([]byte{0})
	digest.Write([]byte(path))
	return string(digest.Sum(nil))
}

func (cache *reviewGitAnswerCache[V]) load(key string) (V, bool) {
	key = reviewGitAnswerKey(key)
	cache.mu.Lock()
	defer cache.mu.Unlock()
	value, ok := cache.entries[key]
	return value, ok
}

func (cache *reviewGitAnswerCache[V]) store(key string, value V) {
	key = reviewGitAnswerKey(key)
	cache.mu.Lock()
	defer cache.mu.Unlock()
	// Only a new key may clear the cache, so refreshing a live answer never
	// evicts the answers stored alongside it.
	if _, exists := cache.entries[key]; cache.entries == nil || !exists && len(cache.entries) >= reviewGitAnswerCacheLimit {
		cache.entries = make(map[string]V)
	}
	cache.entries[key] = value
}

func (cache *reviewGitAnswerCache[V]) drop(key string) {
	key = reviewGitAnswerKey(key)
	cache.mu.Lock()
	defer cache.mu.Unlock()
	delete(cache.entries, key)
}

// reviewGitFileStamp records a Git configuration file that can change Git's
// answer without touching the .git control entry (core.worktree, commondir,
// extensions.objectformat). A nil info records that the file was absent.
type reviewGitFileStamp struct {
	path string
	info fs.FileInfo
}

func captureReviewGitFileStamps(paths ...string) ([]reviewGitFileStamp, bool) {
	stamps := make([]reviewGitFileStamp, 0, len(paths))
	for _, path := range paths {
		info, err := os.Lstat(path)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, false
		}
		stamps = append(stamps, reviewGitFileStamp{path: path, info: info})
	}
	return stamps, true
}

func reviewGitFileStampsUnchanged(stamps []reviewGitFileStamp) bool {
	for _, stamp := range stamps {
		current, err := os.Lstat(stamp.path)
		if stamp.info == nil {
			if !errors.Is(err, fs.ErrNotExist) {
				return false
			}
			continue
		}
		if err != nil || !os.SameFile(stamp.info, current) || current.Mode() != stamp.info.Mode() ||
			current.Size() != stamp.info.Size() || !current.ModTime().Equal(stamp.info.ModTime()) {
			return false
		}
	}
	return true
}

// reviewRepositoryRootAnswer is Git's --show-toplevel answer for one canonical
// requested path, bound to the .git control entry it was found through.
type reviewRepositoryRootAnswer struct {
	root       string
	gitControl reviewRepositoryControlIdentity
}

func cachedReviewRepositoryRoot(abs string) (string, bool) {
	answer, ok := reviewRepositoryRootCache.load(abs)
	if !ok {
		return "", false
	}
	if validateReviewRepositoryControl(answer.gitControl) != nil || !reviewRepositoryRootStillNearest(abs, answer.root) {
		reviewRepositoryRootCache.drop(abs)
		return "", false
	}
	return answer.root, true
}

func rememberReviewRepositoryRoot(abs, root string) {
	control, err := captureReviewRepositoryControl(filepath.Join(root, ".git"), true, true)
	if err != nil {
		return
	}
	reviewRepositoryRootCache.store(abs, reviewRepositoryRootAnswer{root: root, gitControl: control})
}

// reviewRepositoryRootStillNearest reports whether Git's discovery walk from
// abs would still stop at root: no directory below root may have gained a
// .git entry (a nested repository) or a HEAD (a bare repository directory).
// shortcut: a core.worktree change is not seen here; the lease's own
// directories check rejects it, so it surfaces as an error, not a wrong root.
func reviewRepositoryRootStillNearest(abs, root string) bool {
	rootInfo, err := os.Stat(root)
	if err != nil {
		return false
	}
	for dir := abs; ; {
		info, err := os.Stat(dir)
		if err != nil {
			return false
		}
		if os.SameFile(info, rootInfo) {
			return true
		}
		for _, name := range []string{".git", "HEAD"} {
			if _, err := os.Lstat(filepath.Join(dir, name)); !errors.Is(err, fs.ErrNotExist) {
				return false
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false
		}
		dir = parent
	}
}

// reviewRepositoryDirectoriesAnswer is Git's top/common/git directory answer
// for one canonical root, stored only after a lease built from it passed
// every identity check.
type reviewRepositoryDirectoriesAnswer struct {
	gitControl reviewRepositoryControlIdentity
	paths      [3]string
	stamps     []reviewGitFileStamp
}

func reviewRepositoryDirectoryStampPaths(directories []reviewRepositoryDirectoryIdentity) []string {
	commonDir, gitDir := directories[1].path, directories[2].path
	return []string{
		filepath.Join(commonDir, "config"),
		filepath.Join(gitDir, "config.worktree"),
		filepath.Join(gitDir, "commondir"),
	}
}

// cachedReviewRepositoryDirectories re-captures the cached directories exactly
// as resolveReviewRepositoryDirectories would, provided the .git control entry
// captured for this open is the one the answer was proved against.
func cachedReviewRepositoryDirectories(
	root string,
	gitControl reviewRepositoryControlIdentity,
) ([]reviewRepositoryDirectoryIdentity, bool) {
	answer, ok := reviewRepositoryDirectoriesCache.load(root)
	if !ok {
		return nil, false
	}
	cached := answer.gitControl
	if cached.path != gitControl.path || cached.info.Mode().Type() != gitControl.info.Mode().Type() ||
		!os.SameFile(cached.info, gitControl.info) || cached.hasPayload != gitControl.hasPayload ||
		!bytes.Equal(cached.payload, gitControl.payload) || !reviewGitFileStampsUnchanged(answer.stamps) {
		return nil, false
	}
	directories := make([]reviewRepositoryDirectoryIdentity, len(answer.paths))
	for index, path := range answer.paths {
		directory, err := captureReviewRepositoryDirectory(path)
		if err != nil {
			return nil, false
		}
		directories[index] = directory
	}
	return directories, true
}

func rememberReviewRepositoryDirectories(
	root string,
	gitControl reviewRepositoryControlIdentity,
	directories []reviewRepositoryDirectoryIdentity,
	stamps []reviewGitFileStamp,
) {
	reviewRepositoryDirectoriesCache.store(root, reviewRepositoryDirectoriesAnswer{
		gitControl: gitControl,
		paths:      [3]string{directories[0].path, directories[1].path, directories[2].path},
		stamps:     stamps,
	})
}

// reviewObjectFormatAnswer is the object format of one common directory. It is
// bound to the directory's file identity and its config file, so a repository
// re-created at the same path is asked again.
type reviewObjectFormatAnswer struct {
	commonDir fs.FileInfo
	stamps    []reviewGitFileStamp
	format    string
}

func repositoryObjectFormat(ctx context.Context, identity reviewRepositoryIdentityRecord) (string, error) {
	key := identity.GitCommonDir
	commonDir, statErr := os.Stat(key)
	if statErr == nil {
		if answer, ok := reviewObjectFormatCache.load(key); ok {
			if os.SameFile(answer.commonDir, commonDir) && reviewGitFileStampsUnchanged(answer.stamps) {
				return answer.format, nil
			}
			reviewObjectFormatCache.drop(key)
		}
	}
	// Stamp before asking Git, so a config change racing the query can only
	// make the stored answer look stale, never make a stale answer look fresh.
	stamps, stamped := captureReviewGitFileStamps(filepath.Join(key, "config"))
	format, err := gitObjectFormat(ctx, identity.RepositoryRoot)
	if err != nil {
		return "", err
	}
	if statErr == nil && stamped {
		reviewObjectFormatCache.store(key, reviewObjectFormatAnswer{commonDir: commonDir, stamps: stamps, format: format})
	}
	return format, nil
}
