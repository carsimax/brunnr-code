package reviewtransaction

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
)

// Git's path answers are cached process-wide only to skip re-spawning
// `git rev-parse`; they are never trusted on their own. Every hit is re-proved
// with the same filesystem identity checks an uncached answer must pass, plus
// stamps of every config file Git read, taken before Git was asked; a hit that
// fails them is dropped and Git is asked again.
var (
	reviewRepositoryRootCache        reviewGitAnswerCache[reviewRepositoryRootAnswer]
	reviewRepositoryDirectoriesCache reviewGitAnswerCache[reviewRepositoryDirectoriesAnswer]
	reviewObjectFormatCache          reviewGitAnswerCache[reviewObjectFormatAnswer]
	reviewGitConfigFilesCache        reviewGitAnswerCache[[]string]
)

// reviewGitAnswerHook, when set by a test, runs after Git answered a root or
// directories query and before the answer is cached.
var reviewGitAnswerHook func()

func afterReviewGitAnswer() {
	if reviewGitAnswerHook != nil {
		reviewGitAnswerHook()
	}
}

// reviewGitAnswerCacheLimit and reviewGitAnswerCacheByteLimit bound each
// cache; an answer above a quarter of the byte limit is never cached.
// Production runs one command per process, so only long-lived processes such
// as test binaries ever reach them.
const (
	reviewGitAnswerCacheLimit     = 256
	reviewGitAnswerCacheByteLimit = 64 << 20
)

type reviewGitAnswerCache[V any] struct {
	mu      sync.Mutex
	entries map[string]reviewGitCachedAnswer[V]
	bytes   int
}

type reviewGitCachedAnswer[V any] struct {
	value V
	size  int
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
	answer, ok := cache.entries[key]
	return answer.value, ok
}

// store caches value, whose size in bytes is size. A full cache is cleared
// rather than evicted entry by entry; replacing a key's answer frees its
// bytes first, so a refresh never clears the answers stored alongside it.
func (cache *reviewGitAnswerCache[V]) store(key string, value V, size int) {
	key = reviewGitAnswerKey(key)
	cache.mu.Lock()
	defer cache.mu.Unlock()
	cache.deleteLocked(key)
	if size > reviewGitAnswerCacheByteLimit/4 {
		return
	}
	if cache.entries == nil || len(cache.entries) >= reviewGitAnswerCacheLimit || cache.bytes+size > reviewGitAnswerCacheByteLimit {
		cache.entries, cache.bytes = make(map[string]reviewGitCachedAnswer[V]), 0
	}
	cache.entries[key] = reviewGitCachedAnswer[V]{value: value, size: size}
	cache.bytes += size
}

func (cache *reviewGitAnswerCache[V]) drop(key string) {
	key = reviewGitAnswerKey(key)
	cache.mu.Lock()
	defer cache.mu.Unlock()
	cache.deleteLocked(key)
}

func (cache *reviewGitAnswerCache[V]) deleteLocked(key string) {
	if answer, ok := cache.entries[key]; ok {
		cache.bytes -= answer.size
		delete(cache.entries, key)
	}
}

// reviewGitFileStamp records a Git configuration file that can change Git's
// answer without touching the .git control entry (core.worktree, commondir,
// extensions.objectformat, safe.directory). A nil info records that the file
// was absent. Stamps follow symlinks, so an edit to a linked dotfile counts.
type reviewGitFileStamp struct {
	path string
	info fs.FileInfo
}

func captureReviewGitFileStamps(paths ...string) ([]reviewGitFileStamp, bool) {
	stamps := make([]reviewGitFileStamp, 0, len(paths))
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, false
		}
		stamps = append(stamps, reviewGitFileStamp{path: path, info: info})
	}
	return stamps, true
}

func reviewGitFileStampsUnchanged(stamps []reviewGitFileStamp) bool {
	for _, stamp := range stamps {
		current, err := os.Stat(stamp.path)
		if stamp.info == nil {
			if !errors.Is(err, fs.ErrNotExist) {
				return false
			}
			continue
		}
		if err != nil || !os.SameFile(stamp.info, current) || current.Mode() != stamp.info.Mode() {
			return false
		}
		// A device such as /dev/null has no content to change, and its
		// timestamps move with every write anyone makes to it.
		if current.Mode().IsRegular() && (current.Size() != stamp.info.Size() || !current.ModTime().Equal(stamp.info.ModTime())) {
			return false
		}
	}
	return true
}

// reviewGitConfigFiles lists the global and system config files Git reads
// under the review Git environment. Git is asked once per environment; the
// answer depends only on that environment, which keys the cache.
func reviewGitConfigFiles(ctx context.Context) ([]string, bool) {
	if files, ok := reviewGitConfigFilesCache.load(""); ok {
		return files, true
	}
	var files []string
	for _, scope := range []string{"GIT_CONFIG_GLOBAL", "GIT_CONFIG_SYSTEM"} {
		output, err := runGit(ctx, os.TempDir(), nil, nil, "var", scope)
		if err != nil {
			// GIT_CONFIG_NOSYSTEM makes Git name no system file and fail.
			if scope == "GIT_CONFIG_SYSTEM" && os.Getenv("GIT_CONFIG_NOSYSTEM") != "" && ctx.Err() == nil {
				continue
			}
			// Git older than 2.42 cannot name its config files; cache nothing.
			return nil, false
		}
		for _, line := range strings.Split(string(output), "\n") {
			if line = strings.TrimSuffix(line, "\r"); line != "" {
				files = append(files, line)
			}
		}
	}
	reviewGitConfigFilesCache.store("", files, 0)
	return files, true
}

// captureReviewGitConfigStamps stamps, before Git is asked, every config file
// that can change its answer for the repository behind gitControl: the global
// and system files, and the worktree's commondir, config.worktree and common
// config. Stamping first means an edit racing the query can only make the
// answer look stale, never fresh. It also returns the git and common
// directories the stamps assume, so a caller can refuse an answer that
// resolved others.
// shortcut: files pulled in by include.path or includeIf are not stamped;
// bind them if review answers ever come to depend on included config.
func captureReviewGitConfigStamps(
	ctx context.Context,
	gitControl reviewRepositoryControlIdentity,
) (stamps []reviewGitFileStamp, gitDir, commonDir string, ok bool) {
	globalFiles, ok := reviewGitConfigFiles(ctx)
	if !ok {
		return nil, "", "", false
	}
	gitDir = gitControl.path
	if gitControl.info.Mode().IsRegular() {
		target, found := strings.CutPrefix(strings.TrimRight(string(gitControl.payload), "\r\n"), "gitdir: ")
		if !found {
			return nil, "", "", false
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(gitControl.path), target)
		}
		gitDir = target
	}
	// commondir is stamped before it is read, so a rewrite after the read
	// fails the stamp instead of leaving a stale common directory unwatched.
	files := append(slices.Clone(globalFiles), filepath.Join(gitDir, "commondir"), filepath.Join(gitDir, "config.worktree"))
	if stamps, ok = captureReviewGitFileStamps(files...); !ok {
		return nil, "", "", false
	}
	commonDir = gitDir
	if content, err := os.ReadFile(filepath.Join(gitDir, "commondir")); err == nil {
		commonDir = strings.TrimRight(string(content), "\r\n")
		if !filepath.IsAbs(commonDir) {
			commonDir = filepath.Join(gitDir, commonDir)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, "", "", false
	}
	config, ok := captureReviewGitFileStamps(filepath.Join(commonDir, "config"))
	if !ok {
		return nil, "", "", false
	}
	return append(stamps, config...), gitDir, commonDir, true
}

func sameReviewDirectory(left, right string) bool {
	leftInfo, leftErr := os.Stat(left)
	rightInfo, rightErr := os.Stat(right)
	return leftErr == nil && rightErr == nil && os.SameFile(leftInfo, rightInfo)
}

// reviewRepositoryRootAnswer is Git's --show-toplevel answer for one canonical
// requested path, bound to the .git control entry it was found through and to
// the config Git read.
type reviewRepositoryRootAnswer struct {
	root       string
	gitControl reviewRepositoryControlIdentity
	stamps     []reviewGitFileStamp
}

func cachedReviewRepositoryRoot(abs string) (string, bool) {
	answer, ok := reviewRepositoryRootCache.load(abs)
	if !ok {
		return "", false
	}
	if validateReviewRepositoryControl(answer.gitControl) != nil || !reviewGitFileStampsUnchanged(answer.stamps) ||
		!reviewRepositoryRootStillNearest(abs, answer.root) {
		reviewRepositoryRootCache.drop(abs)
		return "", false
	}
	return answer.root, true
}

// prepareReviewRepositoryRoot captures, before Git is asked, what a root
// answer for abs will be bound to: the repository Git's discovery walk should
// find, its .git control entry and its config stamps.
func prepareReviewRepositoryRoot(ctx context.Context, abs string) (reviewRepositoryRootAnswer, bool) {
	dir, ok := reviewNearestGitControlDirectory(abs)
	if !ok {
		return reviewRepositoryRootAnswer{}, false
	}
	control, err := captureReviewRepositoryControl(filepath.Join(dir, ".git"), true, true)
	if err != nil {
		return reviewRepositoryRootAnswer{}, false
	}
	stamps, _, _, ok := captureReviewGitConfigStamps(ctx, control)
	return reviewRepositoryRootAnswer{root: dir, gitControl: control, stamps: stamps}, ok
}

// rememberReviewRepositoryRoot caches root only when Git answered the
// repository prepared answer was captured for; otherwise its stamps watch
// another repository's config (core.worktree moved the top level).
func rememberReviewRepositoryRoot(abs, root string, prepared reviewRepositoryRootAnswer) {
	if !sameReviewDirectory(prepared.root, root) {
		return
	}
	prepared.root = root
	reviewRepositoryRootCache.store(abs, prepared, 0)
}

// reviewNearestGitControlDirectory follows Git's discovery walk from abs to
// the first directory holding a .git entry. It fails when a directory on the
// way holds a HEAD (Git may stop there at a bare repository) or the walk
// cannot be proved.
func reviewNearestGitControlDirectory(abs string) (string, bool) {
	for dir := abs; ; {
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
			return dir, true
		} else if !errors.Is(err, fs.ErrNotExist) {
			return "", false
		}
		if _, err := os.Lstat(filepath.Join(dir, "HEAD")); !errors.Is(err, fs.ErrNotExist) {
			return "", false
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

// reviewRepositoryRootStillNearest reports whether Git's discovery walk from
// abs would still stop at root: no directory below root may have gained a
// .git entry (a nested repository) or a HEAD (a bare repository directory).
func reviewRepositoryRootStillNearest(abs, root string) bool {
	dir, ok := reviewNearestGitControlDirectory(abs)
	return ok && sameReviewDirectory(dir, root)
}

// reviewRepositoryDirectoriesAnswer is Git's top/common/git directory answer
// for one canonical root, stored only after a lease built from it passed
// every identity check.
type reviewRepositoryDirectoriesAnswer struct {
	gitControl reviewRepositoryControlIdentity
	paths      [3]string
	stamps     []reviewGitFileStamp
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

// rememberReviewRepositoryDirectories caches directories with the stamps
// captureReviewGitConfigStamps took before Git was asked, provided Git
// resolved the git and common directories those stamps assumed.
func rememberReviewRepositoryDirectories(
	root string,
	gitControl reviewRepositoryControlIdentity,
	directories []reviewRepositoryDirectoryIdentity,
	stamps []reviewGitFileStamp,
	stampedGitDir, stampedCommonDir string,
) {
	if !sameReviewDirectory(stampedCommonDir, directories[1].path) || !sameReviewDirectory(stampedGitDir, directories[2].path) {
		return
	}
	reviewRepositoryDirectoriesCache.store(root, reviewRepositoryDirectoriesAnswer{
		gitControl: gitControl,
		paths:      [3]string{directories[0].path, directories[1].path, directories[2].path},
		stamps:     stamps,
	}, 0)
}

// reviewObjectFormatAnswer is the object format of one common directory. It is
// bound to the directory's file identity, its config file and the global and
// system config, so a repository re-created at the same path is asked again.
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
	globalFiles, stamped := reviewGitConfigFiles(ctx)
	var stamps []reviewGitFileStamp
	if stamped {
		stamps, stamped = captureReviewGitFileStamps(append(slices.Clone(globalFiles), filepath.Join(key, "config"))...)
	}
	format, err := gitObjectFormat(ctx, identity.RepositoryRoot)
	if err != nil {
		return "", err
	}
	if statErr == nil && stamped {
		reviewObjectFormatCache.store(key, reviewObjectFormatAnswer{commonDir: commonDir, stamps: stamps, format: format}, 0)
	}
	return format, nil
}
