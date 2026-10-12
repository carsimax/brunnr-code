package cli

import (
	"testing"

	"github.com/carsimax/brunnr-code/internal/installcmd"
	"github.com/carsimax/brunnr-code/internal/model"
	"github.com/carsimax/brunnr-code/internal/system"
)

// windowsProfile is a convenience profile for Windows tests that exercise the
// npm preflight (scoop is the package manager reported by Windows scoop users).
var windowsProfile = system.PlatformProfile{OS: "windows", PackageManager: "winget", Supported: true}

// checkDependenciesStep only preflights Pi now: Pi is the sole agent that
// still executes anything on the user's behalf (its own already-present `pi`
// subcommands, which need npm/pnpm — see agentInstallStep in run.go). Every
// other agent's "not installed" outcome is a printed refusal that needs no
// local dependency at all, so this step no longer blocks the pipeline for
// their missing npm/uv — see agentInstallStepRefusesMissingRuntime tests in
// run_integration_test.go for the refusal itself.

func TestCheckDependenciesStepDoesNotRequireUVForOtherAgents(t *testing.T) {
	// Claude Code requires npm (not uv), and Claude Code is not Pi, so this
	// step never even reaches a preflight check for it — proving uv is not
	// required for npm agents this step no longer preflights at all.
	restore := installcmd.OverrideLookPath(func(file string) (string, error) {
		if file == "npm" {
			return "/usr/local/bin/npm", nil
		}
		return "", errNotFound{}
	})
	t.Cleanup(restore)

	step := checkDependenciesStep{
		id:      "prepare:check-dependencies",
		profile: system.PlatformProfile{OS: "darwin", PackageManager: "brew", Supported: true},
		selection: model.Selection{
			Agents: []model.AgentID{model.AgentClaudeCode},
		},
	}

	if err := step.Run(); err != nil {
		t.Fatalf("checkDependenciesStep.Run() unexpected error = %v", err)
	}
}

type errNotFound struct{}

func (errNotFound) Error() string { return "not found" }

// --- npm preflight tests (Bug A: node/npm missing gate) ---
//
// These four tests used to prove that a missing npm blocked the pipeline
// with a clear, actionable error before `npm install -g …` ran and surfaced
// a cryptic "exec: npm: executable file not found" (the Windows regression
// this file was originally written for). gentle-ai no longer runs that
// command at all for non-Pi agents — the refusal in agentInstallStep prints
// it instead — so there is nothing left to preflight here, and no exec to
// crash: the cryptic-error scenario these guarded against cannot happen
// anymore, for a stronger reason than a nicer error message.

func TestCheckDependenciesStepDoesNotBlockClaudeCodeOnMissingNpm(t *testing.T) {
	restore := installcmd.OverrideLookPath(func(file string) (string, error) {
		if file == "npm" {
			return "", errNotFound{}
		}
		return "/usr/bin/" + file, nil
	})
	t.Cleanup(restore)

	step := checkDependenciesStep{
		id:      "prepare:check-dependencies",
		profile: windowsProfile,
		selection: model.Selection{
			Agents: []model.AgentID{model.AgentClaudeCode},
		},
	}

	if err := step.Run(); err != nil {
		t.Fatalf("checkDependenciesStep.Run() unexpected error = %v — Claude Code is not Pi, so a missing npm is not this step's concern anymore", err)
	}
}

func TestCheckDependenciesStepPassesWhenNpmPresent(t *testing.T) {
	restore := installcmd.OverrideLookPath(func(file string) (string, error) {
		// npm is present, everything else is too
		return "/usr/bin/" + file, nil
	})
	t.Cleanup(restore)

	for _, agent := range []model.AgentID{
		model.AgentClaudeCode,
	} {
		agent := agent
		t.Run(string(agent), func(t *testing.T) {
			step := checkDependenciesStep{
				id:      "prepare:check-dependencies",
				profile: windowsProfile,
				selection: model.Selection{
					Agents: []model.AgentID{agent},
				},
			}
			if err := step.Run(); err != nil {
				t.Fatalf("checkDependenciesStep.Run() unexpected error for %s with npm present: %v", agent, err)
			}
		})
	}
}
