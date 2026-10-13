# Feature: single-setup-preset

## Objective

Make "Dev Stack + Permissions" (`PresetFull`) the only setup configuration. No other preset, custom selection, or per-component/per-skill configuration path remains.

## Problem / Why

- The TUI offers four presets (Memory Only, Dev Stack, Dev Stack + Permissions, Custom). Custom opens a component picker and a skill picker.
- `install` accepts `--preset`, `--component(s)` and `--skill(s)`, so `--component engram` reproduces "Memory Only".
- `sync` without state installs Dev Stack without permissions unless `--include-permissions` is passed. It accepts `--skill(s)`.
- `sync` restores stored components/skills from `state.json`, so an existing `minimal` or `custom` selection survives.
- The maintainer wants one setup and one only.

## Decision (user, 2026-10-12)

Full scope: remove the TUI Preset screen, Custom mode and Skill picker; remove `install --preset/--component(s)/--skill(s)` and `sync --skill(s)/--include-permissions`; install and sync always resolve to full (engram, skills, permission, persona; the 8 fixed skills). A `state.json` with another preset or a different component/skill list is tolerated on read and normalized to full.

## Scope

- TUI: Agents → Install Plan (read-only DependencyTree) → existing later steps. Delete `screens/preset.go`, `screens/skill_picker.go`, the custom DependencyTree picker and all `PresetCustom` branches.
- Model: only `PresetFull` remains as a value. Legacy string values in `state.json` decode without error and are ignored.
- CLI: flags removed; unknown-flag errors are the expected behavior for them.
- State: restore paths force full components and skills.
- Tests, golden file, e2e script and README updated with the behavior.

## Out of scope

- Claude/Codex model presets (`ClaudePreset*`, `CodexPreset*`). They are model assignments, not setup presets.
- `--agents` selection stays.

## Constraints

- No legacy code paths beyond tolerating old `state.json` values.
- Artifacts in English. Guards are ratchets.

## Tasks

- [x] T1: TUI. Remove Preset screen, Custom mode and Skill picker; reroute Agents → DependencyTree and back. Tests. Route: delegated (writer trigger: 2+ non-trivial files in `internal/tui`).
- [x] T2: CLI, model and state. Remove preset/component/skill flags from install and sync; force full in install, sync and restore; reduce `PresetID` values and `ComponentsForPreset`/`SkillsForPreset`; golden file. Tests. Route: delegated (writer trigger: 2+ non-trivial files across `cli`, `model`, `state`, `components/skills`).
- [x] T3: e2e script and README. Route: delegated (writer; large shell script).

## Acceptance criteria

- `go run ./internal/gofmtcheck` passes.
- `go test ./...` passes.
- `git grep -nw 'PresetCustom\|PresetMinimal\|PresetEcosystemOnly\|ScreenPreset\|ScreenSkillPicker\|include-permissions'` outside `odd/` returns nothing.
- `brunnr-code install --preset full` and `--component engram` fail as unknown flags.

## Delivery

Strategy: `single-pr`. Forecast: ~1000–1400 authored lines, mostly deletions in tests and TUI. Exceeds the 400-line heuristic because it is a removal; single maintainer convention uses no chained PRs.

## Progress

- Exploration done (one read-only mapping agent).
- Branch `feat/single-setup-preset`.
- T1 done (commit `cf6d8242`). Route: delegated (writer trigger: 2+ non-trivial files in `internal/tui`). Deleted `screens/preset.go`, `screens/skill_picker.go` and their tests, the custom component picker, `ScreenPreset`/`ScreenSkillPicker` and every `PresetCustom` branch. Agents Continue now enters the picker chain anchored at Agents and ends at the read-only Install Plan; Back/Esc from the plan returns to Agents. Review shows "Dev Stack + Permissions". RED observed first: `TestAgentsContinueOpensInstallPlan` and `TestInstallFlowHasNoSetupPickers` failed (Continue landed on the Preset screen), then passed. Verification: `go run ./internal/gofmtcheck` ok; `go build ./...` ok; `go test ./internal/tui/... -count=1` ok; `go test ./...` ok with no failures; `git grep -nw` for the removed preset/screen identifiers in `internal/tui` is empty (only the unrelated `ClaudePresetCustom` model-picker constant matches the substring). `catalog.SelectableComponents` is now unused and left for T2.

- T2 done (commit `c362bc6d`). Route: delegated (writer trigger: 2+ non-trivial files across `cli`, `model`, `state`, `components/skills`). `PresetID` keeps only `PresetFull`; `model.SetupComponents()` and `skills.SetupSkills()` replace `ComponentsForPreset`/`SkillsForPreset`. `install --preset/--component(s)/--skill(s)` and `sync --skill(s)/--include-permissions` are removed and fail as unknown flags. Install, sync and TUI sync always build the full selection; `RestorePersistedSelection`, `state.RestoreSelection`, `KnownComponentIDs`, the orphan-modifier check, `syncShouldIncludePermissions` and `catalog.SelectableComponents` are gone. A `state.json` with a retired preset or a narrower list still decodes (`PresetID` is a plain string), is ignored for selection, and is rewritten to the full setup by the next install (explicit-agent merge) or global sync. Golden `skills-presets.json` reduced to `full`. Behavior notes: sync now always applies the permissions overlay, so a JSONC `~/.claude/settings.json` loses its comments on sync (keys are kept); a `--scope workspace` install still adds the global-only permissions overlay to global settings (hooks stay scoped). RED observed first: `TestParseInstallFlagsRejectsRemovedSetupFlags`, `TestParseSyncFlagsRejectsRemovedSetupFlags`, `TestRunSyncWithoutStateUsesSetup` (components `[persona engram skills]`) and `TestRunSyncNormalizesLegacyStateToSetup` (components `[engram persona]`) failed, then passed. Verification: `go run ./internal/gofmtcheck` ok; `go build ./...` ok; `go test ./... -timeout 28m` ok (49 packages; `internal/cli` takes ~520-560s on this machine, also ~521s at the T1 base, so the default 10m per-package timeout can trip under full-suite load); removed-identifier `git grep` empty; `brunnr-code install --preset full --dry-run` fails with `flag provided but not defined: -preset`.

- T3 done (commit `8cee4534`). Route: delegated (writer; large shell script). `e2e/e2e_test.sh` drops every `--preset`/`--component(s)`/`--skill(s)` invocation: preset/custom/single-component/skill-selection cases (dry-run presets, minimal/ecosystem/custom skills, minimal preset, permissions-only edge) are deleted; per-component install tests now run the plain full install and keep their assertions; the skills test asserts exactly the 8 named setup skills; `test_removed_setup_flags_rejected` asserts that the eight removed install/sync flags fail with `flag provided but not defined`. The stale dry-run `Persona:` header assertion (header removed in `8c8850e6`) is dropped. The real-agent organic e2e installs without `--components permissions`. README describes the single setup and no longer lists `--components`. Verification: `bash -n e2e/e2e_test.sh` ok; shellcheck not installed; `go run ./internal/gofmtcheck` ok; `go test ./... -timeout 28m` ok (49 packages, `internal/cli` 622s); `go test -tags real_agent_e2e ./e2e/organicruntime -run TestOrganicConfiguredAgentReceivesRoutingGuidanceRealAgents` ok. Docker e2e (`RUN_FULL_E2E=1 RUN_BACKUP_TESTS=1 ./e2e/docker-test.sh`) ran on ubuntu/arch/fedora: every Tier 1 setup and removed-flag check passes; every Tier 2/3 real install fails with `install CodeGraph: run "npm install -g @colbymchenry/codegraph@latest": exit status 243` (EACCES on the global npm prefix as the non-root test user). That failure predates this feature: the mandatory `codegraph:install` step runs on every install since `3c354609`, and the e2e has no CodeGraph shim. Remaining match outside T3's edit surface: `internal/cli/run.go:1930` `engramInstallCommand` still suggests `brunnr-code install --agent <ids> --components engram`, a command that now fails.

- Follow-up fix: `engramInstallCommand` (`internal/cli/run.go`) suggested the removed `--components engram`; it now suggests `brunnr-code install --agent <ids>`. Route: inline (one mechanical file plus test). RED observed (`TestEngramInstallCommandUsesSingleSetup` got the `--components` suffix), then GREEN.
- Docker e2e (run during T3, before the last stale assertion was removed): all Tier 1 single-setup checks passed on ubuntu, arch and fedora. Tier 2/3 installs fail on the mandatory CodeGraph npm global install (EACCES as non-root testuser), which predates this feature (since `3c354609`). Not fixed here.

## Next step

Feature complete. Open item outside this feature: the Docker e2e needs a CodeGraph shim (like the Engram fake) or a user-writable npm prefix. Push and PR are the maintainer's decision.
