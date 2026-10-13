package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunDoctor_AssetVersionRemediation(t *testing.T) {
	stubDoctorToolProbe(t)
	setStdioProbeForTest(t, nil)
	t.Setenv(engramHealthEnvVar, "")
	origExec, origLook := osExecutableDoctor, lookPathFn
	origHome, origDirs, origAvail := osUserHomeDirDoctor, pathDirsFn, availableBytesFn
	t.Cleanup(func() {
		osExecutableDoctor, lookPathFn = origExec, origLook
		osUserHomeDirDoctor, pathDirsFn, availableBytesFn = origHome, origDirs, origAvail
	})

	home := t.TempDir()
	statePath := filepath.Join(home, ".brunnr-code", "state.json")
	if err := os.MkdirAll(filepath.Dir(statePath), 0o755); err != nil {
		t.Fatal(err)
	}
	writeDoctorEngramConfig(t, home, "engram", []string{"mcp", "--tools=agent"})
	osUserHomeDirDoctor = func() (string, error) { return home, nil }
	pathDirsFn = func() []string { return nil }
	availableBytesFn = func(string) (int64, error) { return 1024 * 1024 * 1024, nil }

	simple := filepath.ToSlash(filepath.Join(home, "pinned", "brunnr-code"))
	spaces := filepath.Join(home, "pinned build", "brunnr-code")
	apostrophe := filepath.Join(home, "user's build", "brunnr-code")
	other := filepath.Join(home, "other-install", "brunnr-code")
	// The temp dir may hold unsafe bytes, so the POSIX token comes from the product helper.
	simpleSync := managedAssetsExecutableToken(simple) + " sync"
	for _, tc := range []struct {
		name, invoked, pathCopy, want string
		execErr                       error
		matching                      bool
	}{
		{name: "different PATH build", invoked: simple, pathCopy: other, want: simpleSync},
		{name: "same PATH build", invoked: simple, pathCopy: simple, want: simpleSync},
		{name: "POSIX spaces", invoked: spaces, pathCopy: other, want: "'" + spaces + "' sync"},
		{name: "POSIX apostrophe", invoked: apostrophe, pathCopy: other, want: "'" + strings.ReplaceAll(apostrophe, "'", `'\''`) + "' sync"},
		{name: "lookup failure", pathCopy: other, execErr: errors.New("unavailable")},
		{name: "relative identity", invoked: "brunnr-code", pathCopy: other},
		{name: "multiline identity", invoked: simple + "\nunsafe", pathCopy: other},
		{name: "matching assets", invoked: simple, pathCopy: simple, matching: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			version := "v0.9.0"
			if tc.matching {
				version = AppVersion
			}
			before := []byte(fmt.Sprintf(`{"installed_binary_version":%q}`, version))
			if err := os.WriteFile(statePath, before, 0o600); err != nil {
				t.Fatal(err)
			}
			osExecutableDoctor = func() (string, error) { return tc.invoked, tc.execErr }
			lookPathFn = func(name string) (string, error) {
				if name == "brunnr-code" {
					return tc.pathCopy, nil
				}
				return filepath.Join(home, name), nil
			}
			var output bytes.Buffer
			if err := RunDoctor(context.Background(), &output); err != nil {
				t.Fatalf("RunDoctor: %v", err)
			}
			var line string
			for _, candidate := range strings.Split(output.String(), "\n") {
				if strings.Contains(candidate, "installed:asset_version") {
					line = candidate
				}
			}
			want := "installed assets match running binary version (" + AppVersion + ")"
			if !tc.matching {
				want = "installed assets were configured by brunnr-code v0.9.0, but running binary is " + AppVersion
				if tc.want == "" {
					want += " — cannot determine the absolute invoked executable path; run sync using the absolute path of this running build to update installed assets"
				} else {
					want += " — run `" + tc.want + "` to update installed assets"
					if tc.invoked != tc.pathCopy {
						want += "; brunnr-code on PATH resolves to " + tc.pathCopy + ", which differs from the running executable; use the invoked path above"
					}
				}
			}
			if !strings.HasSuffix(line, want) {
				t.Errorf("asset check:\n%s\nwant suffix:\n%s", line, want)
			}
			icon := "[!!]"
			if tc.matching {
				icon = "[ok]"
			}
			if !strings.Contains(line, icon) {
				t.Errorf("missing %s: %s", icon, line)
			}
			after, err := os.ReadFile(statePath)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("doctor modified state: %q, %v", after, err)
			}
		})
	}
}
