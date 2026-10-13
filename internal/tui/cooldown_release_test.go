package tui

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/carsimax/brunnr-code/internal/state"
	"github.com/carsimax/brunnr-code/internal/system"
	"github.com/carsimax/brunnr-code/internal/update"
	tea "github.com/charmbracelet/bubbletea"
)

func TestCooldownReleaseWelcomeDoesNotClaimCurrent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	now := time.Now()
	recent := now.Add(-time.Minute)
	if err := state.Write(home, state.InstallState{LastUpdateCheck: &recent}); err != nil {
		t.Fatal(err)
	}
	m := NewModel(system.DetectionResult{}, "1.0.0")
	next, _ := m.Update(m.Init()())
	m = next.(Model)
	view := m.View()
	if strings.Contains(view, "Upgrade + Sync (up to date)") {
		t.Fatalf("contradictory welcome: %s", view)
	}
	saved, err := state.Read(home)
	if err != nil || saved.LastUpdateCheck == nil || !saved.LastUpdateCheck.Equal(recent) {
		t.Fatalf("skipped check changed timestamp: %+v, %v", saved, err)
	}
}

func TestFailedUpdateCheckDoesNotClaimCurrent(t *testing.T) {
	m := NewModel(system.DetectionResult{}, "1.0.0")
	next, _ := m.Update(UpdateCheckResultMsg{Results: []update.UpdateResult{{Status: update.CheckFailed}}})
	if strings.Contains(next.(Model).View(), "Upgrade + Sync (up to date)") {
		t.Fatal("failed check rendered as up to date")
	}
}

func TestCooldownReleaseRefreshOnUpgradeEntry(t *testing.T) {
	for _, cursor := range []int{1} {
		t.Run("upgrade-and-sync", func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			now := time.Now()
			recent := now.Add(-time.Minute)
			if err := state.Write(home, state.InstallState{LastUpdateCheck: &recent}); err != nil {
				t.Fatal(err)
			}
			oldCheck, oldNow := updateCheckFn, tuiNowFn
			t.Cleanup(func() { updateCheckFn, tuiNowFn = oldCheck, oldNow })
			tuiNowFn = func() time.Time { return now }
			calls := 0
			updateCheckFn = func(context.Context, system.PlatformProfile) []update.UpdateResult {
				calls++
				return []update.UpdateResult{makeUpdateResult(update.UpdateAvailable, "https://example.com/release")}
			}
			m := NewModel(system.DetectionResult{}, "1.0.0")
			next, _ := m.Update(m.Init()())
			m = next.(Model)
			if calls != 0 || m.UpdateCheckState != update.CheckSkipped {
				t.Fatal("startup did not respect cooldown")
			}
			m.Cursor = cursor
			next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
			m = next.(Model)
			if m.UpdateCheckDone || !strings.Contains(m.View(), "Checking for updates") {
				t.Fatal("upgrade entry did not start fresh check")
			}
			commands := cmd().(tea.BatchMsg)
			next, _ = m.Update(commands[1]())
			m = next.(Model)
			if calls != 1 || !m.UpdateCheckDone || !update.HasUpdates(m.UpdateResults) {
				t.Fatalf("fresh check not offered: calls=%d model=%+v", calls, m)
			}
			if !strings.Contains(m.View(), "Updates available") {
				t.Fatal("combined upgrade confirmation did not offer updates")
			}
			saved, err := state.Read(home)
			if err != nil || saved.LastUpdateCheck == nil || !saved.LastUpdateCheck.Equal(now) {
				t.Fatalf("successful forced check not persisted: %+v %v", saved, err)
			}
		})
	}
}

func TestCooldownReleaseNavigationWhileStartupPending(t *testing.T) {
	m := NewModel(system.DetectionResult{}, "1.0.0")
	m.Cursor = 1
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	next, cmd := m.Update(UpdateCheckResultMsg{State: update.CheckSkipped})
	m = next.(Model)
	if m.Screen != ScreenUpgradeSync || m.UpdateCheckDone || cmd == nil {
		t.Fatal("late skipped result left upgrade without a refresh")
	}
}

func TestCooldownReleaseLateFailedStartupRefreshesOnce(t *testing.T) {
	for _, cursor := range []int{1} {
		for _, succeeds := range []bool{false, true} {
			name := "upgrade-and-sync"
			if succeeds {
				name += "/refresh-succeeds"
			} else {
				name += "/refresh-fails"
			}
			t.Run(name, func(t *testing.T) {
				home := t.TempDir()
				t.Setenv("HOME", home)
				oldCheck := updateCheckFn
				t.Cleanup(func() { updateCheckFn = oldCheck })
				calls := 0
				updateCheckFn = func(context.Context, system.PlatformProfile) []update.UpdateResult {
					calls++
					if calls == 2 && succeeds {
						return []update.UpdateResult{makeUpdateResult(update.UpdateAvailable, "https://example.com/release")}
					}
					return []update.UpdateResult{{Status: update.CheckFailed}}
				}
				m := NewModel(system.DetectionResult{}, "1.0.0")
				startup := m.Init()
				m.Cursor = cursor
				next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
				m = next.(Model)
				wantScreen := ScreenUpgradeSync
				next, cmd := m.Update(startup())
				m = next.(Model)
				if calls != 1 || m.Screen != wantScreen || m.UpdateCheckDone || cmd == nil || !strings.Contains(m.View(), "Checking for updates") {
					t.Fatal("late startup failure did not start one fresh check")
				}
				forced := cmd().(tea.BatchMsg)[1]
				next, cmd = m.Update(forced())
				m = next.(Model)
				if calls != 2 || !m.UpdateCheckDone || m.Screen != wantScreen || cmd != nil {
					t.Fatalf("forced result started another retry: calls=%d done=%v screen=%v cmd=%v", calls, m.UpdateCheckDone, m.Screen, cmd != nil)
				}
				if succeeds {
					if !update.HasUpdates(m.UpdateResults) || m.UpdateCheckState != update.CheckCompleted {
						t.Fatal("successful refresh did not expose updates")
					}
				} else {
					if m.UpdateCheckState != update.CheckUnsuccessful {
						t.Fatal("failed forced check lost its failure state")
					}
					if _, err := state.Read(home); !os.IsNotExist(err) {
						t.Fatalf("failed checks changed persisted state: %v", err)
					}
				}
			})
		}
	}
}

func TestCooldownReleaseVerifiedCheckRemainsCurrent(t *testing.T) {
	m := NewModel(system.DetectionResult{}, "1.0.0")
	next, _ := m.Update(UpdateCheckResultMsg{Results: []update.UpdateResult{makeUpdateResult(update.UpToDate, "")}})
	m = next.(Model)
	if !strings.Contains(m.View(), "Upgrade + Sync (up to date)") {
		t.Fatal("verified up-to-date label disappeared")
	}
	m.Cursor = 1
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if next.(Model).Screen != ScreenUpgradeSync || cmd != nil {
		t.Fatal("verified results unexpectedly refreshed")
	}
}
