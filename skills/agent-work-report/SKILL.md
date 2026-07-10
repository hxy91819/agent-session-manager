---
name: agent-work-report
description: Generate Chinese daily and weekly work reports from local coding-agent sessions by calling asm report. Use when the user asks for an Agent work summary, today's work, yesterday's work, last week's work, daily report, weekly report, 日报, 周报, 今日工作总结, 今天工作总结, 昨天工作总结, 上周工作总结, or wants Codex to classify and summarize recent agent sessions.
---

# Agent Work Report

Use `asm report` as the source of truth. Do not inspect provider-private session stores directly unless the command is unavailable and the user explicitly asks for fallback investigation.

## Workflow

1. Choose the period:
   - Use `--period today` for 今日, 今天, so far today, or 截止当前 requests.
   - Use `--period yesterday` for 日报, yesterday, 昨天, or daily-report requests.
   - Use `--period last-week` for 周报, last week, 上周, or weekly-report requests.
   - Use `--period last-7-days` for 最近 7 天, 7 天内, rolling-week, or recent-week requests.
   - Use `--start <time> --end <time>` when the user asks for a specific custom window. `--end` is exclusive; for example, use `--start 2026-06-17 --end 2026-06-18` for June 17 only.
   - Accepted custom time formats are `YYYY-MM-DD`, local `YYYY-MM-DD HH:MM[:SS]`, and RFC3339.
2. Run the CLI:
   - Prefer an installed binary: `asm report --period <period>`.
   - For custom windows, run `asm report --start <time> --end <time>`.
   - If working inside this repository and `asm` is not installed, use `go run ./cmd/asm report --period <period>`.
   - Pass through any user-requested filters with `--query`.
3. If the default previews are not enough to classify a session, prefer incremental loading before asking for a larger full window:
   - First incremental pass: add `--preview-messages-per-edge 2 --preview-edge-offset 2`.
   - Second incremental pass: add `--preview-messages-per-edge 2 --preview-edge-offset 4`.
   - If the snippets themselves are too short, rerun the needed pass with `--preview-max-chars 1000`.
   - Stop escalating when the report is clear enough; do not request full transcripts unless the user explicitly asks.
4. Read the JSON payload. Treat `sessions[].evidence[].text` as the only evidence that specific work happened inside the requested period. `sessions[].previews` is kept for compatibility and has the same in-window content, but prefer `evidence` because its name encodes the reporting contract.
   - Main `sessions`, `projects`, and `totals.sessions` include only sessions with timestamped in-window evidence.
   - `unverified_sessions` contains session-record update diagnostics without trustworthy message evidence. Never infer that user work occurred from these entries.
   - Read `coverage` before summarizing. Report `partial` or `unavailable` providers under risks when they could make the requested report incomplete.
   - Session titles are intentionally omitted from report output so a long-lived session's older topic cannot anchor the current report.
5. Classify work by project path first, then merge related sessions into themes using evidence text only.
   - Use `sessions[].resume_command` for copyable follow-up commands.
   - Include resume commands only for items that are useful to revisit, especially follow-up work.
6. Write the report in Chinese unless the user asks for another language.

## Output Format

For 日报:

```markdown
## 工作概览
- 会话数：<n>，项目数：<n>
- 主要方向：<1-3 个主题>

## 完成事项
- <项目或主题>：<仅基于窗口内 evidence 的具体成果>

## 后续跟进
- <仍在推进、需要确认、需要明天继续或下周继续的事项>  
  `asm resume --provider '<provider>' '<session-id>'`

## 风险与阻塞
- <缺失信息、失败检查、需要人工决策或环境问题；没有就写“暂无明确阻塞”>
```

For 周报, use the same evidence rules; group `完成事项` and `后续跟进` by project or workstream when there are many sessions.

## Evidence Rules

- Mention verified provider/session counts only from `totals`; label `totals.unverified_sessions` separately if it is non-zero.
- Prefer concise synthesis over listing every session.
- Never use `unverified_sessions` as work evidence; mention them only as coverage diagnostics when relevant.
- Preserve uncertainty: use “看起来/主要是/可能需要” when the payload only implies intent.
- Do not include raw session IDs unless the user asks for traceability.
- Prefer `resume_command` over raw IDs when a follow-up needs a session reference.
