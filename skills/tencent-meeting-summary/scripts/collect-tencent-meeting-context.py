#!/usr/bin/env python3
"""Collect ended Tencent Meetings and available smart minutes for a report window.

Inputs are a required half-open ISO time window and output path. The script
writes one JSON object and returns zero for source-level failures so scheduled
reports can fall back to asm; invalid command-line arguments return non-zero.
"""

from __future__ import annotations

import argparse
import json
import os
import subprocess
import sys
from pathlib import Path
from typing import Any


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description=(
            "Collect read-only Tencent Meeting context for a half-open report window. "
            "API failures are recorded in the output so the caller can fall back to asm."
        ),
        epilog=(
            "Examples:\n"
            "  collect-tencent-meeting-context.py --start 2026-07-21T00:00:00+08:00 "
            "--end 2026-07-22T00:00:00+08:00 --output meeting.json\n"
            "  collect-tencent-meeting-context.py --start 2026-07-14T00:00:00+08:00 "
            "--end 2026-07-21T00:00:00+08:00 --output week.json"
        ),
        formatter_class=argparse.RawDescriptionHelpFormatter,
    )
    parser.add_argument("--start", required=True, help="Inclusive ISO 8601 start time")
    parser.add_argument("--end", required=True, help="Exclusive ISO 8601 end time")
    parser.add_argument("--output", required=True, help="Destination JSON file")
    parser.add_argument(
        "--meeting-skill-dir",
        default=str(Path(__file__).resolve().parents[2] / "tencent-meeting-mcp"),
        help="Path to the bundled tencent-meeting-mcp skill",
    )
    parser.add_argument("--timezone", default="Asia/Shanghai", help="IANA display timezone")
    return parser.parse_args()


def call_tool(script: Path, name: str, arguments: dict[str, Any]) -> dict[str, Any]:
    payload = {
        "name": name,
        "arguments": {
            **arguments,
            "_client_info": {"os": "Linux", "agent": "asm-agent-work-report", "model": "n/a"},
        },
    }
    completed = subprocess.run(
        [sys.executable, str(script), "tools/call", json.dumps(payload, ensure_ascii=False)],
        check=False,
        capture_output=True,
        text=True,
        env={**os.environ, "PYTHONDONTWRITEBYTECODE": "1"},
    )
    if completed.returncode != 0:
        detail = completed.stderr.strip() or completed.stdout.strip() or f"exit {completed.returncode}"
        raise RuntimeError(detail)
    try:
        response = json.loads(completed.stdout)
        body = response.get("body", "{}")
        parsed_body = json.loads(body) if isinstance(body, str) else body
    except (json.JSONDecodeError, TypeError) as exc:
        raise RuntimeError("Tencent Meeting returned invalid JSON") from exc
    if response.get("status_code") != 200:
        raise RuntimeError(f"Tencent Meeting returned status {response.get('status_code')}: {parsed_body}")
    return {
        "body": parsed_body,
        "trace": response.get("headers", {}).get("X-Tc-Trace", ""),
        "rpc_uuid": response.get("headers", {}).get("rpcUuid", ""),
    }


def collect_pages(
    script: Path,
    tool_name: str,
    list_key: str,
    base_arguments: dict[str, Any],
) -> tuple[list[dict[str, Any]], list[dict[str, str]]]:
    items: list[dict[str, Any]] = []
    traces: list[dict[str, str]] = []
    page_token = ""
    while True:
        arguments = {**base_arguments, "page_size": 30}
        if page_token:
            arguments["page_token"] = page_token
        result = call_tool(script, tool_name, arguments)
        body = result["body"]
        items.extend(body.get(list_key, []))
        traces.append({"tool": tool_name, "trace": result["trace"], "rpc_uuid": result["rpc_uuid"]})
        if not body.get("has_more"):
            break
        page_token = str(body.get("next_page_token", ""))
        if not page_token:
            raise RuntimeError(f"{tool_name} returned has_more without next_page_token")
    return items, traces


def preferred_record_files(record_meetings: list[dict[str, Any]]) -> list[dict[str, str]]:
    by_meeting: dict[str, list[dict[str, Any]]] = {}
    for record in record_meetings:
        key = str(record.get("meeting_id") or record.get("subject") or len(by_meeting))
        by_meeting.setdefault(key, []).append(record)

    selected: list[dict[str, str]] = []
    for records in by_meeting.values():
        # Cloud recordings are the canonical source when both a recording and a
        # transcript artifact exist for the same meeting; this avoids duplicate minutes.
        record = next((item for item in records if item.get("record_type") == "云录制"), records[0])
        files = record.get("record_files") or []
        if not files:
            continue
        selected.append(
            {
                "subject": str(record.get("subject", "")),
                "meeting_id": str(record.get("meeting_id", "")),
                "record_file_id": str(files[0].get("record_file_id", "")),
            }
        )
    return [item for item in selected if item["record_file_id"]]


def main() -> int:
    args = parse_args()
    output = Path(args.output)
    output.parent.mkdir(parents=True, exist_ok=True)
    result: dict[str, Any] = {
        "status": "unavailable",
        "start": args.start,
        "end": args.end,
        "timezone": args.timezone,
        "meetings": [],
        "smart_minutes": [],
        "errors": [],
        "traces": [],
    }

    skill_script = Path(args.meeting_skill_dir) / "scripts" / "tencent_meeting.py"
    if not os.environ.get("TENCENT_MEETING_TOKEN"):
        result["errors"].append("TENCENT_MEETING_TOKEN is not set")
        output.write_text(json.dumps(result, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
        return 0
    if not skill_script.is_file():
        result["errors"].append(f"Tencent Meeting skill entrypoint not found: {skill_script}")
        output.write_text(json.dumps(result, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
        return 0

    common = {
        "start_time": args.start,
        "end_time": args.end,
        "is_compact": True,
        "timezone": args.timezone,
    }
    try:
        meetings, traces = collect_pages(
            skill_script, "get_user_ended_meetings", "meeting_info_list", common
        )
        result["meetings"] = [
            {
                "subject": item.get("subject", ""),
                "start_time": item.get("start_time", ""),
                "end_time": item.get("end_time", ""),
                "meeting_type": item.get("meeting_type", ""),
            }
            for item in meetings
        ]
        result["traces"].extend(traces)
        result["status"] = "ok"
    except RuntimeError as exc:
        result["errors"].append(f"ended meetings: {exc}")

    try:
        recordings, traces = collect_pages(
            skill_script, "get_records_list", "record_meetings", common
        )
        result["traces"].extend(traces)
        for record in preferred_record_files(recordings):
            try:
                minutes_result = call_tool(
                    skill_script,
                    "get_smart_minutes",
                    {
                        "record_file_id": record["record_file_id"],
                        "lang": "zh",
                        "is_compact": True,
                        "timezone": args.timezone,
                    },
                )
                minute = minutes_result["body"].get("meeting_minute", {})
                result["smart_minutes"].append(
                    {
                        "subject": record["subject"],
                        "minute": minute.get("minute", ""),
                        "todo": minute.get("todo", ""),
                    }
                )
                result["traces"].append(
                    {
                        "tool": "get_smart_minutes",
                        "trace": minutes_result["trace"],
                        "rpc_uuid": minutes_result["rpc_uuid"],
                    }
                )
            except RuntimeError as exc:
                result["errors"].append(f"smart minutes for {record['subject']}: {exc}")
    except RuntimeError as exc:
        result["errors"].append(f"recordings: {exc}")

    if result["errors"] and result["status"] == "ok":
        result["status"] = "partial"
    output.write_text(json.dumps(result, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
