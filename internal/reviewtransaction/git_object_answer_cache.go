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
	"strconv"
	"strings"
)

// Answers to read-only Git queries whose only revisions are full object IDs
// are cached process-wide: an object ID names its content, so the same query
// over the same IDs, object store, environment and isolation config answers
// the same, and only the object store's identity needs re-proving on a hit.
var reviewGitObjectAnswerCache reviewGitAnswerCache[reviewGitObjectAnswer]

type reviewGitObjectAnswer struct {
	objects fs.FileInfo
	output  []byte
}

// reviewGitObjectQuery is one cacheable Git run: its key, and the object
// store, captured before Git ran, that the answer is bound to.
type reviewGitObjectQuery struct {
	key     string
	objects fs.FileInfo
}

// reviewGitObjectRecheckKey marks a context whose object queries must be
// answered by Git: an object-store identity check cannot see pruned objects or
// a removed alternate, so evidence validation re-proves its trees.
type reviewGitObjectRecheckKey struct{}

func withReviewGitObjectRecheck(ctx context.Context) context.Context {
	return context.WithValue(ctx, reviewGitObjectRecheckKey{}, true)
}

func (query reviewGitObjectQuery) load(ctx context.Context) ([]byte, bool) {
	if ctx.Err() != nil || ctx.Value(reviewGitObjectRecheckKey{}) != nil {
		return nil, false
	}
	answer, ok := reviewGitObjectAnswerCache.load(query.key)
	if !ok || !os.SameFile(answer.objects, query.objects) {
		return nil, false
	}
	return bytes.Clone(answer.output), true
}

func (query reviewGitObjectQuery) store(output []byte) {
	reviewGitObjectAnswerCache.store(query.key, reviewGitObjectAnswer{objects: query.objects, output: bytes.Clone(output)}, len(output))
}

// reviewGitObjectQueryFor reports whether a Git run is a cacheable read-only
// object query, and its key. Anything it cannot prove depends only on the
// named objects and the bound config bypasses the cache.
func reviewGitObjectQueryFor(
	ctx context.Context,
	repo string,
	extraEnv []string,
	stdin []byte,
	outputOffset, outputLimit int,
	isolateConfig, rejectStderr, rejectOverflow bool,
	args []string,
) (reviewGitObjectQuery, bool) {
	// A ranged or overflow-tolerant read is not a complete answer.
	if stdin != nil || outputOffset != 0 || !rejectOverflow {
		return reviewGitObjectQuery{}, false
	}
	var key strings.Builder
	write := func(parts ...string) {
		for _, part := range parts {
			key.WriteString(part)
			key.WriteByte(0)
		}
	}
	write(repo, strconv.FormatBool(isolateConfig), strconv.FormatBool(rejectStderr), strconv.Itoa(outputLimit))
	index := 0
	for index < len(args) && (args[index] == "--no-pager" || args[index] == "-c" && index+1 < len(args)) {
		if args[index] == "--no-pager" {
			write(args[index])
			index++
			continue
		}
		name, value, _ := strings.Cut(args[index+1], "=")
		switch strings.ToLower(name) {
		case "core.attributesfile":
			// The attributes file is a fresh temp path per view; bind its content.
			digest, ok := reviewGitFileDigest(value)
			if !ok {
				return reviewGitObjectQuery{}, false
			}
			write("-c", name+"="+digest)
		case "color.ui", "diff.external":
			write("-c", args[index+1])
		default:
			// Other settings may name files whose content the key does not bind.
			return reviewGitObjectQuery{}, false
		}
		index += 2
	}
	if index >= len(args) {
		return reviewGitObjectQuery{}, false
	}
	isolatedView := false
	for _, entry := range extraEnv {
		if strings.HasPrefix(entry, "GIT_DIR=") {
			isolatedView = true
		}
	}
	// Outside the bare isolated view, tree diffs read worktree attributes and
	// repository config; only name lists and tree listings are immune to both.
	switch subcommand := args[index]; {
	case subcommand == "ls-tree":
	case subcommand == "diff-tree" && (isolatedView || slices.Contains(args[index:], "--name-only")):
	case subcommand == "diff" && isolatedView:
	default:
		return reviewGitObjectQuery{}, false
	}
	var objectIDs []string
	pathspecs := false
	write(args[index])
	for _, arg := range args[index+1:] {
		write(arg)
		switch {
		case pathspecs:
		case arg == "--":
			pathspecs = true
		case strings.HasPrefix(arg, "--output") || strings.HasPrefix(arg, "-O"):
			// These write or read a file the key does not bind.
			return reviewGitObjectQuery{}, false
		case strings.HasPrefix(arg, "-"):
		default:
			objectIDs = append(objectIDs, arg)
		}
	}
	if len(objectIDs) == 0 {
		return reviewGitObjectQuery{}, false
	}
	identity, err := reviewRepositoryIdentity(ctx, repo)
	if err != nil {
		return reviewGitObjectQuery{}, false
	}
	format, err := repositoryObjectFormat(ctx, identity)
	if err != nil {
		return reviewGitObjectQuery{}, false
	}
	// A shorter hex string is an abbreviation whose resolution can change as
	// objects are added; only a full ID of this repository's format is fixed.
	idLength := 40
	if format == "sha256" {
		idLength = 64
	}
	for _, objectID := range objectIDs {
		if !isFullHexObjectID(objectID, idLength) {
			return reviewGitObjectQuery{}, false
		}
	}
	objectsDir := filepath.Join(identity.GitCommonDir, "objects")
	sawObjectDirectory := false
	for _, entry := range extraEnv {
		name, value, _ := strings.Cut(entry, "=")
		switch name {
		case "GIT_OBJECT_DIRECTORY":
			if filepath.Clean(value) != objectsDir {
				return reviewGitObjectQuery{}, false
			}
			sawObjectDirectory = true
			write(entry)
		case "GIT_CONFIG_SYSTEM", "GIT_CONFIG_GLOBAL":
			digest, ok := reviewGitFileDigest(value)
			if !ok {
				return reviewGitObjectQuery{}, false
			}
			write(name + "=" + digest)
		case "GIT_DIR":
			digest, ok := reviewIsolatedGitDirDigest(value)
			if !ok {
				return reviewGitObjectQuery{}, false
			}
			write(name + "=" + digest)
		default:
			write(entry)
		}
	}
	if isolatedView && !sawObjectDirectory {
		return reviewGitObjectQuery{}, false
	}
	objects, err := os.Stat(objectsDir)
	if err != nil {
		return reviewGitObjectQuery{}, false
	}
	return reviewGitObjectQuery{key: key.String(), objects: objects}, true
}

func isFullHexObjectID(value string, length int) bool {
	if len(value) != length {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func reviewGitFileDigest(path string) (string, bool) {
	content, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	digest := sha256.Sum256(content)
	return string(digest[:]), true
}

// reviewIsolatedGitDirDigest binds a per-view temp Git directory by the
// content that can change an object query's answer. Only the bare view that
// isolatedImmutableTreeGitWithAttributesFile writes qualifies: a non-bare one
// would read worktree attributes, and info/ or commondir would add state the
// digest does not cover.
func reviewIsolatedGitDirDigest(gitDir string) (string, bool) {
	for _, name := range []string{"info", "commondir"} {
		if _, err := os.Lstat(filepath.Join(gitDir, name)); !errors.Is(err, fs.ErrNotExist) {
			return "", false
		}
	}
	config, err := os.ReadFile(filepath.Join(gitDir, "config"))
	if err != nil || !bytes.Contains(config, []byte("\tbare = true\n")) {
		return "", false
	}
	head, err := os.ReadFile(filepath.Join(gitDir, "HEAD"))
	if err != nil {
		return "", false
	}
	digest := sha256.Sum256(append(append(config, 0), head...))
	return string(digest[:]), true
}
