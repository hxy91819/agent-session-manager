---
name: code-health-review
description: Analyze agent-session-manager code-health reports and current branch or PR changes, then recommend proportionate fixes without editing code. Use when the user invokes code-health-review, asks for code-health analysis or 防腐分析, wants help interpreting a Code health report artifact, or needs a pass/fix/accept recommendation for a PR.
---

# Code Health Review

Return a decision, not a score. Treat existing debt as context and isolate regressions introduced by the analyzed change.

## 1. Resolve the evidence

1. Read `AGENTS.md`, `docs/agent-code-health-experiment.md`, and
   `tools/check-code-health/config.json` completely.
2. Run `git status --short` and preserve all existing changes.
3. If the user provides `code-health.json`, validate its `schema_version` and analyze it directly.
4. Otherwise resolve an explicit comparison:
   - Use a user-provided base/head when present.
   - For a GitHub PR, prefer its downloaded CI artifact. If rerunning locally, compare the PR base
     revision with the tested merge revision when available.
   - For a local branch, fetch `origin/master`, use `git merge-base HEAD origin/master` as base,
     and analyze the current worktree by omitting `--head`. State when dirty changes are included.
5. Write JSON only to a temporary directory and run:

   ```sh
   go run ./tools/check-code-health --base <base> [--head <head>] --format json
   ```

Stop on exit 2 and report the operational error. Default report mode returning 0 can still contain
`would_fail: true`; read the JSON verdict.

## 2. Separate change from debt

- Treat `comparison.violations` as candidate regressions from this change.
- Treat head threshold observations as current debt unless base/head values prove growth.
- Keep test findings report-only.
- Check function/KLOC, micro-function ratio, package file count, changed files, and added-helper call
  ratio for fragmentation or metric gaming.
- For clone groups, inspect every fragment and recommend sharing only when business semantics and
  expected evolution match.

## 3. Form recommendations

Prioritize changed symbols and propose the smallest structural response:

- Cognitive or cyclomatic growth: reduce nested decisions, implicit states, or mixed parsing/I/O.
- Function length or statements: identify a responsibility boundary; avoid mechanical extraction.
- File growth: split by ownership or lifecycle only when it reduces simultaneous context.
- Clone growth: consolidate stable invariants while keeping provider formats provider-local.

Classify each item as `fix before merge`, `consider`, or `accept during Stage C`. Do not edit code,
change thresholds, add suppressions, or create exceptions unless the user explicitly requests it.

## 4. Report

Return these sections:

1. **Decision** — `pass`, `fix recommended`, or `analysis failed`, with base and analyzed revision.
2. **Regressions** — metric, symbol/path, base → head, and why it matters.
3. **Recommendations** — ordered actions tied to each regression.
4. **Observations** — existing hotspots, test-only findings, and anti-gaming signals.
5. **Next action** — what the maintainer should ask the Agent to do; during Stage C, state that
   `would-fail` remains non-blocking and should be recorded.

Finish only after every violation is classified and every recommendation names its evidence.
