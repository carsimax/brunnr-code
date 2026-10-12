package app

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/carsimax/brunnr-code/internal/system"
	"github.com/carsimax/brunnr-code/internal/update"
	"github.com/carsimax/brunnr-code/internal/update/upgrade"
)

func TestRunUpdate_ReturnsErrorWhenChecksFail(t *testing.T) {
	origCheckAll := updateCheckAll
	t.Cleanup(func() {
		updateCheckAll = origCheckAll
	})

	updateCheckAll = func(context.Context, system.PlatformProfile) []update.UpdateResult {
		return []update.UpdateResult{{
			Tool:   update.ToolInfo{Name: "engram"},
			Status: update.CheckFailed,
		}}
	}

	var buf bytes.Buffer
	err := runUpdate(context.Background(), system.PlatformProfile{OS: "darwin", PackageManager: "brew"}, &buf)
	if err == nil {
		t.Fatal("runUpdate() error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), "update check failed for: engram") {
		t.Fatalf("runUpdate() error = %v, want update check failure", err)
	}

	out := buf.String()
	if strings.Contains(out, "All tools are up to date!") {
		t.Fatalf("runUpdate() output incorrectly claimed tools are up to date:\n%s", out)
	}
	if !strings.Contains(out, "Update check incomplete") {
		t.Fatalf("runUpdate() output missing incomplete check warning:\n%s", out)
	}
}

func TestRunUpgrade_ReturnsErrorBeforeExecutingWhenAllChecksFail(t *testing.T) {
	origCheckFiltered := updateCheckFiltered
	origUpgradeExecute := upgradeExecute
	origUpgradeExecuteWithOptions := upgradeExecuteWithOptions
	t.Cleanup(func() {
		updateCheckFiltered = origCheckFiltered
		upgradeExecute = origUpgradeExecute
		upgradeExecuteWithOptions = origUpgradeExecuteWithOptions
	})

	called := false
	updateCheckFiltered = func(context.Context, system.PlatformProfile, []string) []update.UpdateResult {
		return []update.UpdateResult{
			{
				Tool:   update.ToolInfo{Name: "engram"},
				Status: update.CheckFailed,
			},
		}
	}
	upgradeExecute = func(context.Context, []update.UpdateResult, system.PlatformProfile, string, bool, ...io.Writer) upgrade.UpgradeReport {
		called = true
		return upgrade.UpgradeReport{}
	}
	upgradeExecuteWithOptions = func(context.Context, []update.UpdateResult, system.PlatformProfile, string, bool, upgrade.ExecuteOptions) upgrade.UpgradeReport {
		called = true
		return upgrade.UpgradeReport{}
	}

	// Issue #535: runUpgrade now consumes a structured upgradeArgs value
	// parsed once in RunArgs, not raw CLI args. The check must run with the
	// forwarded tool filter, and execution must be skipped on check failure.
	var buf bytes.Buffer
	err := runUpgrade(context.Background(), upgradeArgs{noBackup: true}, system.DetectionResult{System: system.SystemInfo{Profile: system.PlatformProfile{OS: "darwin", PackageManager: "brew", Supported: true}}}, &buf)
	if err == nil {
		t.Fatal("runUpgrade() error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), "update check failed for: engram") {
		t.Fatalf("runUpgrade() error = %v, want update check failure", err)
	}
	if called {
		t.Fatal("runUpgrade() executed upgrades despite failed checks")
	}

	out := buf.String()
	if !strings.Contains(out, "Update Check") {
		t.Fatalf("runUpgrade() output missing check report:\n%s", out)
	}
	if strings.Contains(out, "All tools are up to date!") {
		t.Fatalf("runUpgrade() output incorrectly claimed tools are up to date:\n%s", out)
	}
	if strings.Contains(out, "Upgrade\n") {
		t.Fatalf("runUpgrade() should stop before rendering upgrade report:\n%s", out)
	}
}

// Partial checks must preserve useful results without upgrading failed tools.
func TestPartialUpdateChecks(t *testing.T) {
	for _, healthyStatus := range []update.UpdateStatus{update.UpdateAvailable, update.UpToDate} {
		for _, command := range []string{"update", "upgrade", "dry-run", "upgrade-failure"} {
			t.Run(string(healthyStatus)+"/"+command, func(t *testing.T) {
				home := t.TempDir()
				t.Setenv("HOME", home)
				t.Setenv("USERPROFILE", home)
				results := []update.UpdateResult{
					{Tool: update.ToolInfo{Name: "engram"}, Status: update.CheckFailed},
					{Tool: update.ToolInfo{Name: "gga"}, InstalledVersion: "1.0.0", LatestVersion: "2.0.0", Status: healthyStatus},
				}
				origAll, origFiltered, origExecute := updateCheckAll, updateCheckFiltered, upgradeExecuteWithOptions
				t.Cleanup(func() {
					updateCheckAll, updateCheckFiltered, upgradeExecuteWithOptions = origAll, origFiltered, origExecute
				})
				updateCheckAll = func(context.Context, system.PlatformProfile) []update.UpdateResult { return results }
				updateCheckFiltered = func(context.Context, system.PlatformProfile, []string) []update.UpdateResult { return results }
				calls := 0
				upgradeExecuteWithOptions = func(_ context.Context, got []update.UpdateResult, _ system.PlatformProfile, gotHome string, dryRun bool, opts upgrade.ExecuteOptions) upgrade.UpgradeReport {
					calls++
					if !reflect.DeepEqual(got, results) || gotHome != home || dryRun != (command == "dry-run") || !opts.SkipBackup {
						t.Fatalf("executor arguments = %v, %q, %v, %+v", got, gotHome, dryRun, opts)
					}
					if command == "upgrade-failure" {
						return upgrade.UpgradeReport{Results: []upgrade.ToolUpgradeResult{{ToolName: "gga", Status: upgrade.UpgradeFailed, Err: fmt.Errorf("upgrade failure")}}}
					}
					return upgrade.UpgradeReport{}
				}
				var out bytes.Buffer
				profile := system.PlatformProfile{OS: "darwin", PackageManager: "brew", Supported: true}
				var err error
				if command == "update" {
					err = runUpdate(context.Background(), profile, &out)
				} else {
					err = runUpgrade(context.Background(), upgradeArgs{dryRun: command == "dry-run", noBackup: true}, system.DetectionResult{System: system.SystemInfo{Profile: profile}}, &out)
				}
				if command == "upgrade-failure" {
					if err == nil || !strings.Contains(err.Error(), "upgrade failed for \"gga\"") {
						t.Fatalf("error = %v, want actual upgrade failure", err)
					}
				} else if err != nil {
					t.Fatalf("partial checks returned error: %v", err)
				}
				wantCalls := 1
				if command == "update" {
					wantCalls = 0
				}
				if calls != wantCalls {
					t.Fatalf("executor calls = %d, want %d", calls, wantCalls)
				}
				text := out.String()
				for _, want := range []string{"[!!] engram", "check failed", "gga"} {
					if !strings.Contains(text, want) {
						t.Fatalf("missing %q in output:\n%s", want, text)
					}
				}
				if healthyStatus == update.UpdateAvailable && !strings.Contains(text, "1 update(s) available. 1 check(s) failed.") {
					t.Fatalf("missing mixed summary:\n%s", text)
				}
				if healthyStatus == update.UpToDate && !strings.Contains(text, "Update check incomplete") {
					t.Fatalf("missing partial warning:\n%s", text)
				}
				if strings.Contains(text, "All tools are up to date!") {
					t.Fatalf("misleading output:\n%s", text)
				}
			})
		}
	}
}

func envContains(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

// TestRunUpgrade_ForwardsParsedArgsOnceWithoutReparsing verifies the issue #535
// contract: runUpgrade consumes the structured upgradeArgs (parsed once in
// RunArgs) and forwards the flags/filters to updateCheckFiltered and
// upgradeExecuteWithOptions exactly once. It must NOT reparse raw CLI args
// (there are none to reparse), and unsupported flags can never reach it.
func TestRunUpgrade_ForwardsParsedArgsOnceWithoutReparsing(t *testing.T) {
	origCheckFiltered := updateCheckFiltered
	origUpgradeExecuteWithOptions := upgradeExecuteWithOptions
	t.Cleanup(func() {
		updateCheckFiltered = origCheckFiltered
		upgradeExecuteWithOptions = origUpgradeExecuteWithOptions
	})

	var checkFilters []string
	checkCalls := 0
	updateCheckFiltered = func(_ context.Context, _ system.PlatformProfile, filters []string) []update.UpdateResult {
		checkCalls++
		checkFilters = filters
		return []update.UpdateResult{{Tool: update.ToolInfo{Name: "gentle-ai"}, Status: update.UpToDate}}
	}

	var execDryRun, execSkipBackup bool
	execCalls := 0
	upgradeExecuteWithOptions = func(_ context.Context, _ []update.UpdateResult, _ system.PlatformProfile, _ string, dryRun bool, opts upgrade.ExecuteOptions) upgrade.UpgradeReport {
		execCalls++
		execDryRun = dryRun
		execSkipBackup = opts.SkipBackup
		return upgrade.UpgradeReport{}
	}

	home := t.TempDir()
	setupMockHome(t, home)

	var buf bytes.Buffer
	err := runUpgrade(context.Background(), upgradeArgs{dryRun: true, noBackup: true, toolFilter: []string{"engram", "gga"}}, system.DetectionResult{System: system.SystemInfo{Profile: system.PlatformProfile{OS: "darwin", PackageManager: "brew", Supported: true}}}, &buf)
	if err != nil {
		t.Fatalf("runUpgrade() error = %v, want nil", err)
	}
	if checkCalls != 1 {
		t.Fatalf("updateCheckFiltered called %d times, want exactly 1", checkCalls)
	}
	if !reflect.DeepEqual(checkFilters, []string{"engram", "gga"}) {
		t.Fatalf("updateCheckFiltered filters = %#v, want [engram gga] forwarded once", checkFilters)
	}
	if execCalls != 1 {
		t.Fatalf("upgradeExecuteWithOptions called %d times, want exactly 1", execCalls)
	}
	if !execDryRun {
		t.Errorf("upgradeExecuteWithOptions dryRun = false, want true (forwarded once)")
	}
	if !execSkipBackup {
		t.Errorf("upgradeExecuteWithOptions SkipBackup = false, want true (forwarded once)")
	}
}

// TestRunUpgrade_NonGentleAIUpgradeDoesNotPrintDoctorAdvisory verifies that
// upgrading a tool other than gentle-ai (e.g. engram, gga) does NOT trigger
// the doctor advisory. The advisory is gated on gentle-ai specifically.
func TestRunUpgrade_NonGentleAIUpgradeDoesNotPrintDoctorAdvisory(t *testing.T) {
	origCheckFiltered := updateCheckFiltered
	origUpgradeExecuteWithOptions := upgradeExecuteWithOptions
	t.Cleanup(func() {
		updateCheckFiltered = origCheckFiltered
		upgradeExecuteWithOptions = origUpgradeExecuteWithOptions
	})

	updateCheckFiltered = func(context.Context, system.PlatformProfile, []string) []update.UpdateResult {
		return []update.UpdateResult{{
			Tool:             update.ToolInfo{Name: "engram", InstallMethod: update.InstallBinary},
			InstalledVersion: "0.5.0",
			LatestVersion:    "0.5.1",
			Status:           update.UpdateAvailable,
		}}
	}
	upgradeExecuteWithOptions = func(context.Context, []update.UpdateResult, system.PlatformProfile, string, bool, upgrade.ExecuteOptions) upgrade.UpgradeReport {
		return upgrade.UpgradeReport{Results: []upgrade.ToolUpgradeResult{{
			ToolName:   "engram",
			OldVersion: "0.5.0",
			NewVersion: "0.5.1",
			Status:     upgrade.UpgradeSucceeded,
		}}}
	}

	var buf bytes.Buffer
	err := runUpgrade(context.Background(), upgradeArgs{}, system.DetectionResult{System: system.SystemInfo{Profile: system.PlatformProfile{OS: "darwin", PackageManager: "brew", Supported: true}}}, &buf)
	if err != nil {
		t.Fatalf("runUpgrade() error = %v", err)
	}
	if strings.Contains(buf.String(), "ecosystem health after upgrade") {
		t.Fatalf("non-gentle-ai upgrade must NOT print post-upgrade doctor advisory:\n%s", buf.String())
	}
}
