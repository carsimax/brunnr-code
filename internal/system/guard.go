package system

import (
	"errors"
	"fmt"
	"runtime"
)

var ErrUnsupportedPlatform = errors.New("unsupported platform")

// IsSupportedPlatform reports whether brunnr-code runs on goos/goarch.
// Brunnr-Code is a personal tool that supports only macOS on Apple Silicon.
func IsSupportedPlatform(goos, goarch string) bool {
	return goos == "darwin" && goarch == "arm64"
}

func EnsureCurrentPlatformSupported() error {
	return EnsureSupportedPlatform(runtime.GOOS, runtime.GOARCH)
}

func EnsureSupportedPlatform(goos, goarch string) error {
	if IsSupportedPlatform(goos, goarch) {
		return nil
	}

	return fmt.Errorf("%w: only macOS on Apple Silicon (darwin/arm64) is supported (detected %s/%s)", ErrUnsupportedPlatform, goos, goarch)
}
