# Feature: remove-cosmetics

Locator: `odd/tasks/remove-cosmetics.md` · Engram mirror: `odd/remove-cosmetics/tasks` · Branch: `refactor/remove-cosmetics`

## Objective

Remove the purely cosmetic features: the ASCII wordmark and tagline on the TUI welcome screen, and the two theme components (`theme`, `claude-theme`). Brunnr-Code is a personal tool shared with colleagues; it only needs to work.

## Why

Branding and visual themes add code, tests and an install/uninstall surface that brings no functional value.

## Decisions

- **No legacy cleanup path (2026-10-12):** there are no production installs, so files a previous build wrote (`"theme"` key in settings, `~/.claude/themes/brunnr*.json`) are not handled. This matches the repo rule for removed agents. Old `state.json` entries are already dropped by `model.KnownComponentIDs`.
- **Terminal-native colors (user, 2026-10-12):** the TUI uses only the terminal's own ANSI colors, so each colleague's terminal theme is inherited. No hex/256 colors and no backgrounds; plain text uses the terminal default, titles and headings are Bold only, help/subtext is Faint, the cursor/selection is Bold + ANSI 4, and success/warning/error are ANSI 2/3/1. This replaces the earlier note keeping the Rose Pine lipgloss palette.
- **Kept on purpose:** spinners, ✓/✗/⚠ status symbols, personas and output styles (they shape agent behavior).
- **Presets:** without `claude-theme`, the Full preset differs from Dev Stack only by Permissions; its label "Dev Stack + Polish" is renamed to reflect that.
- Delivery strategy: `single-pr` (single-maintainer repo). Forecast is about 700 authored changed lines, almost all deletions.

## Tasks

Each task runs `go run ./internal/gofmtcheck`, `go build ./... && go vet ./...`, and the tests of touched packages; T2 ends with a full `go test ./...`. These are removals, so the build and updated tests are the proof; no new behavior needs RED/GREEN. RDD is disabled in this clone.

- [x] **T1: Welcome screen logo.** Delete `internal/tui/styles/logo.go`, drop `styles.Tagline`, remove the logo branch in `internal/tui/screens/welcome.go` and simplify its fallbacks.
- [x] **T2: Theme components.** Delete `internal/components/theme/`, remove the component IDs and presets wiring (`model`, `catalog`, `planner`), the install/sync paths and `--include-theme` flag (`cli/run.go`, `cli/sync.go`), uninstall handling, the upgrade backup paths, preset screen text; update tests and `CLAUDE.md`. Full `go test ./...` and a `git grep -i theme` sweep.
- [x] **T3: Terminal-native TUI colors.** Rewrite `internal/tui/styles/styles.go`: drop the Rose Pine hex palette, keep a minimal set of ANSI constants (accent 4, success 2, warning 3, error 1), Bold titles/headings, Faint help/subtext, uncolored borders; update every screen that used the removed constants. Sweep `internal/tui` for hex colors and `Background(`.

## Progress

Branch created from `60bb5069`.
- **T1 done** (route: delegated writer; commit `4ed08511`). Deleted `internal/tui/styles/logo.go` and `styles.Tagline`; the welcome screen shows a plain `Brunnr-Code <version>` title (TitleStyle) instead of the wordmark and tagline. `RenderWelcomeWithSize` lost the `includeLogo` branch: it now tries the framed view with the update banner, then the compact frameless view, then the minimum state. Checks: gofmtcheck ok; `go build ./... && go vet ./...` ok; `go test ./internal/tui/... -count=1` ok (welcome viewport tests pass unchanged).
- **T2 done** (route: delegated writer; commit `fb3d0206`). Deleted `internal/components/theme/`; removed `ComponentTheme`/`ComponentClaudeTheme` from `model` (IDs, known list), `catalog`, `planner`, and `VisualPolishComponents`/the preset visual helper, so Full installs engram, skills, permissions and persona. Removed the theme install dispatch and component paths (`cli/run.go`), the `--include-theme` flag and theme sync cases (`cli/sync.go`; `workspaceGlobalOnlyComponent` only covered claude-theme, so it and its 3 call sites went too), the uninstall theme handling and visual-polish expansion, and the upgrade backup of theme files. Preset label is now "Dev Stack + Permissions". Tests: theme-only cases deleted; `--component theme` fixtures in `internal/cli` and `internal/app` now use `permissions`; `e2e/e2e_test.sh` lost its theme tests, the theme edge case became a permissions-only check, and multi-component installs no longer pass `--component theme`. `CLAUDE.md` no longer lists theme. Checks: gofmtcheck ok; `go build ./... && go vet ./...` ok; `bash -n e2e/e2e_test.sh` ok; full `go test ./... -count=1 -timeout 30m` ok (49 packages, no failures). Sweep: the targeted patterns return nothing; `git grep -i theme` keeps only generic `"theme"` JSON keys used as sample data in filemerge, communitytool and uninstall cleaner tests.
- **T3 done** (route: delegated writer; commit `33ccd259`). `internal/tui/styles/styles.go` dropped the Rose Pine hex palette for four ANSI constants (`ColorAccent` 4, `ColorSuccess` 2, `ColorWarning` 3, `ColorError` 1). Title/heading are Bold only; help/subtext and the empty progress bar are Faint; selected items are Bold + accent; unselected text uses the terminal default; the filled progress bar uses success; the percent is Bold. `FrameStyle` keeps its uncolored double border (it groups content and the welcome layout measures it); the unused `PanelStyle` was removed. No screen referenced the removed `Color*` constants directly, and no package outside `internal/tui` imports `styles`. Checks: gofmtcheck ok; `go build ./... && go vet ./...` ok; `go test ./internal/tui/... -count=1` ok. Sweep `git grep -e '#[0-9a-fA-F]\{6\}' -e 'Background(' -- internal/tui`: only `context.Background()` calls in `internal/tui/model.go`, which are not styling.

All tasks (T1-T3) are done. Next step: the user decides on push and the PR.
