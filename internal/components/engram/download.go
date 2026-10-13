package engram

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/carsimax/brunnr-code/internal/components/filemerge"
	"github.com/carsimax/brunnr-code/internal/system"
)

const (
	engramOwner = "Gentleman-Programming"
	engramRepo  = "engram"
	engramName  = "engram"

	// engramReleaseOS and engramReleaseArch select the only release asset
	// brunnr-code installs: it supports macOS on Apple Silicon only.
	engramReleaseOS   = "darwin"
	engramReleaseArch = "arm64"
)

// Package-level vars for testability.
var (
	engramHTTPClient    = &http.Client{Timeout: 5 * time.Minute}
	engramGitHubBaseURL = "https://github.com"
	engramInstallDirFn  = engramInstallDir
	engramChecksumURLFn = engramChecksumURL
)

// engramCoreTagPattern matches only plain semver tags (vX.Y.Z) that identify
// core engram binary releases. The Gentleman-Programming/engram repository also
// publishes gentle-engram npm and pi releases under tags like
// "gentle-engram vX.Y.Z" or "pi-vX.Y.Z" in the same release stream. This
// pattern intentionally excludes those so a gentle-engram/pi tag can never be
// selected as the core engram binary version. It mirrors the ReleaseTagPattern
// used by the update-check path in internal/update/registry.go.
const engramCoreTagPattern = `^v[0-9]+\.[0-9]+\.[0-9]+$`

// DownloadLatestBinary fetches the latest engram release from GitHub and
// installs it. Only the darwin/arm64 asset is fetched; the profile does not
// choose the asset. It returns the full path to the installed binary.
//
// Checksum verification is mandatory: the install fails if checksums.txt is
// unavailable, if the archive is not listed, or if
// the digest does not match.
//
// This is the Engram installation method.
func DownloadLatestBinary(_ system.PlatformProfile) (string, error) {
	ctx := context.Background()

	// 1. Fetch the latest version tag from GitHub API. Only tags matching the
	// core engram pattern (vX.Y.Z) are considered; gentle-engram/pi tags are
	// excluded so the download and update-check paths share the same source of truth.
	version, err := fetchLatestEngramVersion()
	if err != nil {
		return "", fmt.Errorf("fetch latest engram version: %w", err)
	}

	// 2. Determine the archive URL.
	assetURL := engramAssetURL(engramGitHubBaseURL, version)
	archiveName := engramArchiveName(version)
	checksumURL := engramChecksumURLFn(engramGitHubBaseURL, version)

	// 3. Determine install directory.
	installDir := engramInstallDirFn()
	if err := os.MkdirAll(installDir, 0o755); err != nil {
		return "", fmt.Errorf("create engram install dir %q: %w", installDir, err)
	}

	// 4. Download archive to a temp dir so we can verify before extracting.
	outPath := filepath.Join(installDir, engramName)

	tmpDir, err := os.MkdirTemp("", "brunnr-code-engram-*")
	if err != nil {
		return "", fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	archivePath := filepath.Join(tmpDir, archiveName)
	actualDigest, err := engramDownloadToFile(ctx, assetURL, archivePath)
	if err != nil {
		return "", fmt.Errorf("download engram archive: %w", err)
	}

	// 5. Verify checksum — fail closed if checksums.txt is unavailable or mismatched.
	checksumsContent, err := engramFetchChecksums(ctx, checksumURL)
	if err != nil {
		return "", fmt.Errorf("checksum verification failed: checksums.txt unavailable: %w", err)
	}
	expectedDigest, err := engramExpectedChecksumFor(checksumsContent, archiveName)
	if err != nil {
		return "", fmt.Errorf("checksum verification failed: %w", err)
	}
	if actualDigest != expectedDigest {
		return "", fmt.Errorf("checksum mismatch for %s:\n  expected: %s\n  got:      %s",
			archiveName, expectedDigest, actualDigest)
	}

	// 6. Extract the verified binary.
	f, err := os.Open(archivePath)
	if err != nil {
		return "", fmt.Errorf("open archive: %w", err)
	}
	defer f.Close()

	if err := extractBinaryFromTarGz(f, engramName, outPath); err != nil {
		return "", fmt.Errorf("extract engram tar.gz: %w", err)
	}

	return outPath, nil
}

// engramCoreTagRE is the compiled form of engramCoreTagPattern, used to filter
// GitHub release tags so only core engram binary releases are selected.
var engramCoreTagRE = regexp.MustCompile(engramCoreTagPattern)

// fetchLatestEngramVersion queries the GitHub Releases API for the latest
// core engram binary release and returns the version string (without leading "v").
// Only releases whose tag matches engramCoreTagPattern (vX.Y.Z) are considered,
// so gentle-engram/pi tags published in the same release stream are ignored.
func fetchLatestEngramVersion() (string, error) {
	token := githubToken()
	version, status, err := fetchLatestEngramVersionRequest(token)
	if err == nil {
		return version, nil
	}

	// GitHub Actions injects a repository-scoped GITHUB_TOKEN into CI. When that
	// token is forwarded into E2E runs, the public engram releases
	// endpoint can respond 401/403 for a different repository. Retry anonymously
	// before failing because the release metadata is public.
	if token != "" && (status == http.StatusUnauthorized || status == http.StatusForbidden) {
		version, _, retryErr := fetchLatestEngramVersionRequest("")
		if retryErr == nil {
			return version, nil
		}
	}

	return "", err
}

func fetchLatestEngramVersionRequest(token string) (string, int, error) {
	apiURL := fmt.Sprintf("%s/repos/%s/%s/releases/latest",
		engramAPIBaseURL(), engramOwner, engramRepo)

	req, err := http.NewRequest(http.MethodGet, apiURL, nil)
	if err != nil {
		return "", 0, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := engramHTTPClient.Do(req)
	if err != nil {
		return "", 0, fmt.Errorf("call GitHub API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", resp.StatusCode, fmt.Errorf("GitHub API returned HTTP %d", resp.StatusCode)
	}

	var release struct {
		TagName string             `json:"tag_name"`
		Assets  *[]json.RawMessage `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return "", resp.StatusCode, fmt.Errorf("decode release JSON: %w", err)
	}

	// Reject tags that don't match the core engram pattern (e.g. gentle-engram/pi-v* tags).
	// Fall through to the release-list scan when /latest points at a non-core release.
	if !engramCoreTagRE.MatchString(release.TagName) {
		fallbackVersion, fallbackStatus, err := fetchLatestEngramVersionWithAssets(token)
		if err == nil {
			return fallbackVersion, resp.StatusCode, nil
		}
		if token != "" && (fallbackStatus == http.StatusUnauthorized || fallbackStatus == http.StatusForbidden) {
			fallbackVersion, _, retryErr := fetchLatestEngramVersionWithAssets("")
			if retryErr == nil {
				return fallbackVersion, resp.StatusCode, nil
			}
		}
		return "", resp.StatusCode, err
	}

	version := strings.TrimPrefix(release.TagName, "v")
	if version == "" {
		return "", resp.StatusCode, fmt.Errorf("empty tag_name in GitHub release response")
	}

	// Older tests and non-GitHub-compatible mocks may omit assets entirely; in
	// that case keep the historical latest-release behavior. GitHub returns an
	// explicit assets array, so skip releases that do not publish core engram
	// binaries (for example pi-v* gentle-engram package releases, which are
	// separate from core engram binary releases).
	if release.Assets != nil && !hasEngramBinaryAsset(*release.Assets) {
		fallbackVersion, fallbackStatus, err := fetchLatestEngramVersionWithAssets(token)
		if err == nil {
			return fallbackVersion, resp.StatusCode, nil
		}
		if token != "" && (fallbackStatus == http.StatusUnauthorized || fallbackStatus == http.StatusForbidden) {
			fallbackVersion, _, retryErr := fetchLatestEngramVersionWithAssets("")
			if retryErr == nil {
				return fallbackVersion, resp.StatusCode, nil
			}
		}
		return "", resp.StatusCode, err
	}

	return version, resp.StatusCode, nil
}

func hasEngramBinaryAsset(assets []json.RawMessage) bool {
	for _, raw := range assets {
		var asset struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(raw, &asset); err == nil && strings.HasPrefix(asset.Name, engramRepo+"_") {
			return true
		}
	}
	return false
}

// engramReleasePageSize is the number of releases requested per page when
// paginating the GitHub Releases list. GitHub's maximum is 100; 20 is
// sufficient for typical cadence while keeping response payloads small.
const engramReleasePageSize = 20

// engramReleaseMaxPages caps the pagination loop so it can never run forever.
// At 20 releases/page this covers 100 releases — enough runway even when the
// Gentleman-Programming/engram repo publishes many pi-v*/gentle-engram entries
// between core vX.Y.Z releases.
const engramReleaseMaxPages = 5

func fetchLatestEngramVersionWithAssets(token string) (string, int, error) {
	lastStatus := 0

	for page := 1; page <= engramReleaseMaxPages; page++ {
		apiURL := fmt.Sprintf("%s/repos/%s/%s/releases?per_page=%d&page=%d",
			engramAPIBaseURL(), engramOwner, engramRepo, engramReleasePageSize, page)

		req, err := http.NewRequest(http.MethodGet, apiURL, nil)
		if err != nil {
			return "", 0, fmt.Errorf("build releases request: %w", err)
		}
		req.Header.Set("Accept", "application/vnd.github+json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}

		resp, err := engramHTTPClient.Do(req)
		if err != nil {
			return "", lastStatus, fmt.Errorf("call GitHub releases API: %w", err)
		}

		if resp.StatusCode != http.StatusOK {
			status := resp.StatusCode
			resp.Body.Close()
			return "", status, fmt.Errorf("GitHub releases API returned HTTP %d", status)
		}
		lastStatus = resp.StatusCode

		var releases []struct {
			TagName    string `json:"tag_name"`
			Draft      bool   `json:"draft"`
			Prerelease bool   `json:"prerelease"`
			Assets     []struct {
				Name string `json:"name"`
			} `json:"assets"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
			resp.Body.Close()
			return "", lastStatus, fmt.Errorf("decode releases JSON: %w", err)
		}
		resp.Body.Close()

		// An empty page means GitHub has no more releases — stop early.
		if len(releases) == 0 {
			break
		}

		for _, release := range releases {
			if release.Draft || release.Prerelease || len(release.Assets) == 0 {
				continue
			}
			// Skip tags that don't match the core engram pattern (e.g. gentle-engram/pi-v* tags).
			if !engramCoreTagRE.MatchString(release.TagName) {
				continue
			}
			for _, asset := range release.Assets {
				if strings.HasPrefix(asset.Name, engramRepo+"_") {
					version := strings.TrimPrefix(release.TagName, "v")
					if version != "" {
						return version, lastStatus, nil
					}
				}
			}
		}
	}

	return "", lastStatus, fmt.Errorf("no engram release with downloadable binary assets found")
}

// githubToken returns a GitHub API token from the environment, if available.
// Checks GITHUB_TOKEN first, then GH_TOKEN (used by the gh CLI).
func githubToken() string {
	if t := os.Getenv("GITHUB_TOKEN"); t != "" {
		return t
	}
	return os.Getenv("GH_TOKEN")
}

// engramAPIBaseURL returns the GitHub API base URL for fetching release info.
// In tests, the mock server handles both API and download under the same URL,
// so we derive the API base from engramGitHubBaseURL.
func engramAPIBaseURL() string {
	base := engramGitHubBaseURL
	if strings.Contains(base, "127.0.0.1") || strings.Contains(base, "localhost") {
		return base
	}
	return "https://api.github.com"
}

// engramArchiveName returns the GoReleaser darwin/arm64 archive filename for
// the given version.
//
// Convention: engram_{version}_{os}_{arch}.tar.gz
func engramArchiveName(version string) string {
	return fmt.Sprintf("%s_%s_%s_%s.tar.gz", engramRepo, version, engramReleaseOS, engramReleaseArch)
}

// engramAssetURL constructs the download URL for the engram release asset.
func engramAssetURL(baseURL, version string) string {
	filename := engramArchiveName(version)
	return fmt.Sprintf("%s/%s/%s/releases/download/v%s/%s",
		baseURL, engramOwner, engramRepo, version, filename)
}

// engramChecksumURL constructs the GitHub Releases URL for checksums.txt.
func engramChecksumURL(baseURL, version string) string {
	return fmt.Sprintf("%s/%s/%s/releases/download/v%s/checksums.txt",
		baseURL, engramOwner, engramRepo, version)
}

// engramDownloadToFile downloads the resource at url to outPath and returns the
// SHA256 hex digest of the bytes that landed there.
//
// The digest is read back from outPath, not accumulated from the response body.
// A digest taken from the stream certifies its own copy: it matched the release
// manifest even when a write-back failure left the archive incomplete on disk,
// so verification confirmed corruption instead of catching it (#1998).
func engramDownloadToFile(ctx context.Context, url string, outPath string) (hexDigest string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	resp, err := engramHTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("download %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download %s: HTTP %d", url, resp.StatusCode)
	}

	// Create the parent at 0755 explicitly: the writer's own parent creation is
	// tuned for private config files, and a download directory on PATH is not one.
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return "", fmt.Errorf("create dir: %w", err)
	}
	result, err := filemerge.WriteStreamAtomic(outPath, resp.Body, 0o644)
	if err != nil {
		return "", fmt.Errorf("write %s: %w", outPath, err)
	}
	return result.Digest, nil
}

// engramFetchChecksums downloads checksums.txt from url and returns its content.
// Returns an error if the file cannot be fetched or the server returns non-200.
func engramFetchChecksums(ctx context.Context, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	resp, err := engramHTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch checksums.txt: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("checksums.txt: HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read checksums.txt: %w", err)
	}
	return string(data), nil
}

// engramExpectedChecksumFor parses checksums.txt content and returns the SHA256
// hex digest for filename. Returns an error if the filename is not listed.
//
// GoReleaser produces BSD-style checksums.txt: "<digest>  <filename>" per line.
func engramExpectedChecksumFor(content, filename string) (string, error) {
	for _, line := range strings.Split(content, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == filename {
			return fields[0], nil
		}
	}
	return "", fmt.Errorf("%q not listed in checksums.txt", filename)
}

// engramInstallDir returns the directory where the engram binary should be
// installed: /usr/local/bin, or ~/.local/bin when that is not writable.
func engramInstallDir() string {
	candidate := "/usr/local/bin"
	if isWritableDir(candidate) {
		return candidate
	}

	// Fallback to ~/.local/bin.
	home, err := os.UserHomeDir()
	if err != nil {
		return "/usr/local/bin"
	}
	return filepath.Join(home, ".local", "bin")
}

// isWritableDir reports whether the directory exists and the process can write to it.
func isWritableDir(dir string) bool {
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return false
	}
	tmp, err := os.CreateTemp(dir, ".engram-write-test-*")
	if err != nil {
		return false
	}
	tmp.Close()
	os.Remove(tmp.Name())
	return true
}

// extractBinaryFromTarGz reads a .tar.gz stream and extracts the first file
// whose base name matches binaryName, writing it to outPath.
func extractBinaryFromTarGz(r io.Reader, binaryName, outPath string) error {
	gr, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("open gzip: %w", err)
	}
	defer gr.Close()

	tr := tar.NewReader(gr)

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read tar: %w", err)
		}

		if filepath.Base(hdr.Name) == binaryName &&
			(hdr.Typeflag == tar.TypeReg || hdr.Typeflag == tar.TypeRegA) {
			return writeExecutable(tr, outPath)
		}
	}

	return fmt.Errorf("binary %q not found in archive", binaryName)
}

// writeExecutable writes a binary to outPath using an atomic rename to avoid
// ETXTBSY ("text file busy") errors when the target binary is
// currently running (e.g. engram as an MCP server). The rename trick works
// because os.Rename replaces the directory entry — the running process keeps
// its open file descriptor to the old inode, while new executions pick up
// the new binary.
//
// The staged file is synchronized before publication, so if recovery preserves
// the new name it also preserves complete content (#2216).
func writeExecutable(r io.Reader, outPath string) error {
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return fmt.Errorf("create parent dir: %w", err)
	}
	if _, err := filemerge.WriteStreamAtomic(outPath, r, 0o755); err != nil {
		return fmt.Errorf("write %s: %w", outPath, err)
	}
	return nil
}
