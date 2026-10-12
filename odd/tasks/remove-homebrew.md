# Feature: remove-homebrew

Locator: `odd/tasks/remove-homebrew.md` · Engram mirror: `odd/remove-homebrew/tasks` · Branch: `refactor/remove-homebrew`

## Objective

Remove every Homebrew code path. Brunnr-Code is an internal company tool, not a commercial product: everything installs through curl (GitHub release downloads, `scripts/install.sh`) or `go install`.

## Why

Homebrew adds a second install channel with its own detection, trust/tap handling, upgrade strategy, and legacy path repair. The fork has no tap and no production installs, so this code only adds maintenance cost.

## Decisions

- **Full removal (user, 2026-10-12):** this includes the detection of an existing Engram under `/opt/homebrew/bin` or linuxbrew, and the repair of stale `/Cellar/engram/<ver>` MCP commands. Engram itself is planned for removal in a later change.
- **Engram on macOS** uses the existing `engram.DownloadLatestBinary` (release tarball + checksum). Verified 2026-10-12: Engram v3.4.0 publishes `darwin_amd64` and `darwin_arm64` tarballs.
- **macOS system deps (git, curl, node, go)** get no auto-install command. Hints become manual: `xcode-select --install` for git, curl ships with macOS, and the official download URLs for node and go.
- **Darwin package manager:** no longer hard-coded to `brew`. Linux keeps its native managers (apt, dnf, pacman, apk, zypper, nix, emerge), without brew/linuxbrew.

## Footprint (explorer, 2026-10-12)

Production (about 22 files): `.goreleaser.yaml`, `CLAUDE.md`, `e2e/lib.sh` (comments only), `internal/system/{detect,deps,install_deps}.go`, `internal/cli/{run,install}.go`, `internal/installcmd/resolver.go`, `internal/components/engram/{download,inject,caller_binary}.go`, `internal/update/{check,homebrew,instructions,registry,types}.go`, `internal/update/upgrade/{executor,homebrew,strategy}.go`, `internal/tui/model.go`, `internal/testenv/brew.go`, `testdata/golden/engram-claude-mcp.golden`. Tests: 42 files reference brew; 5 exist mainly for brew. The `single-pr` delivery strategy is used (single-maintainer repo, no chained PRs).

## Tasks

Each task runs `go run ./internal/gofmtcheck`, `go build ./... && go vet ./...`, and the tests of touched packages. A full `go test ./...` runs at the end. Each task ends with a Conventional Commit. These are removals, so the build and updated tests are the proof; RED/GREEN applies only where new behavior appears (darwin profile routing to the download path). RDD is disabled in this clone.

- [x] **T1: Platform detection and system deps.** Darwin stops defaulting to `brew`; brew leaves the Linux manager list and `ResolveInstallProfile`; darwin dep hints/commands become manual; brew is no longer an optional dep; fix `install.go` text.
- [x] **T2: Engram install.** Remove the brew install branch, the brew resolver/trust/tap commands, `HOMEBREW_*` env injection, `resolveBrewCommand`, the Homebrew path lookup, and the Cellar/stable-path repair in `inject.go`. macOS uses `DownloadLatestBinary`; fix the stale `withGoInstallPathNote` text; update the golden.
- [x] **T3: Update/upgrade.** Delete `update/homebrew.go`, `upgrade/homebrew.go`, the brew strategy, `InstallBrew`, and brew hints in `instructions.go`/`check.go`.
- [ ] **T4: TUI, test helpers, comments, sweep.** Remove brew uninstall detection in `tui/model.go`, `internal/testenv/brew.go`, brew-only tests, and comment mentions. Run a full `go test ./...` and a final `git grep -i brew` sweep.

## Progress

Branch created from `d47bde70`.
- **T1 done** (route: delegated writer; commit `ac44f99d`). Darwin profile now has an empty `PackageManager` (same "no manager" value Linux uses on refusal), so Engram on macOS routes to the download branch; brew left `linuxPackageManagers` and the `ResolveInstallProfile` fallback; darwin git/curl/node/go have no install command and manual hints (`xcode-select --install`, curl.se, nodejs.org, go.dev/dl); the Homebrew bootstrap, `installHintBrew`, `installCommandsBrew` and the optional brew dep are gone; `install.go` help text no longer lists brew. RED observed first (`TestDetectFromInputsMarksSupportedMacOS`, `TestResolvePlatformProfileMatrix` failed with `"brew"`); added `TestDarwinDepsAreManual`; brew-only tests deleted. Checks: gofmtcheck ok; `go build ./... && go vet ./...` ok; `go test` of system, cli, installcmd, app, planner, components/engram, tui all ok (app parity fixtures updated to the empty darwin PM).
- **T2 done** (route: delegated writer; commit `41172f6f`). Engram installs from the release download on every platform: the brew branch, `resolveBrewCommand`/`withResolvedBrewCommand`, `commandEnv`/`HOMEBREW_*` env injection, the Homebrew/linuxbrew prefix lookup (`resolveEngramInstalledPath` became a plain `cmdLookPath("engram")`), `isExecutableFile`, the installcmd brew resolver/trust probe/tap commands, the dead `validateGoForModuleInstall` + `installcmd/testhelper.go`, `engram.InstallCommand`, and the Cellar rewrite in `inject.go` are gone. `isStableHomebrewEngramPath` became `isStableEngramPath` (only `/usr/local/bin/engram`, the download location). `withGoInstallPathNote` became `withEngramPathNote`: it names the download dir when it was not on PATH before install (Unix; recorded in `runtimeState.engramOffPathDir`, since `addUserPath` mutates the process PATH), and the verify hint no longer claims `go install`. CLI TestMain stubs `engramDownloadFn` so macOS tests stay off the network. Golden now expects `/usr/local/bin/engram`. RED observed first (new note/guidance tests failed to build), then GREEN. Checks: gofmtcheck ok; `go build ./... && go vet ./...` ok; `go test` of cli, installcmd, components, app, planner: 17 packages ok. Left for T3: `internal/app/update_test.go`, `upgrade_test.go` brew profiles. Left for T4: `internal/testenv/brew.go` (now unused), `internal/components/delivery_guarantee_installed_test.go` `/opt/homebrew/bin/engram` stub.
- **T3 done** (route: delegated writer; commit `6c07931f`). Deleted `update/homebrew.go`, `upgrade/homebrew.go` and both ownership tests; `runStrategy` routes on the tool's declared `InstallMethod` only (engram → `engramBinaryUpgrade`), with `brewUpgrade`, tap/trust/update, cask verification/migration and failure advice gone; `effectiveMethod` removed (callers use `r.Tool.InstallMethod`); `checkSingleTool` no longer probes Homebrew ownership; `updateHintForOwnership` removed and `engramHint` always returns `brunnr-code upgrade (downloads pre-built binary)` (unchanged non-brew text, so no RED needed); `InstallBrew` and brew registry comments removed. Tests: brew-only cases deleted, darwin profiles use the empty package manager, added `TestEngramUpgradeMacOSUsesDownload` and a macOS case in `TestEngramHintIsBinaryDownload`. Checks: gofmtcheck ok; `go build ./... && go vet ./...` ok; `go test ./internal/update/... ./internal/app/...` ok; `git grep -i brew|homebrew|cask` in `internal/update internal/app` has no hits. CLI suite not run (no cli tests or cli-used update APIs changed; vet compiles all tests).
