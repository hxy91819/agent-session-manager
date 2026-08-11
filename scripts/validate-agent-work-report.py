#!/usr/bin/env python3
"""Validate the stable Markdown contract for generated Agent work reports.

Definition:
    Check that every numbered item in "工作概览" begins with one supported
    effort level and one or more source-project tags. Accept optional nested
    task lists and check source tags, explicit flat-item next steps,
    percentages, and item length.

Parameters:
    report is the required path to a UTF-8 Markdown report.

Outputs:
    Prints a success summary to stdout. Validation and read errors go to stderr.
    Exit 0 means valid, 1 means invalid report content, and 2 means invocation
    or file access failed.

Examples:
    python3 scripts/validate-agent-work-report.py report.md
    python3 scripts/validate-agent-work-report.py .local/daily-agent-report-runs/latest.md
"""

from __future__ import annotations

import argparse
import re
import sys
from pathlib import Path


OVERVIEW_HEADING = "## 工作概览"
FOLLOW_UP_HEADING = "## 后续跟进"
RISK_HEADING = "## 风险与阻塞"
NEXT_HEADING_PREFIX = "## "
SOURCE_TAGS = r"(?P<tags>(?:\[[^\[\]\r\n]+\]\s+)+)"
OVERVIEW_ITEM = re.compile(
    rf"^(?P<number>[1-9]\d*)\.\s+\[(?:高|中|低)投入\]\s+"
    rf"{SOURCE_TAGS}(?P<body>\S.*)$"
)
CHILD_ITEM = re.compile(r"^ {4}-\s+(?P<body>\S.*)$")
EFFORT_LABEL = re.compile(r"\[(?:高|中|低)投入\]")
DETAIL_ITEM = re.compile(rf"^-\s+{SOURCE_TAGS}\S")
PERCENTAGE_MARKS = ("%", "％")
NEXT_STEP = re.compile(r"[；。]\s*下一步：\S")
MAX_OVERVIEW_ITEM_CHARS = 180


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description=(
            "Validate effort labels, source tags, and optional task hierarchy "
            "in the 工作概览 section."
        ),
        epilog=(
            "Examples:\n"
            "  python3 scripts/validate-agent-work-report.py report.md\n"
            "  python3 scripts/validate-agent-work-report.py "
            ".local/daily-agent-report-runs/latest.md"
        ),
        formatter_class=argparse.RawDescriptionHelpFormatter,
    )
    parser.add_argument("report", help="UTF-8 Markdown report to validate")
    return parser.parse_args()


def overview_lines(markdown: str) -> list[tuple[int, str]]:
    return section_lines(markdown, OVERVIEW_HEADING)


def section_lines(markdown: str, heading: str) -> list[tuple[int, str]]:
    lines = markdown.splitlines()
    try:
        start = lines.index(heading) + 1
    except ValueError:
        return []

    section: list[tuple[int, str]] = []
    for index in range(start, len(lines)):
        line = lines[index]
        if line.startswith(NEXT_HEADING_PREFIX):
            break
        if line.strip():
            section.append((index + 1, line))
    return section


def validate(markdown: str) -> list[str]:
    if OVERVIEW_HEADING not in markdown.splitlines():
        return [f"missing required heading: {OVERVIEW_HEADING}"]

    lines = overview_lines(markdown)
    if not lines:
        return ["工作概览 must contain at least one numbered item"]

    errors: list[str] = []
    expected_number = 1
    current_parent: dict[str, int | str] | None = None

    def check_length(line_number: int, line: str) -> None:
        if len(line) > MAX_OVERVIEW_ITEM_CHARS:
            errors.append(
                f"line {line_number}: overview item has {len(line)} characters; "
                f"maximum is {MAX_OVERVIEW_ITEM_CHARS}"
            )

    def finish_parent() -> None:
        if current_parent is None:
            return
        child_count = int(current_parent["child_count"])
        line_number = int(current_parent["line_number"])
        if child_count == 1:
            errors.append(
                f"line {line_number}: nested overview items must contain at least "
                "two task bullets; use the flat format for one task"
            )
        if child_count > 0 and "：" in str(current_parent["body"]):
            errors.append(
                f"line {line_number}: nested overview parent must contain only "
                "source tags and the project or matter name"
            )
        if child_count == 0 and not NEXT_STEP.search(str(current_parent["line"])):
            errors.append(
                f"line {line_number}: flat overview item must end its progress with "
                "'；下一步：<plan>' or '。下一步：<plan>'"
            )

    for line_number, line in lines:
        if any(mark in line for mark in PERCENTAGE_MARKS):
            errors.append(
                f"line {line_number}: overview items must use effort levels, not percentages"
            )

        match = OVERVIEW_ITEM.match(line)
        if match:
            finish_parent()
            if int(match.group("number")) != expected_number:
                errors.append(
                    f"line {line_number}: overview numbering must be consecutive "
                    f"from 1 (expected {expected_number})"
                )
            expected_number += 1
            check_length(line_number, line)
            current_parent = {
                "line_number": line_number,
                "line": line,
                "body": match.group("body"),
                "child_count": 0,
            }
            continue

        child_match = CHILD_ITEM.match(line)
        if child_match:
            if current_parent is None:
                errors.append(
                    f"line {line_number}: nested task bullet must follow a numbered overview item"
                )
                continue
            current_parent["child_count"] = int(current_parent["child_count"]) + 1
            child_body = child_match.group("body")
            if "：" not in child_body:
                errors.append(
                    f"line {line_number}: nested task bullet must use '任务：进展' format"
                )
            if EFFORT_LABEL.search(child_body):
                errors.append(
                    f"line {line_number}: effort level belongs on the numbered parent, "
                    "not a nested task"
                )
            check_length(line_number, line)
            continue

        if line.lstrip().startswith("- "):
            errors.append(
                f"line {line_number}: nested task bullet must start with exactly four spaces"
            )
        else:
            errors.append(
                f"line {line_number}: overview item must start with "
                "'N. [高投入] [项目] ', 'N. [中投入] [项目] ', "
                "'N. [低投入] [项目] ', or a four-space-indented task bullet"
            )

    finish_parent()
    if expected_number == 1:
        errors.append("工作概览 must contain at least one numbered item")

    for heading in (FOLLOW_UP_HEADING, RISK_HEADING):
        for line_number, line in section_lines(markdown, heading):
            if line.startswith("- ") and not DETAIL_ITEM.match(line):
                errors.append(
                    f"line {line_number}: list item under {heading.removeprefix('## ')} "
                    "must start with one or more source tags, for example '- [项目] '"
                )
    return errors


def main() -> int:
    args = parse_args()
    report = Path(args.report)
    try:
        markdown = report.read_text(encoding="utf-8")
    except (OSError, UnicodeError) as exc:
        print(f"ERROR: unable to read report {report}: {exc}", file=sys.stderr)
        return 2

    errors = validate(markdown)
    if errors:
        for error in errors:
            print(f"ERROR: {error}", file=sys.stderr)
        return 1

    print(f"Valid Agent work report: {report}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
