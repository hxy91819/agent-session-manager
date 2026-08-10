# Changelog

## v0.8.3

Changes since [v0.8.2](https://github.com/hxy91819/agent-session-manager/compare/v0.8.2...v0.8.3).

### Changes

- perf: skip Claude subagent transcripts during discovery ([#50](https://github.com/hxy91819/agent-session-manager/pull/50)). Thanks @hxy91819.
- perf: parse Claude cold cache misses concurrently ([#49](https://github.com/hxy91819/agent-session-manager/pull/49)). Thanks @hxy91819.
- perf: reduce Claude cold startup parsing ([#48](https://github.com/hxy91819/agent-session-manager/pull/48)). Thanks @hxy91819.
- Mark TUI startup P3 landed ([#47](https://github.com/hxy91819/agent-session-manager/pull/47)). Thanks @hxy91819.
- Incrementally parse Codex dynamic title indexes ([#46](https://github.com/hxy91819/agent-session-manager/pull/46)). Thanks @hxy91819.
- perf: add Codex metadata fast path for TUI startup ([#45](https://github.com/hxy91819/agent-session-manager/pull/45)). Thanks @hxy91819.
- perf: parse Codex cold cache misses concurrently ([#44](https://github.com/hxy91819/agent-session-manager/pull/44)). Thanks @hxy91819.
- ci: establish diff-aware agent code health reporting ([#41](https://github.com/hxy91819/agent-session-manager/pull/41)). Thanks @hxy91819.
- fix: restore Codex subagent sessions and startup cache hits ([#42](https://github.com/hxy91819/agent-session-manager/pull/42)). Thanks @hxy91819.
- Clarify work reports with project source tags ([#43](https://github.com/hxy91819/agent-session-manager/pull/43)). Thanks @hxy91819.
- perf: optimize TUI startup cache and Codex parsing ([#40](https://github.com/hxy91819/agent-session-manager/pull/40)). Thanks @hxy91819.
- test: establish TUI startup optimization baselines ([#39](https://github.com/hxy91819/agent-session-manager/pull/39)). Thanks @hxy91819.
- Add TUI agent picker for new sessions ([#38](https://github.com/hxy91819/agent-session-manager/pull/38)). Thanks @hxy91819.
- Add Kiro CLI session provider ([#37](https://github.com/hxy91819/agent-session-manager/pull/37)). Thanks @hxy91819.
- feat: deliver reports to local file and Telegram (`48854c1`). Thanks Mason Huang.
- feat: add Ollama Cloud report pipeline (`a6204c5`). Thanks Mason Huang.

### Contributors

Thanks @hxy91819 and Mason Huang for this release.

## v0.8.2

Changes since [v0.8.1](https://github.com/hxy91819/agent-session-manager/compare/v0.8.1...v0.8.2).

### Changes

- Bump modernc.org/sqlite from 1.52.0 to 1.54.0 in the go-modules group ([#31](https://github.com/hxy91819/agent-session-manager/pull/31)).
- Bump actions/setup-go from 6 to 7 in the github-actions group ([#30](https://github.com/hxy91819/agent-session-manager/pull/30)).
- Fix historical report completeness under --limit ([#28](https://github.com/hxy91819/agent-session-manager/pull/28)). Thanks @momothemage.
- Keep healthy sessions when provider discovery fails ([#26](https://github.com/hxy91819/agent-session-manager/pull/26)). Thanks @momothemage.
- Preserve business scope in work reports (`2e69621`). Thanks masonxhuang.

### Contributors

Thanks @momothemage and masonxhuang for this release.

## v0.8.1

Changes since [v0.8.0](https://github.com/hxy91819/agent-session-manager/compare/v0.8.0...v0.8.1).

### Changes

- Verify release binaries use patched Go ([#32](https://github.com/hxy91819/agent-session-manager/pull/32)). Thanks @hxy91819.
- Harden Go release security checks ([#29](https://github.com/hxy91819/agent-session-manager/pull/29)). Thanks @hxy91819.
- Fix report evidence completeness across providers ([#25](https://github.com/hxy91819/agent-session-manager/pull/25)). Thanks @hxy91819.
- Exclude injected Codex context from report evidence ([#22](https://github.com/hxy91819/agent-session-manager/pull/22)). Thanks @momothemage.
- Document session review guardrails ([#24](https://github.com/hxy91819/agent-session-manager/pull/24)). Thanks @hxy91819.
- Reconcile reviewed local session improvements ([#23](https://github.com/hxy91819/agent-session-manager/pull/23)). Thanks @hxy91819.

### Contributors

Thanks @hxy91819 and @momothemage for this release.

## v0.8.0

Changes since [v0.7.1](https://github.com/hxy91819/agent-session-manager/compare/v0.7.1...v0.8.0).

### Changes

- Refresh existing release changelog sections ([#20](https://github.com/hxy91819/agent-session-manager/pull/20)). Thanks @hxy91819.
- Ignore changelog-only merge commits ([#19](https://github.com/hxy91819/agent-session-manager/pull/19)). Thanks @hxy91819.
- Automate attributed release changelogs ([#15](https://github.com/hxy91819/agent-session-manager/pull/15)). Thanks @hxy91819.
- Exclude detected non-interactive sessions ([#14](https://github.com/hxy91819/agent-session-manager/pull/14)). Thanks @hxy91819.
- Add end-to-end and cross-agent review skills ([#16](https://github.com/hxy91819/agent-session-manager/pull/16)). Thanks @hxy91819.
- Keep sibling sessions after oversized JSONL records ([#17](https://github.com/hxy91819/agent-session-manager/pull/17)). Thanks @hxy91819.
- Keep Codex sessions after oversized JSONL records ([#13](https://github.com/hxy91819/agent-session-manager/pull/13)). Thanks @momothemage.

### Contributors

Thanks @hxy91819 and @momothemage for this release.

## v0.7.1

Changes since [v0.7.0](https://github.com/hxy91819/agent-session-manager/compare/v0.7.0...v0.7.1).

### Changes

- Preserve Codex subagent turn context ([#11](https://github.com/hxy91819/agent-session-manager/pull/11)). Thanks @hxy91819.
- Require E2E coverage for user-visible changes ([#12](https://github.com/hxy91819/agent-session-manager/pull/12)). Thanks @hxy91819.
- Add end-to-end coverage for recent behavior changes ([#10](https://github.com/hxy91819/agent-session-manager/pull/10)). Thanks @hxy91819.
- Fix Codex subagent session identity ([#9](https://github.com/hxy91819/agent-session-manager/pull/9)). Thanks @momothemage.

### Contributors

Thanks @hxy91819 and @momothemage for this release.
