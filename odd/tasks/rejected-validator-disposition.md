# Feature: rejected-validator-disposition

Locator: `odd/tasks/rejected-validator-disposition.md` · Engram mirror: `odd/rejected-validator-disposition/tasks` · Branch: `fix/rejected-validator-disposition`

## Objective

Port upstream gentle-ai v4.1.1's rejected-validator terminal disposition (upstream commits `37b3145b` and `0bc4fdbc`) to Brunnr-Code.

## Why

When the targeted validator rejects the single bounded correction, the authority ends `escalated` with its correction consumed and keeps its active claim. `review repair` cannot release it, so the user is stuck. Upstream adds an explicit-selector disposition that releases only the active claim. It keeps the verdict and the consumed correction, never approves, and never restarts review.

## Scope

- `internal/reviewtransaction/authority_disposition_plan.go`: add `RejectedValidatorDispositionClass`, add `rejectedValidatorDispositionPlanForSelector`, route a successor-only selector for a loaded record to it, and list eligible selectors.
- `internal/cli/review_repair.go`: publish a lone selector when it is a rejected-validator terminal, and update the help and flag text.
- New tests: `internal/reviewtransaction/rejected_validator_disposition_test.go` and `internal/cli/review_rejected_disposition_test.go`.
- Out of scope: upstream `docs/review-authority-threat-model.md` (the fork has no `docs/`), and the Windows test commit `cbdbe97d`, whose packages were removed from the fork.

## Constraints

- Brunnr naming: `brunnr-code.*` schema IDs, the `brunnr-code` CLI, and the module `github.com/carsimax/brunnr-code`.
- Delivery: `single-pr`. RDD is disabled in this clone.

## Tasks

- [x] **T1: Port the disposition and its tests.** Route: delegated (writer trigger: 2 non-trivial production files plus 2 test files). RED first: add the upstream tests and watch them fail, then port the production code to GREEN. Checks: `go run ./internal/gofmtcheck`, `go build ./... && go vet ./...`, `go test ./internal/reviewtransaction ./internal/cli -count=1`, `go test ./... -count=1`.

## Progress

Branch created from `d27af613`.

- **T1 (delegated, writer):** RED: `TestRejectedValidatorDispositionRequiresExactAuthorizedRelease` failed with "this plan is scoped to outdated historical entries", and `TestRejectedValidatorDispositionCompiledCLI` got `DispositionSelectors: nil` from a selectorless preflight. GREEN: `authority_disposition_plan.go` took the upstream patch unchanged (`git apply` was clean). `review_repair.go` was ported by hand and keeps the `brunnr-code.review-repair-authorization/v1` schema string. CLI test adaptations: the brunnr-code module and binary, no telemetry or self-update env vars (the fork has neither), and the four existing provider-role calls in place of upstream's missing `reviewProviderCloseTargetedValidatorRaw`. Checks: gofmtcheck ok, build+vet ok, reviewtransaction ok, `internal/cli` ok alone with `-timeout=40m` (570s), `go test ./... -count=1 -timeout=40m` ok. Parent spot check: the new CLI test passes in about 3s.
- Known risk, not introduced here: `internal/cli` takes about 570s, near Go's default 600s timeout used by CI `go test ./...`. Tracked separately on `perf/test-speed`.
- Next: PR to `main` (the user's decision).
