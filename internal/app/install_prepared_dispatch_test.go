package app

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/carsimax/brunnr-code/internal/system"
)

func TestRunArgsPreparedInstallPreservesValidDispatch(t *testing.T) {
	for _, tc := range []struct {
		name       string
		args       []string
		components string
	}{
		// The managed persona is mandatory, so every explicit list resolves it.
		{"implicit persona", []string{"--component", "permissions"}, "permissions,persona"},
		{"managed persona", []string{"--component", "persona"}, "persona"},
		{"skill", []string{"--component", "skills", "--skill", "go-testing"}, "persona,skills"},
		{"skills alias", []string{"--components", "skills", "--skills", "go-testing"}, "persona,skills"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setupMockHome(t, t.TempDir())
			oldDetect, oldEnsure := detectSystem, ensureCurrentOSSupported
			t.Cleanup(func() { detectSystem, ensureCurrentOSSupported = oldDetect, oldEnsure })
			ensureCurrentOSSupported = func() error { return nil }
			detectSystem = func(context.Context) (system.DetectionResult, error) {
				return system.DetectionResult{System: system.SystemInfo{Supported: true, Profile: system.PlatformProfile{OS: "windows", Supported: true}}}, nil
			}
			var out bytes.Buffer
			args := append([]string{"install", "--agent", "claude-code", "--dry-run"}, tc.args...)
			if err := RunArgs(args, &out); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out.String(), "Components order: "+tc.components+"\n") {
				t.Fatalf("unexpected plan output: %q", out.String())
			}
		})
	}
}
