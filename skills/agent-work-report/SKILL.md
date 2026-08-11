---
name: agent-work-report
description: Generate Chinese daily and weekly work reports from local coding-agent sessions, optionally enriched with Tencent Meeting history and smart minutes. Use only when the user asks to generate, view, or summarize work for a concrete time window, such as today, yesterday, last week, 日报, 周报, 今日工作总结, 昨天工作总结, or 上周工作总结. Do not use when the user is designing, implementing, modifying, debugging, reviewing, or configuring report scripts, report skills, meeting integrations, delivery automations, or report formatting.
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
3. When Tencent Meeting context is available, collect it for the exact same half-open window:
   - Keep `asm report` output. Meeting context enriches it and never replaces coding-session evidence.
   - Load `TENCENT_MEETING_TOKEN` from the environment. For a repository-local scheduled job, source the ignored root `.env` first.
   - Use the sibling `tencent-meeting-summary` skill and run `python3 skills/tencent-meeting-summary/scripts/collect-tencent-meeting-context.py --start <start> --end <end> --output <path>` from the repository root.
   - Treat the ended-meeting list as evidence that a meeting was on the user's meeting history. Treat smart minutes as secondary meeting context, not proof of coding work.
   - Prefer smart minutes. When they are unavailable, including when collection is `partial` or `unavailable`, use an available meeting subject to infer only a broad topic and explicitly label it “据会议名称推测”. Never infer decisions, owners, deadlines, or completed work from a subject. Do not request recording permission or submit feedback from an unattended report job.
   - If collection is `partial` or `unavailable`, continue with the asm report and use any available meeting subjects as the fallback context. Do not add a meeting-coverage risk solely because meeting details or smart minutes were unavailable; if no subject is available, omit unverified meeting content.
4. If the default previews are not enough to classify a session, prefer incremental loading before asking for a larger full window:
   - First incremental pass: add `--preview-messages-per-edge 2 --preview-edge-offset 2`.
   - Second incremental pass: add `--preview-messages-per-edge 2 --preview-edge-offset 4`.
   - If the snippets themselves are too short, rerun the needed pass with `--preview-max-chars 1000`.
   - Stop escalating when the report is clear enough; do not request full transcripts unless the user explicitly asks.
5. Read the JSON payload. Treat `sessions[].evidence[].text` as the only evidence that specific coding-agent work happened inside the requested period. `sessions[].previews` is kept for compatibility and has the same in-window content, but prefer `evidence` because its name encodes the reporting contract.
   - Main `sessions`, `projects`, and `totals.sessions` include only sessions with timestamped in-window evidence.
   - `unverified_sessions` contains session-record update diagnostics without trustworthy message evidence. Never infer that user work occurred from these entries.
   - Read `coverage` before summarizing. Report `partial` or `unavailable` asm providers under risks when they could make the requested report incomplete; Tencent Meeting detail failures follow the subject-only fallback rule above.
   - Session titles are intentionally omitted from report output so a long-lived session's older topic cannot anchor the current report.
6. Classify coding work by project path first, then merge related sessions into themes using evidence text only. Treat the path as a grouping hint, not a forced report boundary: merge technical steps serving the same outcome, but split independent deliverables when their results, status, or next steps differ. Merge meeting context afterward:
   - Use meetings to clarify decisions, ownership, deadlines, risks, and follow-ups related to evidence-backed coding themes.
   - Preserve materially distinct meeting-only workstreams and label them as meeting discussions or decisions, not completed coding work.
   - Do not copy long smart minutes verbatim. Synthesize only the context that improves the report.
   - Use `sessions[].resume_command` for copyable follow-up commands.
   - Include resume commands only for items that are useful to revisit, especially follow-up work.
7. Write the report in Chinese unless the user asks for another language.

## Morning Standup Writing Rules

Write the daily report for a cross-functional morning standup attended by product managers, project managers, and engineers:

1. Build a list of projects or business matters before writing. Use `cwd`/project path as the initial grouping hint. Merge technical subtasks that serve the same outcome. Keep one numbered top-level item for a project and represent its independent deliverables, statuses, or next steps as child tasks. Keep different projects separate unless they contribute to the same outcome.
2. Make `工作概览` matter-oriented rather than session-oriented. Use one numbered top-level item per project or cross-project matter, ordered by importance.
   - Build a source-tag map from the contributing sessions before writing. Use the shortest human-readable project name that uniquely maps to a `cwd` in this report, normally the path basename; use `parent/name` only to disambiguate collisions.
   - Every top-level item in `工作概览`, `后续跟进`, and `风险与阻塞` must carry one or more source-project tags. Put one tag per contributing project, preserving multiple tags when an item truly merges evidence from multiple projects.
   - Tags identify evidence sources, not themes, technologies, providers, or agent names. Use `[会议]` for meeting-only items without a project path and `[全局]` for report-level coverage or “暂无明确阻塞”.
3. Prefix every numbered top-level item with exactly one relative effort level:
   - `[高投入]` means the matter was a primary, sustained focus in the report window.
   - `[中投入]` means the matter had substantive progress or discussion but was a secondary focus.
   - `[低投入]` means the matter was a brief follow-up, isolated discussion, or small supporting task.
   - Estimate the level from meeting duration, session timing and count, and the continuity and complexity of the evidence. Meeting duration is a meaningful signal, but a routine or long meeting is not automatically high effort.
   - Do not mechanically convert message count or session elapsed time into labor hours. The labels express relative attention, not measured attendance or precise time tracking.
   - Judge daily reports relative to that day and weekly reports relative to the whole reporting week. Multiple matters may share a level; do not force all three levels to appear.
   - Show only the level. Do not add effort percentages or percentage ranges.
4. Write for direct retelling: after reading an item once, a colleague should be able to say what changed, what the result was, and what remains. Assume readers do not know repository internals.
   - 先写事实，再写概括。优先使用“修复、清理、发布、验证、定位、编写、决定”等能落到对象上的动词。
   - “梳理、推进、沉淀、适配、验收、收敛”只能作为过程补充，不能代替具体对象与结果。若删掉这些词后读者仍不知道做了什么，就重写。
   - Tests, builds, experiments, and reviews must state an evidence-backed verdict such as 通过、失败、未完成或待确认. Do not replace the verdict with “完成验证”“形成结论”“主闭环通过”.
   - Keep completion, partial completion, and plans visibly distinct. When evidence does not prove the result, preserve uncertainty instead of upgrading activity into an outcome.
   - An imperative or request such as “帮我、请、需要、想要、试一下” proves requested work or intent, not completion. Write “开始/安排/尝试/待确认” unless later evidence explicitly reports the result.
   - Before finalizing, audit every “已、完成、通过、修复、发布” against an explicit result in the evidence. Downgrade the verb to activity or intent when the evidence only contains a request.
5. Preserve concrete business scope while abstracting implementation details:
   - 核心原则：抽象实现细节，不得抽象业务范围。
   - Keep evidence-backed product names, business capabilities, affected workflows, and cleanup targets when they distinguish what was actually worked on. Do not replace them with vague labels such as “核心服务”“相关功能”“业务逻辑” or “冗余代码”.
   - When one project contains tasks with distinct outcomes or next steps, keep one top-level project item and use child task bullets. Keep multiple business targets in one child when they share the same deliverable and next step.
   - For example, write “清理 IPv6 合并限速与 COS 免费套餐包两项灰度控制” instead of “清理核心服务冗余代码”.
   - Continue to abstract repository internals and low-level implementation details unless a detail is essential to a decision or blocker:
   - Do not normally include API paths, command flags, environment variables, commit hashes, PR numbers, test names, internal metric values, class names, or low-level architecture terms.
   - Translate internal English labels and tool names into their business meaning when that meaning is known. Omit parenthetical English aliases that do not help a reader decide or unblock work.
   - Replace a diagnosis such as “`/api/status` 因指标过多变慢” with “推进管理面板加载缓慢问题的定位与优化”.
   - Translate low-level causes into the affected user or delivery behavior. If the cause is required to unblock work, state it briefly after the concrete symptom or status.
6. Choose the overview shape from the number of independent tasks:
   - For one task, use one Markdown line: “`[投入等级] [项目标签] 项目/事项：进展；下一步：计划`”. When multiple projects contribute, write consecutive tags such as “`[Lightpower] [lightginkgo]`”.
   - For two or more tasks, write a project-only numbered parent with the effort and source tags, then four-space-indented “`- 任务：进展；下一步：计划`” bullets. Child tasks inherit the parent tags. Add a child next step when the evidence supports one.
   - Use one child level and at least two child bullets. Split by distinct outcomes or next steps, not by session, pull request, test, or internal delivery stage such as PR1/PR2/PR3.
   - Keep each flat item or child task on one line. Allow two to three short progress sentences when one sentence would hide distinct facts. Aim for about 140 Chinese characters and never exceed 180 characters per line; retain only standup-relevant results when needed.
7. Merge meeting decisions into the related project item. When meetings exist, ensure at least one overview item reflects meaningful meeting work; group routine meetings instead of listing every title.
8. Preserve uncertainty for title-only meetings with “据会议名称推测”, but omit them when the inference adds no useful standup context.
9. Fold completed progress into the corresponding `工作概览` item; do not create a separate `完成事项` section. Apply the same audience-friendly abstraction to `后续跟进` and `风险与阻塞`. Summarize the decision needed or user-visible impact instead of the underlying mechanism. Include technical detail only when someone needs that exact detail to make a decision or unblock work.
10. Keep a daily standup report concise: normally no more than about 1,200 Chinese characters excluding headings. Remove background explanations, exhaustive evidence coverage, and details already implied by a higher-level status.
11. Before answering, silently verify:
   - every numbered overview item begins with exactly one valid effort level and the whole overview contains no effort percentage;
   - every top-level item has all and only its source-project tags, including multiple tags for merged evidence;
   - projects with multiple independent tasks use one project-only parent and at least two child bullets;
   - single-task projects stay on one line, and child tasks are not split by session or internal delivery stage;
   - different project paths have not been accidentally merged;
   - meeting work is represented when present;
   - a non-engineer can understand every overview item without explanation;
   - evidence-backed business objects have not been generalized into vague project-level labels;
   - every build, test, experiment, or review states its actual verdict instead of a generic process label;
   - no low-level detail can be replaced by a clearer affected behavior or status;
   - the report does not contain internal delivery labels, code identifiers, or engineering log language;
   - completed work, plans, and meeting discussions remain distinguishable.

## Output Format

For 日报:

```markdown
## 工作概览
1. [高投入] [<项目>] <包含多个独立任务的项目或事项>
    - <任务一>：<进展与结果>；下一步：<有证据支持的简短计划>
    - <任务二>：<进展与结果>
2. [中投入] [<项目一>] [<项目二>] <单一任务的事项>：<进展与结果>；下一步：<简短计划>

## 后续跟进
- [<项目>] <仍在推进、需要确认、需要明天继续或下周继续的事项>
  `asm resume --provider '<provider>' '<session-id>'`

## 风险与阻塞
- [<项目或全局>] <缺失信息、失败检查、需要人工决策或环境问题；没有就写“暂无明确阻塞”>
```

For 周报, use the same evidence and effort-level rules; evaluate effort relative to the whole reporting week, then group `工作概览` and `后续跟进` by project or workstream when there are many sessions.

## Evidence Rules

- Mention verified provider/session counts only from `totals`; label `totals.unverified_sessions` separately if it is non-zero.
- Prefer concise synthesis over listing every session.
- Never use `unverified_sessions` as work evidence; mention them only as coverage diagnostics when relevant.
- Preserve evidence-backed smaller workstreams when they are materially distinct from the dominant themes, even if they have only one short session.
- Preserve uncertainty: use “看起来/主要是/可能需要” when the payload only implies intent.
- Do not include raw session IDs unless the user asks for traceability.
- Prefer `resume_command` over raw IDs when a follow-up needs a session reference.
- Never present smart minutes as direct evidence that implementation was completed; use wording such as “会议明确/会议讨论/会议待办”.
