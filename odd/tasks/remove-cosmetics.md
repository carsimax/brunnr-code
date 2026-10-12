# Feature: remove-cosmetics

Locator: `odd/tasks/remove-cosmetics.md` · Engram mirror: `odd/remove-cosmetics/tasks` · Branch: `refactor/remove-cosmetics`

## Objective

Remove the purely cosmetic features: the ASCII wordmark and tagline on the TUI welcome screen, and the two theme components (`theme`, `claude-theme`). Brunnr-Code is a personal tool shared with colleagues; it only needs to work.

## Why

Branding and visual themes add code, tests and an install/uninstall surface that brings no functional value.

## Decisions

- **No legacy cleanup path (2026-10-12):** there are no production installs, so files a previous build wrote (`"theme"` key in settings, `~/.claude/themes/brunnr*.json`) are not handled. This matches the repo rule for removed agents. Old `state.json` entries are already dropped by `model.KnownComponentIDs`.
- **Kept on purpose:** the lipgloss palette in `internal/tui/styles/styles.go` (error/warning/success styles carry meaning), spinners, ✓/✗/⚠ status symbols, personas and output styles (they shape agent behavior).
- **Presets:** without `claude-theme`, the Full preset differs from Dev Stack only by Permissions; its label "Dev Stack + Polish" is renamed to reflect that.
- Delivery strategy: `single-pr` (single-maintainer repo). Forecast is about 700 authored changed lines, almost all deletions.

## Tasks

Each task runs `go run ./internal/gofmtcheck`, `go build ./... && go vet ./...`, and the tests of touched packages; T2 ends with a full `go test ./...`. These are removals, so the build and updated tests are the proof; no new behavior needs RED/GREEN. RDD is disabled in this clone.

- [x] **T1: Welcome screen logo.** Delete `internal/tui/styles/logo.go`, drop `styles.Tagline`, remove the logo branch in `internal/tui/screens/welcome.go` and simplify its fallbacks.
- [ ] **T2: Theme components.** Delete `internal/components/theme/`, remove the component IDs and presets wiring (`model`, `catalog`, `planner`), the install/sync paths and `--include-theme` flag (`cli/run.go`, `cli/sync.go`), uninstall handling, the upgrade backup paths, preset screen text; update tests and `CLAUDE.md`. Full `go test ./...` and a `git grep -i theme` sweep.

## Progress

Branch created from `60bb5069`.
- **T1 done** (route: delegated writer; commit `4ed08511`). Deleted `internal/tui/styles/logo.go` and `styles.Tagline`; the welcome screen shows a plain `Brunnr-Code <version>` title (TitleStyle) instead of the wordmark and tagline. `RenderWelcomeWithSize` lost the `includeLogo` branch: it now tries the framed view with the update banner, then the compact frameless view, then the minimum state. Checks: gofmtcheck ok; `go build ./... && go vet ./...` ok; `go test ./internal/tui/... -count=1` ok (welcome viewport tests pass unchanged).
