---
name: cross-agent-pr-review
description: Review agent-session-manager pull requests for bug classes and behavior changes shared across providers, then define contributor scope and maintainer follow-up. Use when reviewing a PR that changes provider parsing, discovery, caching, cwd handling, filtering, report evidence, resume behavior, or another mechanism that may affect Codex, Claude Code, Kimi Code, opencode, CodeBuddy, Cursor, OpenClaw, or ZCode.
---

# Cross-Agent PR Review

Review the mechanism, not only the provider named in the PR. Establish whether the same failure mode exists elsewhere and ensure maintainers own the remaining cross-provider work.

## Workflow

1. Verify the reported problem and the proposed root cause in the PR's target provider.
2. Name the underlying bug class, for example:
   - bounded JSONL record reading;
   - timestamp or content-block parsing;
   - stale parser cache identity;
   - cwd resolution and resume safety;
   - report-window evidence selection;
   - provider file ordering or limits.
3. Inventory every supported provider against that mechanism:
   - Codex
   - Claude Code
   - Kimi Code
   - opencode
   - CodeBuddy
   - Cursor
   - OpenClaw
   - ZCode
4. Read each provider's actual storage and parsing path. Classify it as:
   - affected with code or reproduction evidence;
   - not affected because it uses a different storage model;
   - unknown because evidence is unavailable.
   Do not infer commonality from product names alone.
   Record both the shared primitive and trigger reachability. A provider may use
   the same scanner while its compact index schema cannot contain the oversized
   transcript payload that caused the reported bug.
5. Use `behavior-e2e-validation` to construct the public-boundary proof:
   - First lock the target provider's failure and fix.
   - Then add the smallest cross-provider matrix that reproduces the same mechanism.
   - Keep per-day or other windowed selection contracts identical across providers where applicable.
6. Evaluate the PR scope:
   - Require the contributor's fix to be correct, tested, and accurately scoped.
   - Generally do not require the contributor to repair every affected provider.
   - Do require the PR description or review outcome to identify confirmed sibling providers and avoid claiming a repository-wide fix.
   - Request broader contributor scope only when the narrow change introduces an unsafe shared abstraction, makes later repair harder, or leaves the submitted provider incompletely fixed.
7. Create a maintainer follow-up plan for confirmed sibling bugs:
   - Prefer a shared utility when providers truly have the same low-level contract.
   - Keep provider-specific parsing inside provider packages when formats differ.
   - Add or update end-to-end tests with the follow-up fix.
   - Open a maintainer-owned follow-up PR promptly; use a stacked PR when it must build on the contributor branch, otherwise target the default branch after the original PR lands.
   - Record an owner and PR/issue link when writes are authorized. In a read-only
     review, provide the proposed title, scope, and owner instead of silently
     leaving the follow-up implicit.

## Review Output

Return four explicit sections:

1. **Current PR correctness** — whether the named bug is reproduced and fixed.
2. **Cross-agent assessment** — a provider-by-provider affected/not-affected/unknown matrix with storage path, shared primitive, trigger reachability, impact, and evidence.
3. **Contributor action** — only changes reasonably required in the submitted PR.
4. **Maintainer follow-up** — concrete PR scope, tests, and shared ownership for the remaining affected providers.

Treat confirmed data loss, disappearing sessions, unsafe resume, or silently incomplete report evidence as high-priority maintainer follow-up even when it does not block the contributor PR.
