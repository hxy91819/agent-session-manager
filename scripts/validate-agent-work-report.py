#!/usr/bin/env python3
"""Validate the stable Markdown contract for generated Agent work reports.

Definition:
    Check that every numbered item in "工作概览" begins with one supported
    effort level and does not contain a percentage.

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
NEXT_HEADING_PREFIX = "## "
OVERVIEW_ITEM = re.compile(r"^(?P<number>[1-9]\d*)\.\s+\[(?:高|中|低)投入\]\s+\S")
PERCENTAGE_MARKS = ("%", "％")


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description=(
            "Validate effort-level labels in the 工作概览 section of an Agent work report."
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
    lines = markdown.splitlines()
    try:
        start = lines.index(OVERVIEW_HEADING) + 1
    except ValueError:
        return []

    overview: list[tuple[int, str]] = []
    for index in range(start, len(lines)):
        line = lines[index]
        if line.startswith(NEXT_HEADING_PREFIX):
            break
        if line.strip():
            overview.append((index + 1, line))
    return overview


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
                "'N. [高投入] ', 'N. [中投入] ', or 'N. [低投入] '"
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
