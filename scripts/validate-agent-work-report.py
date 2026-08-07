#!/usr/bin/env python3
"""Validate the stable Markdown contract for generated Agent work reports.

Definition:
    Check that every numbered item in "工作概览" begins with one supported
    effort level and one or more source-project tags. Also check source tags on
    follow-up/risk bullets, explicit next steps, percentages, and item length.

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
    rf"^(?P<number>[1-9]\d*)\.\s+\[(?:高|中|低)投入\]\s+{SOURCE_TAGS}\S"
)
DETAIL_ITEM = re.compile(rf"^-\s+{SOURCE_TAGS}\S")
PERCENTAGE_MARKS = ("%", "％")
NEXT_STEP = re.compile(r"[；。]\s*下一步：\S")
MAX_OVERVIEW_ITEM_CHARS = 180


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description=(
            "Validate effort labels and explicit next steps in the 工作概览 section."
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
    for line_number, line in lines:
        match = OVERVIEW_ITEM.match(line)
        if not match:
            errors.append(
                f"line {line_number}: overview item must start with "
                "'N. [高投入] [项目] ', 'N. [中投入] [项目] ', or "
                "'N. [低投入] [项目] '; use consecutive tags for multiple projects"
            )
            continue
        if int(match.group("number")) != expected_number:
            errors.append(
                f"line {line_number}: overview numbering must be consecutive "
                f"from 1 (expected {expected_number})"
            )
        expected_number += 1
        if any(mark in line for mark in PERCENTAGE_MARKS):
            errors.append(
                f"line {line_number}: overview items must use effort levels, not percentages"
            )
        if not NEXT_STEP.search(line):
            errors.append(
                f"line {line_number}: overview item must end its progress with "
                "'；下一步：<plan>' or '。下一步：<plan>'"
            )
        if len(line) > MAX_OVERVIEW_ITEM_CHARS:
            errors.append(
                f"line {line_number}: overview item has {len(line)} characters; "
                f"maximum is {MAX_OVERVIEW_ITEM_CHARS}"
            )

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
