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
- [ ] T2: CLI, model and state. Remove preset/component/skill flags from install and sync; force full in install, sync and restore; reduce `PresetID` values and `ComponentsForPreset`/`SkillsForPreset`; golden file. Tests. Route: delegated (writer trigger: 2+ non-trivial files across `cli`, `model`, `state`, `components/skills`).
- [ ] T3: e2e script and README. Route: delegated (writer; large shell script).

## Acceptance criteria

- `go run ./internal/gofmtcheck` passes.
- `go test ./...` passes.
- `git grep -n 'PresetCustom\|PresetMinimal\|PresetEcosystemOnly\|ScreenPreset\|ScreenSkillPicker\|include-permissions'` outside `odd/` returns nothing.
- `brunnr-code install --preset full` and `--component engram` fail as unknown flags.

## Delivery

Strategy: `single-pr`. Forecast: ~1000–1400 authored lines, mostly deletions in tests and TUI. Exceeds the 400-line heuristic because it is a removal; single maintainer convention uses no chained PRs.

## Progress

- Exploration done (one read-only mapping agent).
- Branch `feat/single-setup-preset`.
- T1 done. Route: delegated (writer trigger: 2+ non-trivial files in `internal/tui`). Deleted `screens/preset.go`, `screens/skill_picker.go` and their tests, the custom component picker, `ScreenPreset`/`ScreenSkillPicker` and every `PresetCustom` branch. Agents Continue now enters the picker chain anchored at Agents and ends at the read-only Install Plan; Back/Esc from the plan returns to Agents. Review shows "Dev Stack + Permissions". RED observed first: `TestAgentsContinueOpensInstallPlan` and `TestInstallFlowHasNoSetupPickers` failed (Continue landed on the Preset screen), then passed. Verification: `go run ./internal/gofmtcheck` ok; `go build ./...` ok; `go test ./internal/tui/... -count=1` ok; `go test ./...` ok with no failures; `git grep -nw` for the removed preset/screen identifiers in `internal/tui` is empty (only the unrelated `ClaudePresetCustom` model-picker constant matches the substring). `catalog.SelectableComponents` is now unused and left for T2.

## Next step

T2.
