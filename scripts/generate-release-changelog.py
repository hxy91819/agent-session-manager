#!/usr/bin/env python3
"""Generate and validate release changelog sections with contributor credit.

Definition:
  Build release notes from first-parent git history, resolve merged PR titles
  and original authors through GitHub, and preserve a cumulative CHANGELOG.md.

Parameters:
  See --help. --version and --output are required; --target defaults to HEAD.

Outputs:
  notes mode writes one Markdown release section, prepend mode atomically adds
  that section below "# Changelog", and check mode exits non-zero on drift.

Examples:
  python3 scripts/generate-release-changelog.py --version v0.8.0 --output /tmp/v0.8.0.md
  python3 scripts/generate-release-changelog.py --version v0.8.0 --mode prepend --output CHANGELOG.md
"""

from __future__ import annotations

import argparse
import json
import os
import re
import subprocess
import sys
import tempfile
from dataclasses import dataclass
from pathlib import Path
from typing import Any, Sequence


SEMVER_RE = re.compile(r"^v\d+\.\d+\.\d+(?:[-+][0-9A-Za-z.-]+)?$")
MERGE_PR_RE = re.compile(r"^Merge pull request #(\d+)\b")
SQUASH_PR_RE = re.compile(r"\(#(\d+)\)$")


class ChangelogError(RuntimeError):
    """Expected input, repository, or release-data error."""


@dataclass(frozen=True)
class Change:
    title: str
    reference: str
    credit: str
    contributor: str


def run(command: Sequence[str], *, cwd: Path | None = None) -> str:
    result = subprocess.run(
        command,
        cwd=cwd,
        check=False,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
    )
    if result.returncode != 0:
        detail = result.stderr.strip() or result.stdout.strip()
        raise ChangelogError(f"command failed ({' '.join(command)}): {detail}")
    return result.stdout.strip()


def git(*args: str) -> str:
    return run(("git", *args))


def parse_args(argv: Sequence[str]) -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description=(
            "Generate GitHub release notes with original-author thanks, prepend "
            "them to CHANGELOG.md, or verify a committed section."
        ),
        epilog=(
            "Examples:\n"
            "  %(prog)s --version v0.8.0 --output /tmp/v0.8.0.md\n"
            "  %(prog)s --version v0.8.0 --mode prepend --output CHANGELOG.md\n"
            "  %(prog)s --version v0.8.0 --target v0.8.0 --mode check "
            "--output CHANGELOG.md"
        ),
        formatter_class=argparse.RawDescriptionHelpFormatter,
    )
    parser.add_argument("--version", required=True, help="Release version, for example v0.8.0.")
    parser.add_argument(
        "--target",
        default="HEAD",
        help="Git ref containing the release changes. Defaults to HEAD.",
    )
    parser.add_argument("--output", required=True, help="Markdown output or changelog path.")
    parser.add_argument(
        "--mode",
        choices=("notes", "prepend", "check"),
        default="notes",
        help="Write notes, prepend to a cumulative changelog, or verify it.",
    )
    parser.add_argument(
        "--repo",
        help="GitHub owner/repository. Defaults to the origin remote.",
    )
    parser.add_argument(
        "--pr-data",
        type=Path,
        help="Optional JSON array of PR metadata for deterministic/offline tests.",
    )
    return parser.parse_args(argv)


def ensure_git_context(version: str, target: str) -> None:
    if not SEMVER_RE.fullmatch(version):
        raise ChangelogError(f"version must be a semantic tag such as v0.8.0: {version}")
    if git("rev-parse", "--is-inside-work-tree") != "true":
        raise ChangelogError("must be run inside a git work tree")
    git("rev-parse", "--verify", f"{target}^{{commit}}")


def repository_from_origin() -> str:
    origin = git("config", "--get", "remote.origin.url")
    match = re.search(r"github\.com[/:]([^/]+)/([^/]+?)(?:\.git)?$", origin)
    if not match:
        raise ChangelogError("cannot infer GitHub repository from origin; pass --repo owner/name")
    return f"{match.group(1)}/{match.group(2)}"


def previous_tag(version: str, target: str) -> str | None:
    tags = git(
        "tag",
        "--sort=-v:refname",
        "--merged",
        target,
        "v[0-9]*.[0-9]*.[0-9]*",
    ).splitlines()
    return next((tag for tag in tags if tag and tag != version), None)


def pull_request_number(subject: str) -> int | None:
    for pattern in (MERGE_PR_RE, SQUASH_PR_RE):
        match = pattern.search(subject)
        if match:
            return int(match.group(1))
    return None


def is_changelog_only_commit(commit: str) -> bool:
    paths = git(
        "diff-tree",
        "--first-parent",
        "-m",
        "--no-commit-id",
        "--name-only",
        "-r",
        commit,
    ).splitlines()
    return bool(paths) and set(paths) == {"CHANGELOG.md"}


def load_fixture(path: Path | None) -> dict[int, dict[str, Any]] | None:
    if path is None:
        return None
    try:
        payload = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        raise ChangelogError(f"cannot read --pr-data {path}: {exc}") from exc
    if not isinstance(payload, list):
        raise ChangelogError("--pr-data must contain a JSON array")
    records: dict[int, dict[str, Any]] = {}
    for item in payload:
        if not isinstance(item, dict) or not isinstance(item.get("number"), int):
            raise ChangelogError("each --pr-data item must contain an integer number")
        records[item["number"]] = item
    return records


def fetch_pull_request(
    number: int,
    repository: str,
    fixture: dict[int, dict[str, Any]] | None,
) -> dict[str, Any]:
    if fixture is not None:
        if number not in fixture:
            raise ChangelogError(f"--pr-data is missing PR #{number}")
        return fixture[number]
    raw = run(("gh", "api", f"repos/{repository}/pulls/{number}"))
    payload = json.loads(raw)
    user = payload.get("user") or {}
    return {
        "number": number,
        "title": payload.get("title"),
        "author": user.get("login"),
        "is_bot": user.get("type") == "Bot",
        "url": payload.get("html_url"),
    }


def clean_text(value: Any, field: str) -> str:
    if not isinstance(value, str) or not value.strip():
        raise ChangelogError(f"GitHub metadata is missing {field}")
    return " ".join(value.split())


def collect_changes(
    target: str,
    base: str | None,
    repository: str,
    fixture: dict[int, dict[str, Any]] | None,
) -> list[Change]:
    revision_range = f"{base}..{target}" if base else target
    log = git(
        "log",
        "--first-parent",
        "--format=%H%x09%h%x09%an%x09%s",
        revision_range,
    )
    changes: list[Change] = []
    seen_prs: set[int] = set()
    for line in log.splitlines():
        full_sha, short_sha, author_name, subject = line.split("\t", 3)
        if is_changelog_only_commit(full_sha):
            continue
        number = pull_request_number(subject)
        if number is None:
            author_name = clean_text(author_name, f"author for commit {short_sha}")
            is_bot = author_name.lower().endswith("[bot]")
            credit = "" if is_bot else f" Thanks {author_name}."
            changes.append(
                Change(
                    title=clean_text(subject, f"subject for commit {short_sha}"),
                    reference=f"`{short_sha}`",
                    credit=credit,
                    contributor="" if is_bot else author_name,
                )
            )
            continue
        if number in seen_prs:
            continue
        seen_prs.add(number)
        pr = fetch_pull_request(number, repository, fixture)
        title = clean_text(pr.get("title"), f"title for PR #{number}")
        author = clean_text(pr.get("author"), f"author for PR #{number}")
        url = clean_text(
            pr.get("url") or f"https://github.com/{repository}/pull/{number}",
            f"url for PR #{number}",
        )
        is_bot = bool(pr.get("is_bot")) or author.lower().endswith("[bot]")
        credit = "" if is_bot else f" Thanks @{author}."
        changes.append(
            Change(
                title=title,
                reference=f"[#{number}]({url})",
                credit=credit,
                contributor="" if is_bot else f"@{author}",
            )
        )
    return changes


def join_contributors(values: Sequence[str]) -> str:
    unique = list(dict.fromkeys(value for value in values if value))
    if not unique:
        return ""
    if len(unique) == 1:
        return unique[0]
    if len(unique) == 2:
        return f"{unique[0]} and {unique[1]}"
    return f"{', '.join(unique[:-1])}, and {unique[-1]}"


def render_section(
    version: str,
    base: str | None,
    repository: str,
    changes: Sequence[Change],
) -> str:
    lines = [f"## {version}", ""]
    if base:
        compare_url = f"https://github.com/{repository}/compare/{base}...{version}"
        lines.extend([f"Changes since [{base}]({compare_url}).", ""])
    else:
        lines.extend(["Initial release.", ""])
    lines.extend(["### Changes", ""])
    if changes:
        for change in changes:
            lines.append(f"- {change.title} ({change.reference}).{change.credit}")
    else:
        lines.append("- No user-visible changes.")
    contributors = join_contributors([change.contributor for change in changes])
    if contributors:
        lines.extend(["", "### Contributors", "", f"Thanks {contributors} for this release."])
    return "\n".join(lines).rstrip() + "\n"


def atomic_write(path: Path, content: str) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    # A release command must never leave a half-written changelog if interrupted.
    with tempfile.NamedTemporaryFile(
        mode="w",
        encoding="utf-8",
        dir=path.parent,
        prefix=f".{path.name}.",
        delete=False,
    ) as handle:
        handle.write(content)
        temporary = Path(handle.name)
    os.replace(temporary, path)


def prepend_section(path: Path, version: str, section: str) -> None:
    if path.exists():
        content = path.read_text(encoding="utf-8")
        if not content.startswith("# Changelog\n"):
            raise ChangelogError(f"{path} must start with '# Changelog'")
        existing = re.search(
            rf"(?ms)^## {re.escape(version)}[^\n]*\n.*?(?=^## |\Z)",
            content,
        )
        if existing:
            before = content[: existing.start()].rstrip()
            after = content[existing.end() :].lstrip("\n")
            next_content = f"{before}\n\n{section.rstrip()}\n"
            if after:
                next_content += f"\n{after.rstrip()}\n"
            atomic_write(path, next_content)
            return
        remainder = content[len("# Changelog\n") :].lstrip("\n")
    else:
        remainder = ""
    next_content = f"# Changelog\n\n{section.rstrip()}\n"
    if remainder:
        next_content += f"\n{remainder.rstrip()}\n"
    atomic_write(path, next_content)


def extract_section(content: str, version: str) -> str | None:
    match = re.search(
        rf"(?ms)^## {re.escape(version)}[^\n]*\n.*?(?=^## |\Z)",
        content,
    )
    return match.group(0).rstrip() + "\n" if match else None


def apply_mode(mode: str, output: Path, version: str, section: str) -> None:
    if mode == "notes":
        atomic_write(output, section)
        return
    if mode == "prepend":
        prepend_section(output, version, section)
        return
    if not output.exists():
        raise ChangelogError(f"changelog does not exist: {output}")
    actual = extract_section(output.read_text(encoding="utf-8"), version)
    if actual is None:
        raise ChangelogError(f"{output} does not contain {version}")
    if actual != section:
        raise ChangelogError(
            f"{output} section {version} differs from generated release history; "
            "rerun with --mode prepend before tagging"
        )


def main(argv: Sequence[str] | None = None) -> int:
    args = parse_args(argv or sys.argv[1:])
    try:
        ensure_git_context(args.version, args.target)
        repository = args.repo or repository_from_origin()
        base = previous_tag(args.version, args.target)
        fixture = load_fixture(args.pr_data)
        changes = collect_changes(args.target, base, repository, fixture)
        section = render_section(args.version, base, repository, changes)
        apply_mode(args.mode, Path(args.output), args.version, section)
    except (ChangelogError, json.JSONDecodeError) as exc:
        print(f"ERROR: {exc}", file=sys.stderr)
        return 1
    print(
        f"INFO: mode={args.mode} version={args.version} "
        f"previous_tag={base or 'none'} changes={len(changes)} output={args.output}"
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
