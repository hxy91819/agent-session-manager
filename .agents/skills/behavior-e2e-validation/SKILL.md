---
name: behavior-e2e-validation
description: Design and run end-to-end regression tests for user-observable behavior changes and bug fixes in agent-session-manager. Use when implementing, validating, or reviewing changes to CLI contracts, JSON output, report evidence, resume flows, TUI behavior, provider integration behavior, or any bug that can be reproduced through a public boundary.
---

# Behavior E2E Validation

Lock the product contract at the public boundary. Keep focused unit tests, but do not use them as a substitute when an end-to-end reproduction is feasible.

## Workflow

1. State the observable contract before editing code:
   - Identify the command or user flow.
   - Specify the fixture, output, exit status, and safety behavior that distinguish success from failure.
   - Treat provider files as fixture setup, not as the assertion surface.
2. Decide whether repository-local end-to-end validation is feasible:
   - Prefer `tests/e2e_test.go` and invoke `go run ./cmd/asm`.
   - Use temporary native provider stores and explicit `--<provider>-home` flags
     for the providers under test.
   - Isolate every unrelated provider home so real local sessions cannot affect
     results. A shared command helper may do this through a sanitized
     environment; inspect it before duplicating flags or reporting pollution.
   - If the behavior cannot cross a public boundary in this repository, document the concrete reason and add the closest owner-level test instead.
3. Add the regression test before the implementation for a bug fix:
   - Prove the test fails on the affected base revision for the expected product-level reason.
   - Do not count compilation, setup, module-path, or harness failures as a base
     reproduction.
   - Prove the same test passes with the fix.
   - For an intentional behavior change, prove the new public contract and update any conflicting existing contract test.
4. Build realistic fixtures:
   - Match the provider's supported on-disk schema, timestamps, cwd rules, and file layout.
   - Cross time boundaries when the behavior is windowed. For reports, select head and tail evidence independently inside each requested day.
   - Use `--since-days 0` or controlled mtimes when recency filtering is not the behavior under test.
   - Keep fixtures only as large as needed, except when size itself triggers the bug.
5. Assert behavior rather than implementation:
   - Assert JSON fields, selected sessions, evidence order, command output, error text, or rendered dimensions.
   - Do not assert private helper calls, buffer types, cache internals, or incidental formatting.
   - Check negative behavior too: excluded sessions, unsafe resume rejection, out-of-window evidence, or absence of duplicates.
   - For default-hidden classifications, pair the intended exclusion with a
     plausible normal or ambiguous session that must remain visible.
6. Check the bug class across providers:
   - When more than one provider exists, use `cross-agent-pr-review` and inspect
     every provider's actual reader path before classifying scope.
   - Identify providers that use the same storage or parsing mechanism.
   - Distinguish between merely using the same primitive and having a reachable
     equivalent trigger. For example, a bounded scanner over a compact index is
     not automatically equivalent to a transcript record carrying tool output.
   - Reuse one contract across affected providers when practical, using table-driven subtests or focused provider fixtures.
   - Do not force unrelated providers into a test when their storage model cannot exhibit the bug.
   - Never state that another provider is unaffected without code or reproduction evidence.
7. Run proportionate proof:
   - Run the new test against the base and fixed revisions.
   - Run focused owner tests.
   - Before handoff, run the repository checks required by `AGENTS.md`.

## Completion Evidence

Report:

- the public contract tested;
- why end-to-end validation was feasible or why it was not;
- the base failure and fixed pass;
- which providers share the mechanism and which do not;
- the evidence used to classify each provider and whether the trigger is reachable;
- the exact commands used.

Do not call a bug fixed when a feasible end-to-end regression test is still missing.
