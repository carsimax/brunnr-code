package system

import (
	"errors"
	"strings"
	"testing"
)

func TestEnsureSupportedPlatformAllowsAppleSilicon(t *testing.T) {
	if err := EnsureSupportedPlatform("darwin", "arm64"); err != nil {
		t.Fatalf("expected darwin/arm64 to be supported, got %v", err)
	}
}

func TestEnsureSupportedPlatformRejectsEverythingElse(t *testing.T) {
	for _, tc := range []struct{ goos, goarch string }{
		{"darwin", "amd64"},
		{"linux", "arm64"},
		{"linux", "amd64"},
		{"windows", "amd64"},
		{"windows", "arm64"},
		{"freebsd", "arm64"},
	} {
		t.Run(tc.goos+"/"+tc.goarch, func(t *testing.T) {
			err := EnsureSupportedPlatform(tc.goos, tc.goarch)
			if !errors.Is(err, ErrUnsupportedPlatform) {
				t.Fatalf("expected ErrUnsupportedPlatform, got %v", err)
			}
			want := "only macOS on Apple Silicon (darwin/arm64) is supported (detected " + tc.goos + "/" + tc.goarch + ")"
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("error %q does not contain %q", err.Error(), want)
			}
		})
	}
}
