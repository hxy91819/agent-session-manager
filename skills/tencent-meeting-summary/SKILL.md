---
name: tencent-meeting-summary
description: Collect concise Tencent Meeting context for a daily or weekly work report by listing ended meetings and reading available Tencent Meeting smart minutes. Use when a report needs meeting subjects, decisions, owners, deadlines, risks, or follow-ups without downloading and reprocessing full transcripts.
---

# Tencent Meeting Summary

Collect meeting context for the same half-open time window as the work report. Keep this workflow read-only and lightweight.

## Workflow

1. Ensure `TENCENT_MEETING_TOKEN` is present in the environment. For this repository's local scheduled job, source the ignored root `.env`.
2. Run:

   ```sh
   python3 scripts/collect-tencent-meeting-context.py \
     --start <inclusive-iso-time> \
     --end <exclusive-iso-time> \
     --output <meeting-context.json>
   ```

3. Read the generated JSON:
   - `meetings` contains ended meeting subjects and times.
   - `smart_minutes` contains Tencent Meeting's available AI summaries.
   - `status` is `ok`, `partial`, or `unavailable`.
   - `errors` and `traces` describe source coverage and request traceability.
4. Summarize only context that improves the report: decisions, explicit owners, deadlines, risks, and follow-ups.

## Evidence Rules

- Treat meeting history as evidence that a meeting appeared in the user's ended-meeting list.
- Treat smart minutes as secondary meeting context, not proof that coding or delivery work was completed.
- Label meeting-only content as “会议讨论”, “会议明确”, or “会议待办”.
- Do not download or reprocess full transcripts for routine reports.
- Do not request recording permission, schedule or modify meetings, or submit feedback from unattended report jobs.
- If smart minutes are unavailable, including when collection is `partial` or `unavailable`, infer only the broad topic from each available meeting subject and label it “据会议名称推测”. Do not infer decisions, owners, deadlines, or completed work from a title.
- If collection is partial or unavailable, preserve other report sources and use available meeting subjects as fallback context. Do not state a meeting-coverage limitation solely because details or smart minutes failed; if no subject is available, omit unverified meeting content.
