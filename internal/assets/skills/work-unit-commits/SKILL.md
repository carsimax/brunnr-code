---
name: work-unit-commits
description: "Plan commits as reviewable work units. Trigger: implementation, commit splitting, worktrees, release tags, or keeping tests and docs with code."
license: Apache-2.0
metadata:
  author: gentleman-programming
  version: "1.1"
---

## When to Use

Load this skill when deciding what belongs in each commit or PR.

Use it for:

- Splitting a feature into reviewable work.
- Preparing commits before opening a PR.
- Turning a large change into chained or stacked PRs.
- Keeping reviewer cognitive load healthy.
- Closing an ODD task with a work-unit commit.
- Choosing a branch or worktree, or picking a release version.

## Critical Rules

| Rule | Requirement |
|------|-------------|
| Commit by work unit | A commit represents a deliverable behavior, fix, migration, or docs unit. |
| Do not commit by file type | Avoid `models`, then `services`, then `tests` if none works alone. |
| Keep tests with code | Tests belong in the same commit as the behavior they verify. |
| Keep docs with the user-visible change | Docs belong with the feature or workflow they explain. |
| Tell a story | A reviewer should understand why each commit exists from its diff and message. |
| Future PR-ready | Each commit should be a candidate chained PR when the change grows. |
| Separate formatting from behavior | A `gofmt`-only or rename-only change is its own commit. |
| Save points | Commit after each green step, so a failed step costs at most one increment. Inspect `git diff HEAD` before discarding work, and never run `git reset --hard` without the user's approval. |
| Budget is not code-golf | Never shrink a diff by deleting comments, blank lines, docs, or tests, or by compressing code, to fit the review budget (400 by default, or the session `review_budget_lines`). Slice by work unit or report the overage. |

## Verification

Before committing, confirm:

- [ ] The commit has one clear purpose.
- [ ] The repo still makes sense after applying only this commit.
- [ ] Tests or docs for this unit are included when relevant.
- [ ] Rollback is reasonable without reverting unrelated work.
- [ ] Focused test command and exact result are recorded.
- [ ] Runtime harness command/scenario and exact result are recorded, or explicit `N/A` explains why no runtime boundary exists.
- [ ] Rollback boundary names the exact files/behavior removable without unrelated work.
- [ ] The commit message explains the outcome, not the file list.
- [ ] `git diff --cached` reviewed: no secrets, tokens, `.env` files, or build artifacts staged.
- [ ] Out-of-scope issues noticed but not touched are listed in the task report, not fixed silently.

## Split Examples

| Weak split | Better work-unit split |
|------------|------------------------|
| `add models` | `feat(auth): add token validation domain model and tests` |
| `add services` | `feat(auth): wire token validation into login flow` |
| `add tests` | Tests included with each behavior commit |
| `update docs` | Docs included with the user-facing change they explain |

## PR Relationship

Use work-unit commits as the foundation for chained PRs:

1. Build the smallest independent work unit.
2. Include verification for that unit.
3. Commit it with a Conventional Commit message.
4. If the PR approaches 400 changed lines, promote commits or groups of commits into chained PRs.

## Branches and Worktrees

- Branch from `main`, one feature per branch. Keep the branch short-lived and delete it after merge.
- When another agent or session uses the same checkout, never switch its branch. Create a sibling worktree instead:

```bash
git worktree add ../<repo>-worktrees/<feature> -b <type>/<feature> main
git worktree remove ../<repo>-worktrees/<feature>
```

- Never force-push a branch someone else has pulled.

## Release Relationship

- The `v*` tag is the version's source of truth; the build injects it through `-ldflags`. Never hand-edit a version constant.
- The release changelog is generated from commit messages, so the Conventional Commit subject is the user-facing changelog line. Write it for users.
- Pick the bump by what users can observe: a breaking CLI flag, config, or contract output is major; new behavior is minor; a fix is patch. When unsure whether a change breaks users, treat it as breaking. `feat!` or a `BREAKING CHANGE:` footer marks a major.

## ODD Relationship

Organic Driven Development (ODD) closes every substantial task with a work-unit commit on the feature branch:

- Every ODD task closes with at least one work-unit commit, branch first when on the default branch, with tests and docs alongside the behavior and a Conventional Commit message.
- The native review candidate is that commit, or the PR slice it belongs to when review is deferred, against the previous reviewed boundary. It is never a TODO checkbox and never the accumulated feature branch.
- The running authored line count from work-unit commits feeds the delivery-strategy vocabulary: `ask-on-risk`, `auto-chain`, `single-pr`, `exception-ok`.
- The ODD feature document records the commit identity as evidence and, once a delivery strategy applies, the chosen chain strategy and slice boundaries (which commits each PR holds).

## Common Rationalizations

| Rationalization | Reality |
|-----------------|---------|
| "I'll commit when the feature is done" | One giant commit cannot be reviewed, bisected, or reverted. Commit each work unit. |
| "I'll squash it all later" | Squashing erases the work-unit story that review and ODD evidence rely on. |
| "I'll split this later" | Splitting after the fact rewrites history and mixes concerns. Split before committing. |
| "Small fix, bump the patch" | Diff size is irrelevant: changing behavior users relied on is breaking. |

## Red Flags

- Uncommitted changes accumulating across tasks.
- Commit messages like `fix`, `update`, or `misc`.
- Formatting mixed with behavior in one commit.
- A branch switched in a checkout another agent is using.

## Commands

```bash
# Review the story before committing
git diff --stat
git diff --cached --stat

# Check recent commit style
git log --oneline -5
```
