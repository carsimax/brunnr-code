# Bench (removed)

The `bench/` module measured the friction of driving the `review` lifecycle end to end. It ran the binary as a black box through a set of offline, deterministic journeys and compared builds. It was a separate Go module, so the product never depended on it.

It was removed on 2026-10-12 to keep the repository lean.

| Item | Value |
| --- | --- |
| Removal commit | `c3a2c116` — `chore(bench): remove the bench module and its skill` |
| Last commit with the bench | `d20ab0c3` |
| Also removed | the bench skill (`skills/` and `internal/assets/skills/`), its `model` skill constant, and its CI steps |

At `d20ab0c3` the bench builds against the `github.com/carsimax/brunnr-code` module. Identifiers renamed in later commits (schema IDs, `gentle-ai-bench` names) still use the old names there.

## Restoring

Restore only what a change needs: the runner core plus the journeys that cover the changed review behavior. Do not restore the whole module, and do not port it from upstream Gentle AI, which has diverged.

```sh
git show --stat d20ab0c3 -- bench          # browse what existed
git checkout d20ab0c3 -- bench/go.mod bench/main.go bench/runner.go   # runner core (adjust as needed)
git checkout d20ab0c3 -- bench/journeys_<name>.go                     # only the journeys you need
```

Leave out the old-state compatibility journeys (`journeys_issue2879.go`, `journeys_issue2995.go` and their `testdata/` fixtures): they replay state captured from upstream releases, which Brunnr-Code no longer reads.
