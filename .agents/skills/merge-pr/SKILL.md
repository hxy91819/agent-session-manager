---
name: merge-pr
description: Review and deterministically squash-merge an agent-session-manager GitHub pull request when the user asks to land or merge it. Use for a specific PR, not for read-only review, triage, or candidate discovery.
---

# Merge PR

Land a prepared PR only after the reviewed head and its evidence are pinned. A
land request authorizes the merge itself, not unrelated edits, branch rewrites,
admin bypasses, releases, or destructive cleanup.

## 1. Resolve the target and authority

1. Require a PR number or URL and resolve it in
   `hxy91819/agent-session-manager`.
2. Read the live PR state, author, base branch, draft status, head SHA,
   mergeability, reviews, conversations, and check runs. The PR must be open,
   non-draft, and target `master`.
3. Record the head SHA. Every later review, check, and merge decision applies
   only to this SHA.
4. Check `git branch --show-current`, `git status --short`, and
   `git worktree list` before using a local checkout. Preserve all parallel
   work and use an existing matching worktree when available. Repository policy
   requires explicit user approval before creating, switching, or removing a
   branch or worktree.

## 2. Classify author trust

Treat the author as trusted for **intent review** when either condition holds:

- the user explicitly identifies the author or this PR as trusted in the
  current request; or
- a live GitHub collaborator permission check reports `write`, `maintain`, or
  `admin` for the author.

For a trusted author, accept the stated product goal as authorized. Skip motive,
account-age, activity-history, product-priority, and “should we want this
change?” investigation. Start directly with whether the implementation
correctly delivers the stated goal.

Trust does not waive technical review. Always verify that the diff matches the
stated goal, changes only justified scope, preserves security and compatibility,
and has proportionate regression evidence. Intent trust also does not authorize
credentialed or local execution of PR code.

For any other author, establish that the stated intent fits the repository and
that the diff contains no unrelated or suspicious behavior before continuing.
Use identity or activity only as a risk signal; code and evidence decide.

## 3. Review the pinned head

Read beyond the diff: inspect the changed path, its caller and owner boundary,
adjacent tests, sibling providers or flows sharing the invariant, and current
`master` behavior so an obsolete or duplicate implementation is not landed.

- For a bug fix, require a focused regression test that fails for the product
  reason on the affected base and passes on the PR head. If a feasible public
  boundary exists, require end-to-end coverage through `behavior-e2e-validation`.
- For provider parsing, discovery, caching, cwd, filtering, report evidence, or
  resume behavior, use `cross-agent-pr-review` and classify sibling providers.
- For user-observable behavior, confirm the external contract and that tests
  assert behavior rather than implementation details.
- Inspect the CI code-health artifact when it reports regressions. Stage C
  metric findings are advisory, but genuine structural regressions must receive
  an explicit fix/accept decision.
- Address unresolved review conversations that identify actionable defects.
  Do not mark another person's conversation resolved merely to make the PR
  appear ready.

For untrusted PR code, prefer GitHub-hosted evidence and read-only inspection.
Do not execute it on a credentialed host without explicit approval and an
appropriate isolation plan. For trusted PR code, local execution still follows
the shared-workspace and worktree rules above.

The review is complete only when every material finding is fixed or explicitly
accepted with evidence, and the exact pinned head is the head reviewed.

## 4. Enforce landing gates

Before merging:

1. Re-read the PR and confirm the head SHA is unchanged, the branch is
   mergeable, and no new blocking conversation or review exists.
2. Check live branch protection and rulesets, then require the latest CI
   workflow for the pinned head to finish successfully,
   including code health, lint, vulnerability scan, and the Linux, macOS, and
   Windows test jobs. When repository rules do not enforce all of these jobs,
   green repository CI remains an explicit skill gate.
3. Require the checks appropriate to the changed surface from `AGENTS.md`,
   including provider performance and end-to-end evidence where applicable.
   For workflow changes, require `actionlint` on the changed workflow.
4. Never use `--admin`, auto-merge, or a merge queue as a substitute for
   completed evidence. Do not merge a conflict, stale reviewed head, or pending
   check.
5. Recheck the current branch, `git status --short`, and `git worktree list`.
   Parallel changes are not part of the PR and must remain untouched.

If a fix or branch rewrite is needed, stop before mutation unless the user has
also authorized that work. After any pushed change, pin the new head and repeat
review and landing gates.

## 5. Merge and verify

Use a head-pinned squash merge without branch deletion:

```sh
gh pr merge <PR> \
  --repo hxy91819/agent-session-manager \
  --squash \
  --match-head-commit <PINNED_HEAD_SHA>
```

Then query the PR again. Completion requires all of the following:

- state is `MERGED` rather than merely `CLOSED`;
- `mergedAt` and `mergeCommit` are present;
- the reported source head equals the pinned, reviewed SHA.

Report the PR URL, source head SHA, merge commit SHA, CI result, and whether
intent review was performed or skipped under the trusted-author rule. Report a
specific gate or authorization blocker instead of weakening it.
