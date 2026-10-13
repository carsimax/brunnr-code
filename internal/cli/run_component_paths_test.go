package cli

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/carsimax/brunnr-code/internal/agents"
	"github.com/carsimax/brunnr-code/internal/backup"
	"github.com/carsimax/brunnr-code/internal/components/agentguidance"
	"github.com/carsimax/brunnr-code/internal/components/filemerge"
	"github.com/carsimax/brunnr-code/internal/components/reviewassets"
	"github.com/carsimax/brunnr-code/internal/model"
	"github.com/carsimax/brunnr-code/internal/pipeline"
	"github.com/carsimax/brunnr-code/internal/planner"
	"github.com/carsimax/brunnr-code/internal/system"
)

type failingPostApplyRollbackStep struct{ err error }

func (s failingPostApplyRollbackStep) ID() string      { return "failing-post-apply-rollback" }
func (s failingPostApplyRollbackStep) Run() error      { return nil }
func (s failingPostApplyRollbackStep) Rollback() error { return s.err }

func TestNativeReviewPostApplyErrorJoinsRollbackFailure(t *testing.T) {
	cause, rollbackFailure := errors.New("post-apply error"), errors.New("rollback error")
	orchestrator := pipeline.NewOrchestrator(pipeline.DefaultRollbackPolicy())
	execution := orchestrator.Execute(pipeline.StagePlan{Apply: []pipeline.Step{failingPostApplyRollbackStep{err: rollbackFailure}}})
	if execution.Err != nil {
		t.Fatal(execution.Err)
	}
	err := rollbackPostApplyError(orchestrator, execution, cause)
	if !errors.Is(err, cause) || !errors.Is(err, rollbackFailure) {
		t.Fatalf("joined error = %v", err)
	}
}

func TestNativeReviewPreservedAction(t *testing.T) {
	// Recovery examples must use supported parser syntax, with scope explicit.
	for _, runtime := range []string{"claude-code", "codex"} {
		for _, scope := range []string{"global", "workspace"} {
			t.Run(runtime+"/"+scope, func(t *testing.T) {
				flags, err := ParseSyncFlags([]string{"--agent", runtime, "--scope", scope})
				if err != nil {
					t.Fatal(err)
				}
				ids, err := asAgentIDs(flags.Agents)
				if err != nil || len(ids) != 1 || string(ids[0]) != runtime {
					t.Fatalf("recovery runtime = %v, %v", ids, err)
				}
				resolved, err := ResolveInstallScope(flags.Scope)
				if err != nil || string(resolved) != scope {
					t.Fatalf("recovery scope = %v, %v", resolved, err)
				}
			})
		}
	}
	for _, path := range []string{
		"/fixture/.claude/agents/jd-judge-a.md",
		"/fixture with spaces/.claude/agents/jd-judge-a.md",
		`C:\fixture with spaces\agents\jd-judge-a.md`,
	} {
		t.Run(path, func(t *testing.T) {
			got := nativeReviewPreservedAction(path)
			for _, want := range []string{
				"Native review agent " + path + " was preserved, not updated",
				"ownership cannot be verified",
				"missing ledger entry or differing recorded hash",
				"does not mean you customized the file",
				"Keeping it unchanged is valid",
				"back up this file and verify the backup",
				"remove only this warned file",
				"rerun your existing brunnr-code install or brunnr-code sync command",
				"same runtime, scope, and model choices",
				"Brunnr-Code will not adopt or delete it automatically",
			} {
				if !strings.Contains(got, want) {
					t.Errorf("action missing %q: %s", want, got)
				}
			}
			for _, unwanted := range []string{"unknown or modified", "merge changes into your copy"} {
				if strings.Contains(got, unwanted) {
					t.Errorf("action contains misleading advice %q: %s", unwanted, got)
				}
			}
		})
	}
}

func TestComponentPathsClaudeRetainsReviewAndRouting(t *testing.T) {
	home := t.TempDir()
	selection := model.Selection{Agents: []model.AgentID{model.AgentClaudeCode}}
	targets, err := backupTargets(home, "", ScopeGlobal, selection, planner.ResolvedPlan{Agents: selection.Agents})
	if err != nil {
		t.Fatal(err)
	}
	for _, relative := range []string{".claude/CLAUDE.md", ".claude/agents/review-risk.md", ".claude/agents/jd-judge-a.md"} {
		if !containsPath(targets, filepath.Join(home, relative)) {
			t.Errorf("retained Claude path missing: %s", relative)
		}
	}
}

func TestComponentPathsSkillsIncludesSelectedSkills(t *testing.T) {
	home := t.TempDir()
	selection := model.Selection{Skills: []model.SkillID{model.SkillGoTesting, model.SkillJudgmentDay}}
	paths := componentPathsWithWorkspaceScoped(home, "", ScopeGlobal, selection, resolveAdapters([]model.AgentID{model.AgentClaudeCode}), model.ComponentSkills)
	for _, skill := range []string{"go-testing", "judgment-day"} {
		want := filepath.Join(home, ".claude", "skills", skill, "SKILL.md")
		if !containsPath(paths, want) {
			t.Errorf("retained skill missing: %s", want)
		}
	}
}

func TestComponentPathsWorkspaceScopedSkillsUsesWorkspaceDir(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	adapters := resolveAdapters([]model.AgentID{model.AgentClaudeCode})
	selection := model.Selection{
		Skills: []model.SkillID{
			model.SkillGoTesting,
			model.SkillBranchPR,
		},
	}

	paths := componentPathsWithWorkspaceScoped(home, workspace, ScopeWorkspace, selection, adapters, model.ComponentSkills)

	for _, want := range []string{
		filepath.Join(workspace, ".claude", "skills", "go-testing", "SKILL.md"),
		filepath.Join(workspace, ".claude", "skills", "branch-pr", "SKILL.md"),
	} {
		if !containsPath(paths, want) {
			t.Fatalf("componentPathsWithWorkspaceScoped(skills,claude-code,workspace) missing workspace-scoped path %q\npaths=%v", want, paths)
		}
	}

	for _, unwanted := range []string{
		filepath.Join(home, ".claude", "skills", "go-testing", "SKILL.md"),
		filepath.Join(home, ".claude", "skills", "branch-pr", "SKILL.md"),
	} {
		if containsPath(paths, unwanted) {
			t.Fatalf("componentPathsWithWorkspaceScoped(skills,claude-code,workspace) must not include home-scoped path %q\npaths=%v", unwanted, paths)
		}
	}
}

// TestInstallWorkspaceScopeVerificationWithNoGlobalSkills verifies that
// post-apply verification succeeds when --scope=workspace is used and no
// global skill files exist. This is a regression test for issue #785:
// the verifier used to check home-scoped paths even when workspace scope
// was active, causing false failures when only workspace skills existed.
func TestInstallWorkspaceScopeVerificationWithNoGlobalSkills(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	adapters := resolveAdapters([]model.AgentID{model.AgentClaudeCode})
	selection := model.Selection{
		Skills: []model.SkillID{
			model.SkillGoTesting,
			model.SkillBranchPR,
		},
	}

	// Simulate workspace-scoped install: skills are written to workspace only.
	// The verification should check workspace paths, not home paths.
	paths := componentPathsWithWorkspaceScoped(home, workspace, ScopeWorkspace, selection, adapters, model.ComponentSkills)

	// Verify that workspace paths are included (these should exist after install).
	for _, want := range []string{
		filepath.Join(workspace, ".claude", "skills", "go-testing", "SKILL.md"),
		filepath.Join(workspace, ".claude", "skills", "branch-pr", "SKILL.md"),
	} {
		if !containsPath(paths, want) {
			t.Fatalf("workspace-scoped verification missing workspace path %q\npaths=%v", want, paths)
		}
	}

	// Verify that home paths are NOT included (these would cause false failures
	// if checked when only workspace skills exist).
	for _, unwanted := range []string{
		filepath.Join(home, ".claude", "skills", "go-testing", "SKILL.md"),
		filepath.Join(home, ".claude", "skills", "branch-pr", "SKILL.md"),
	} {
		if containsPath(paths, unwanted) {
			t.Fatalf("workspace-scoped verification must not check home path %q when scope=workspace\npaths=%v", unwanted, paths)
		}
	}
}

func TestComponentPathsEngramClaudeUsesUserRegistryAndPreservesWorkspaceScope(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	adapters := resolveAdapters([]model.AgentID{model.AgentClaudeCode})

	global := componentPathsWithWorkspaceScoped(home, "", ScopeGlobal, model.Selection{}, adapters, model.ComponentEngram)
	registry := filepath.Join(home, ".claude.json")
	legacy := filepath.Join(home, ".claude", "mcp", "engram.json")
	if !containsPath(global, registry) || containsPath(global, legacy) {
		t.Fatalf("global Engram paths must use only Claude's user registry; paths=%v", global)
	}

	workspacePaths := componentPathsWithWorkspaceScoped(home, workspace, ScopeWorkspace, model.Selection{}, adapters, model.ComponentEngram)
	workspaceLegacy := filepath.Join(workspace, ".claude", "mcp", "engram.json")
	if !containsPath(workspacePaths, workspaceLegacy) || containsPath(workspacePaths, filepath.Join(workspace, ".claude.json")) {
		t.Fatalf("workspace Engram paths must remain unchanged; paths=%v", workspacePaths)
	}
}

// TestComponentPathsEngramCodexIncludesConfigTOML verifies that componentPaths
// for ComponentEngram + Codex reports ~/.codex/config.toml as a backup target.
func TestComponentPathsEngramCodexIncludesConfigTOML(t *testing.T) {
	home := t.TempDir()
	adapters := resolveAdapters([]model.AgentID{model.AgentCodex})

	paths := componentPathsWithWorkspaceScoped(home, "", ScopeGlobal, model.Selection{}, adapters, model.ComponentEngram)

	want := filepath.Join(home, ".codex", "config.toml")
	if !containsPath(paths, want) {
		t.Fatalf("componentPaths(engram,codex) missing %q\npaths=%v", want, paths)
	}
}

// TestComponentPathsPermissionsCodexContributesNoPaths pins that the
// Permission component claims nothing under ~/.codex. brunnr-code does not write
// Codex's permissions config — not a profile, and not the legacy cleanup that
// used to strip one — so there is no injection target to verify and nothing to
// snapshot for rollback. A path reappearing here would mean something started
// writing that file again (#1794).
func TestComponentPathsPermissionsCodexContributesNoPaths(t *testing.T) {
	home := t.TempDir()
	configPath := filepath.Join(home, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(configPath, []byte("model = \"gpt-5.5\"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	adapters := resolveAdapters([]model.AgentID{model.AgentCodex})

	paths := componentPathsWithWorkspaceScoped(home, "", ScopeGlobal, model.Selection{}, adapters, model.ComponentPermission)

	if len(paths) != 0 {
		t.Fatalf("componentPaths(permissions,codex) = %v, want none", paths)
	}
}

func TestComponentPathsPermissionsSkipsAgentsWithoutInjectionTarget(t *testing.T) {
	home := t.TempDir()
	adapters := resolveAdapters([]model.AgentID{})

	paths := componentPathsWithWorkspaceScoped(home, "", ScopeGlobal, model.Selection{}, adapters, model.ComponentPermission)

	for _, adapter := range adapters {
		unwanted := adapter.SettingsPath(home)
		if unwanted == "" {
			continue
		}
		if containsPath(paths, unwanted) {
			t.Fatalf("componentPaths(permissions) must not include unsupported injection path %q\npaths=%v", unwanted, paths)
		}
	}
}

func TestComponentPathsPermissionsIncludesAgentsWithInjectionTarget(t *testing.T) {
	home := t.TempDir()
	adapters := resolveAdapters([]model.AgentID{
		model.AgentClaudeCode,
	})

	paths := componentPathsWithWorkspaceScoped(home, "", ScopeGlobal, model.Selection{}, adapters, model.ComponentPermission)

	for _, adapter := range adapters {
		want := adapter.SettingsPath(home)
		if !containsPath(paths, want) {
			t.Fatalf("componentPaths(permissions) missing supported injection path %q\npaths=%v", want, paths)
		}
	}
}

func containsPath(paths []string, want string) bool {
	for _, p := range paths {
		if p == want {
			return true
		}
	}
	return false
}

// ─── Organic routing guidance is installed for every configured agent ──────
//
// Routing guidance decides how an agent picks between direct, delegated, and
// proposed work. It is therefore unconditional: an install must receive it
// whatever components it selected (issue #1794).

const (
	routingOpenMarker  = "<!-- brunnr-code:" + agentguidance.RoutingSectionID + " -->"
	routingCloseMarker = "<!-- /brunnr-code:" + agentguidance.RoutingSectionID + " -->"

	legacyTriggerRulesOpenMarker = "<!-- brunnr-code:trigger-rules -->"
)

// newTestInstallRuntime builds an install runtime whose resolved plan mirrors
// the selection, which is what the real planner produces for these inputs.
func newTestInstallRuntime(t *testing.T, home string, selection model.Selection) *installRuntime {
	t.Helper()

	resolved := planner.ResolvedPlan{Agents: selection.Agents, OrderedComponents: selection.Components}
	rt, err := newInstallRuntime(home, ScopeGlobal, selection, resolved, system.PlatformProfile{OS: "darwin"})
	if err != nil {
		t.Fatalf("newInstallRuntime() error = %v", err)
	}
	return rt
}

// runInstallInjectionSteps executes the staged apply steps that write managed
// assets. Agent installation steps are skipped because they shell out to real
// package managers, which is unrelated to what these tests assert.
func runInstallInjectionSteps(t *testing.T, rt *installRuntime) {
	t.Helper()

	for _, step := range rt.stagePlan().Apply {
		if _, isAgentInstall := step.(agentInstallStep); isAgentInstall {
			continue
		}
		if err := step.Run(); err != nil {
			t.Fatalf("Run(%s) error = %v", step.ID(), err)
		}
	}
}

// runInstallComponentSteps executes only the component steps, which is how a
// later run reaches an already-guided installation.
func runInstallComponentSteps(t *testing.T, rt *installRuntime) {
	t.Helper()

	for _, step := range rt.stagePlan().Apply {
		if _, isComponent := step.(componentApplyStep); !isComponent {
			continue
		}
		if err := step.Run(); err != nil {
			t.Fatalf("Run(%s) error = %v", step.ID(), err)
		}
	}
}

func systemPromptFileFor(t *testing.T, home string, agent model.AgentID) string {
	t.Helper()

	adapter, err := agents.NewAdapter(agent)
	if err != nil {
		t.Fatalf("NewAdapter(%q) error = %v", agent, err)
	}
	return adapter.SystemPromptFile(home)
}

func readTextFile(t *testing.T, path string) string {
	t.Helper()

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", path, err)
	}
	return string(content)
}

func TestInstallRemoteAuthorizationIndependentOfComponents(t *testing.T) {
	for _, withPersona := range []bool{false, true} {
		t.Run(map[bool]string{false: "without-persona", true: "with-persona"}[withPersona], func(t *testing.T) {
			home := t.TempDir()
			selection := model.Selection{Agents: []model.AgentID{model.AgentClaudeCode}}
			if withPersona {
				selection.Components = []model.ComponentID{model.ComponentPersona}
			}
			runInstallInjectionSteps(t, newTestInstallRuntime(t, home, selection))
			prompt := readTextFile(t, systemPromptFileFor(t, home, model.AgentClaudeCode))
			if !strings.Contains(prompt, "<!-- brunnr-code:remote-authorization -->") {
				t.Fatal("install omitted remote authorization without the default persona")
			}
		})
	}
}

func TestInstallDeliversRoutingGuidanceWithPersonaOnly(t *testing.T) {
	home := t.TempDir()

	rt := newTestInstallRuntime(t, home, model.Selection{
		Agents:     []model.AgentID{model.AgentClaudeCode},
		Components: []model.ComponentID{model.ComponentPersona},
	})
	runInstallInjectionSteps(t, rt)

	prompt := readTextFile(t, systemPromptFileFor(t, home, model.AgentClaudeCode))
	if !strings.Contains(prompt, routingOpenMarker) || !strings.Contains(prompt, routingCloseMarker) {
		t.Fatalf("persona-only install left the agent unrouted:\n%s", prompt)
	}
	if !strings.Contains(prompt, "Implementation Routing") {
		t.Fatalf("routing section is present but carries no routing guidance:\n%s", prompt)
	}
}

func TestInstallRetainedAgentsIdempotently(t *testing.T) {
	for _, agent := range []model.AgentID{model.AgentClaudeCode} {
		t.Run(string(agent), func(t *testing.T) {
			home := t.TempDir()
			selection := model.Selection{Agents: []model.AgentID{agent}}
			run := func() { runInstallInjectionSteps(t, newTestInstallRuntime(t, home, selection)) }
			run()
			adapter := resolveAdapters([]model.AgentID{agent})[0]
			first := map[string]string{}
			for _, name := range reviewassets.NativeAgentManifest[agent] {
				path := filepath.Join(adapter.SubAgentsDir(home), name)
				body := readTextFile(t, path)
				if strings.HasPrefix(name, "review-") && name != "review-refuter.md" && strings.HasSuffix(name, ".md") && !strings.Contains(body, "BRUNNR_CODE_") {
					t.Fatalf("%s missing lens envelope markers", path)
				}
				first[path] = body
			}
			run()
			for path, body := range first {
				if got := readTextFile(t, path); got != body {
					t.Fatalf("second install changed %s", path)
				}
			}
		})
	}
}

func TestInstallCodexSkillRegistryHookIdempotently(t *testing.T) {
	home := t.TempDir()
	selection := model.Selection{Agents: []model.AgentID{model.AgentCodex}}
	runInstallInjectionSteps(t, newTestInstallRuntime(t, home, selection))
	path := filepath.Join(home, ".codex", "hooks.json")
	first := readTextFile(t, path)
	for _, want := range []string{`brunnr-code skill-registry refresh`} {
		if !strings.Contains(first, want) {
			t.Fatalf("missing %q in %s", want, path)
		}
	}
	runInstallInjectionSteps(t, newTestInstallRuntime(t, home, selection))
	if second := readTextFile(t, path); second != first {
		t.Fatal("second install changed hooks bytes")
	}
}

func TestInstallRoutingGuidanceIsIndependentOfComponentSelection(t *testing.T) {
	for _, tc := range []struct {
		name      string
		selection model.Selection
	}{
		{"no components", model.Selection{Agents: []model.AgentID{model.AgentClaudeCode}}},
		{"persona selected", model.Selection{Agents: []model.AgentID{model.AgentClaudeCode}, Components: []model.ComponentID{model.ComponentPersona}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			runInstallInjectionSteps(t, newTestInstallRuntime(t, home, tc.selection))
			prompt := readTextFile(t, systemPromptFileFor(t, home, model.AgentClaudeCode))
			if !strings.Contains(prompt, routingOpenMarker) || !strings.Contains(prompt, routingCloseMarker) || !strings.Contains(prompt, "Implementation Routing") {
				t.Fatalf("routing guidance missing from %s:\n%s", tc.name, prompt)
			}
		})
	}
}

func TestInstallStripsLegacyTriggerRulesSection(t *testing.T) {
	home := t.TempDir()

	promptPath := systemPromptFileFor(t, home, model.AgentClaudeCode)
	if err := os.MkdirAll(filepath.Dir(promptPath), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", filepath.Dir(promptPath), err)
	}
	seeded := filemerge.InjectMarkdownSection("# My own notes\n", "trigger-rules", "Retired WorkRun ceremony\n")
	if err := os.WriteFile(promptPath, []byte(seeded), 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", promptPath, err)
	}

	runInstallInjectionSteps(t, newTestInstallRuntime(t, home, model.Selection{
		Agents: []model.AgentID{model.AgentClaudeCode},
	}))

	prompt := readTextFile(t, promptPath)
	if strings.Contains(prompt, legacyTriggerRulesOpenMarker) {
		t.Fatalf("legacy trigger-rules section survived the install:\n%s", prompt)
	}
	if strings.Contains(prompt, "Retired WorkRun ceremony") {
		t.Fatalf("legacy trigger-rules content survived the install:\n%s", prompt)
	}
	if !strings.Contains(prompt, "# My own notes") {
		t.Fatalf("stripping the legacy section destroyed unmanaged user content:\n%s", prompt)
	}
	if !strings.Contains(prompt, routingOpenMarker) {
		t.Fatalf("routing guidance missing after the legacy strip:\n%s", prompt)
	}
}

func TestInstallRoutingGuidanceSecondRunIsByteIdentical(t *testing.T) {
	home := t.TempDir()
	selection := model.Selection{Agents: []model.AgentID{model.AgentClaudeCode, model.AgentCodex}}

	runInstallInjectionSteps(t, newTestInstallRuntime(t, home, selection))
	first := map[string]string{
		"claude prompt": readTextFile(t, systemPromptFileFor(t, home, model.AgentClaudeCode)),
		"codex prompt":  readTextFile(t, systemPromptFileFor(t, home, model.AgentCodex)),
	}

	runInstallInjectionSteps(t, newTestInstallRuntime(t, home, selection))
	second := map[string]string{
		"claude prompt": readTextFile(t, systemPromptFileFor(t, home, model.AgentClaudeCode)),
		"codex prompt":  readTextFile(t, systemPromptFileFor(t, home, model.AgentCodex)),
	}

	for label, before := range first {
		if second[label] != before {
			t.Fatalf("second install rewrote %s; routing delivery is not idempotent", label)
		}
	}
}

// ─── Routing guidance is part of the rollback contract ─────────────────────
//
// Routing guidance is installed for every configured agent, independently of
// the selected components. A selection whose components happen to cover the
// same file hid the gap; a selection with no components at all exposes it:
// without the routing path in the snapshot, install rewrites a file that was
// never backed up and a rollback cannot restore it.

func TestBackupTargetsIncludeRoutingGuidancePathsWithoutAnyComponent(t *testing.T) {
	home := t.TempDir()
	agent := model.AgentClaudeCode
	selection := model.Selection{Agents: []model.AgentID{agent}}
	resolved := planner.ResolvedPlan{Agents: selection.Agents}

	targets, err := backupTargets(home, "", ScopeGlobal, selection, resolved)
	if err != nil {
		t.Fatalf("backupTargets() error = %v", err)
	}

	routing, err := agentguidance.RoutingPathsWithOptions(home, agent, agentguidance.RoutingOptions{})
	if err != nil {
		t.Fatalf("RoutingPaths(%q) error = %v", agent, err)
	}
	if len(routing) == 0 {
		t.Fatalf("RoutingPaths(%q) returned no path; the test proves nothing", agent)
	}
	for _, path := range routing {
		if !containsPath(targets, path) {
			t.Fatalf("backupTargets missing routing guidance path %q\ntargets = %v", path, targets)
		}
	}
}

func TestBackupTargetsEngramClaudeIncludeRegistryAndLegacyMigrationSource(t *testing.T) {
	home := t.TempDir()
	selection := model.Selection{Agents: []model.AgentID{model.AgentClaudeCode}, Components: []model.ComponentID{model.ComponentEngram}}
	resolved := planner.ResolvedPlan{Agents: selection.Agents, OrderedComponents: selection.Components}

	targets, err := backupTargets(home, "", ScopeGlobal, selection, resolved)
	if err != nil {
		t.Fatalf("backupTargets() error = %v", err)
	}
	for _, want := range []string{
		filepath.Join(home, ".claude.json"),
		filepath.Join(home, ".claude", "mcp", "engram.json"),
	} {
		if !containsPath(targets, want) {
			t.Fatalf("backupTargets missing Claude Engram path %q; targets=%v", want, targets)
		}
	}
}

func TestBackupTargetsContainNoDuplicatePaths(t *testing.T) {
	home := t.TempDir()
	agentIDs := []model.AgentID{model.AgentClaudeCode}
	selection := model.Selection{
		Agents:     agentIDs,
		Components: []model.ComponentID{model.ComponentEngram, model.ComponentPersona},
	}
	resolved := planner.ResolvedPlan{Agents: agentIDs, OrderedComponents: selection.Components}

	targets, err := backupTargets(home, "", ScopeGlobal, selection, resolved)
	if err != nil {
		t.Fatalf("backupTargets() error = %v", err)
	}

	assertNoDuplicatePaths(t, "backupTargets", targets)
}

func assertNoDuplicatePaths(t *testing.T, label string, paths []string) {
	t.Helper()

	seen := map[string]struct{}{}
	for _, path := range paths {
		if _, duplicate := seen[path]; duplicate {
			t.Fatalf("%s returned duplicate path %q\npaths = %v", label, path, paths)
		}
		seen[path] = struct{}{}
	}
}

// backupTargets is the monolithic install snapshot plan these tests assert:
// installBackupTargets without the Claude module opt-in.
func backupTargets(homeDir, workspaceDir string, scope InstallScope, selection model.Selection, resolved planner.ResolvedPlan) ([]string, error) {
	return installBackupTargets(homeDir, workspaceDir, scope, selection, resolved, false)
}

// Exercise the public post-apply boundaries with a real owned agent and ledger.
// The backup is deliberately deduplicated, so rollback depends on the temporary snapshot.
func TestNativeReviewPostApplyErrorsRestoreDeduplicatedSnapshot(t *testing.T) {
	for _, branch := range []string{"install digest", "sync comparison", "sync digest"} {
		t.Run(branch, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			originalHome, originalCommand, originalLookPath := osUserHomeDir, runCommand, cmdLookPath
			osUserHomeDir = func() (string, error) { return home, nil }
			runCommand = func(string, ...string) error { return nil }
			cmdLookPath = func(string) (string, error) { return "", exec.ErrNotFound }
			t.Cleanup(func() { osUserHomeDir, runCommand, cmdLookPath = originalHome, originalCommand, originalLookPath })
			if _, err := RunInstall([]string{"--agent", "claude-code"}, system.DetectionResult{}); err != nil {
				t.Fatalf("seed install: %v", err)
			}
			adapter, err := agents.NewAdapter(model.AgentClaudeCode)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := reviewassets.InstallNativeAgents(home, adapter, reviewassets.InstallOptions{CodeGraphGuidanceMarkdown: "previous guidance"}); err != nil {
				t.Fatal(err)
			}
			targets := []string{filepath.Join(adapter.SubAgentsDir(home), reviewassets.OwnershipLedgerFilename), filepath.Join(adapter.SubAgentsDir(home), "jd-judge-a.md")}
			before := make(map[string][]byte)
			for _, path := range targets {
				before[path], err = os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
			}
			failure := errors.New("injected post-apply failure")
			var transaction *runtimeState
			assertApplied := func() {
				for _, path := range targets {
					got, err := os.ReadFile(path)
					if err != nil || bytes.Equal(got, before[path]) {
						t.Fatalf("post-apply failure did not follow a change to %s: %v", path, err)
					}
				}
				if transaction == nil || transaction.rollbackSnapshotDir == "" {
					t.Fatal("deduplicated temporary snapshot missing before failure")
				}
				if _, err := os.Stat(transaction.rollbackSnapshotDir); err != nil {
					t.Fatalf("deduplicated temporary snapshot missing before failure: %v", err)
				}
			}
			originalDigest, originalCompare := deriveManagedAssetWriter, compareChangedSyncFiles
			originalInstall, originalSync := installStagePlan, syncStagePlan
			t.Cleanup(func() {
				deriveManagedAssetWriter, compareChangedSyncFiles = originalDigest, originalCompare
				installStagePlan, syncStagePlan = originalInstall, originalSync
			})
			if branch != "sync comparison" {
				deriveManagedAssetWriter = func() (string, error) { assertApplied(); return "", failure }
			} else {
				compareChangedSyncFiles = func([]string, map[string]syncFileSnapshot) ([]string, error) { assertApplied(); return nil, failure }
			}
			planFor := func(backupRoot, workspace string, state *runtimeState, selection model.Selection, changed *[]string) pipeline.StagePlan {
				transaction = state
				if _, err := backup.NewSnapshotter().Create(filepath.Join(backupRoot, "existing"), targets); err != nil {
					t.Fatal(err)
				}
				snapshotDir := filepath.Join(backupRoot, "next")
				return pipeline.StagePlan{
					Prepare: []pipeline.Step{prepareBackupStep{id: "backup", snapshotter: backup.NewSnapshotter(), snapshotDir: snapshotDir, targets: targets, state: state, backupRoot: backupRoot}},
					Apply:   []pipeline.Step{rollbackRestoreStep{id: "restore", state: state, homeDir: home, workspaceDir: workspace}, nativeReviewAgentStep{id: "native", agent: model.AgentClaudeCode, homeDir: home, workspaceDir: workspace, scope: ScopeGlobal, selection: selection, changedFiles: changed, state: state}},
				}
			}
			selection := model.Selection{Agents: []model.AgentID{model.AgentClaudeCode}}
			var runErr error
			if branch == "install digest" {
				installStagePlan = func(rt *installRuntime) pipeline.StagePlan {
					return planFor(rt.backupRoot, rt.workspaceDir, rt.state, selection, nil)
				}
				_, runErr = RunInstall([]string{"--agent", "claude-code"}, system.DetectionResult{})
			} else {
				syncStagePlan = func(rt *syncRuntime) pipeline.StagePlan {
					plan := planFor(rt.backupRoot, rt.workspaceDir, rt.state, selection, &rt.changedFiles)
					rt.managedPaths = targets
					return plan
				}
				_, runErr = RunSyncWithSelection(home, selection)
			}
			if !errors.Is(runErr, failure) {
				t.Fatalf("%s returned %v, want injected failure", branch, runErr)
			}
			for _, path := range targets {
				got, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(got, before[path]) {
					t.Fatalf("%s left changed %s: %v", branch, path, err)
				}
			}
			if transaction.rollbackSnapshotDir != "" {
				t.Fatalf("%s retained transaction snapshot: %s", branch, transaction.rollbackSnapshotDir)
			}
		})
	}
}

// A deduplicated backup still needs a live transaction snapshot until the TUI
// caller has persisted state (or rolled back a failed persistence).
func TestTUIDeduplicatedNativeReviewSnapshotSurvivesPostApplyRollback(t *testing.T) {
	for _, outcome := range []string{"post-apply rollback", "persisted success"} {
		t.Run(outcome, func(t *testing.T) {
			home := t.TempDir()
			adapter, err := agents.NewAdapter(model.AgentClaudeCode)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := reviewassets.InstallNativeAgents(home, adapter, reviewassets.InstallOptions{CodeGraphGuidanceMarkdown: "previous guidance"}); err != nil {
				t.Fatal(err)
			}
			ledger := filepath.Join(adapter.SubAgentsDir(home), reviewassets.OwnershipLedgerFilename)
			agent := filepath.Join(adapter.SubAgentsDir(home), "jd-judge-a.md")
			targets := []string{ledger, agent}
			before := make(map[string][]byte)
			for _, path := range targets {
				before[path], err = os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
			}
			selection := model.Selection{Agents: []model.AgentID{model.AgentClaudeCode}}
			resolved := planner.ResolvedPlan{Agents: selection.Agents}
			var transaction *runtimeState
			original := tuiInstallStagePlan
			tuiInstallStagePlan = func(rt *installRuntime) pipeline.StagePlan {
				transaction = rt.state
				if _, err := backup.NewSnapshotter().Create(filepath.Join(rt.backupRoot, "existing"), targets); err != nil {
					t.Fatal(err)
				}
				return pipeline.StagePlan{
					Prepare: []pipeline.Step{prepareBackupStep{id: "backup", snapshotter: backup.NewSnapshotter(), snapshotDir: filepath.Join(rt.backupRoot, "next"), targets: targets, state: rt.state, backupRoot: rt.backupRoot}},
					Apply: []pipeline.Step{
						rollbackRestoreStep{id: "restore", state: rt.state, homeDir: home, workspaceDir: rt.workspaceDir},
						nativeReviewAgentStep{id: "native", agent: model.AgentClaudeCode, homeDir: home, workspaceDir: rt.workspaceDir, scope: ScopeGlobal, selection: selection, state: rt.state},
					},
				}
			}
			t.Cleanup(func() { tuiInstallStagePlan = original })
			result, orchestrator, _ := ExecuteTUIInstallRecordingCodexServiceTier(home, selection, resolved, system.PlatformProfile{OS: "darwin", Supported: true}, nil)
			if result.Err != nil {
				t.Fatalf("apply: %v", result.Err)
			}
			if transaction.rollbackSnapshotDir == "" {
				t.Fatal("expected deduplicated transaction snapshot")
			}
			for _, path := range targets {
				content, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if bytes.Equal(content, before[path]) {
					t.Fatalf("%s was not updated before simulated persistence failure", path)
				}
			}
			dir := transaction.rollbackSnapshotDir
			if _, err := os.Stat(dir); err != nil {
				t.Fatalf("snapshot must survive apply until persistence settles: %v", err)
			}
			if outcome == "post-apply rollback" {
				rollback := orchestrator.Rollback(result)
				if rollback.Err != nil {
					t.Fatalf("post-apply rollback: %v", rollback.Err)
				}
				for _, path := range targets {
					content, err := os.ReadFile(path)
					if err != nil || !bytes.Equal(content, before[path]) {
						t.Fatalf("rollback did not restore %s: %v", path, err)
					}
				}
			} else {
				orchestrator.Finish()
				orchestrator.Finish() // settling twice must not re-run cleanup
			}
			if _, err := os.Stat(dir); !os.IsNotExist(err) {
				t.Fatalf("transaction snapshot not cleaned after %s: %v", outcome, err)
			}
		})
	}
}

func TestNativeReviewLedgerInstallBackupAndManualActions(t *testing.T) {
	home, workspace := t.TempDir(), t.TempDir()
	adapter, err := agents.NewAdapter(model.AgentClaudeCode)
	if err != nil {
		t.Fatal(err)
	}
	ledger := filepath.Join(adapter.SubAgentsDir(home), reviewassets.OwnershipLedgerFilename)
	selection := model.Selection{Agents: []model.AgentID{model.AgentClaudeCode}}
	targets, err := backupTargets(home, workspace, ScopeGlobal, selection, planner.ResolvedPlan{Agents: selection.Agents})
	if err != nil {
		t.Fatal(err)
	}
	if !containsPath(targets, ledger) {
		t.Fatalf("install backup missing ledger %s", ledger)
	}
	path := filepath.Join(adapter.SubAgentsDir(home), "jd-judge-a.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("my review agent"), 0o644); err != nil {
		t.Fatal(err)
	}
	state := &runtimeState{}
	step := nativeReviewAgentStep{id: "test", agent: model.AgentClaudeCode, homeDir: home, workspaceDir: workspace, scope: ScopeGlobal, selection: selection, state: state}
	if err := step.Run(); err != nil {
		t.Fatal(err)
	}
	if len(state.nativeReviewActions) != 1 || state.nativeReviewActions[0] != nativeReviewPreservedAction(path) {
		t.Fatalf("preserved-file actions = %v", state.nativeReviewActions)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "my review agent" {
		t.Fatalf("user bytes = %q, error = %v", data, err)
	}
}
