#!/usr/bin/env python3
"""Benchmark asm startup against real provider stores without retaining user data.

Definition:
  Run the public missing-session probe with isolated caches, report cold/warm
  distributions, and verify cold/warm JSON results by aggregate counts and
  irreversible hashes. Optional diagnostic mode accepts aggregate timing
  events from an instrumented asm binary.

Parameters:
  See --help. Defaults implement the repository protocol: 10 cold runs,
  2 warmups, 20 warm runs, and the latest 30-day window.

Outputs:
  Writes samples.json, summary.json, and summary.md below a new output
  directory. Raw asm JSON and cache files live only in a temporary directory
  and are removed automatically.

Examples:
  python3 scripts/benchmark-real-startup.py --asm-bin ./asm --revision HEAD
  python3 scripts/benchmark-real-startup.py --asm-bin ./asm-diag --diagnostics --output-dir /tmp/asm-diag
"""

from __future__ import annotations

import argparse
import collections
import datetime as dt
import hashlib
import json
import math
import os
import platform
import shutil
import statistics
import subprocess
import sys
import tempfile
import time
from pathlib import Path
from typing import Any, Sequence


PROBE_ID = "__asm_real_startup_probe_missing__"
EXPECTED_PROBE_ERROR = f"session not found: {PROBE_ID}"


class BenchmarkError(RuntimeError):
    """Expected invocation, probe, or result-validation error."""


def parse_args(argv: Sequence[str]) -> argparse.Namespace:
    timestamp = dt.datetime.now().astimezone().strftime("%Y%m%d-%H%M%S")
    parser = argparse.ArgumentParser(
        description=(
            "Measure real asm cold/warm startup with isolated temporary caches, "
            "then validate aggregate result parity without retaining session data."
        ),
        epilog=(
            "Examples:\n"
            "  %(prog)s --asm-bin ./asm --revision HEAD\n"
            "  %(prog)s --asm-bin ./asm --cold-runs 15 --warm-runs 30\n"
            "  %(prog)s --asm-bin ./asm-diag --diagnostics "
            "--output-dir /tmp/asm-startup-diag"
        ),
        formatter_class=argparse.RawDescriptionHelpFormatter,
    )
    parser.add_argument("--asm-bin", default="asm", help="asm executable. Default: asm.")
    parser.add_argument(
        "--output-dir",
        default=f".local/asm-startup-benchmarks/{timestamp}",
        help="New artifact directory. Default: timestamped path under .local/.",
    )
    parser.add_argument(
        "--revision",
        default="unknown",
        help="Revision label recorded in reports. Default: unknown.",
    )
    parser.add_argument("--cold-runs", type=int, default=10, help="Cold samples. Default: 10.")
    parser.add_argument("--warmup-runs", type=int, default=2, help="Warmups. Default: 2.")
    parser.add_argument("--warm-runs", type=int, default=20, help="Warm samples. Default: 20.")
    parser.add_argument(
        "--since-days",
        type=int,
        default=30,
        help="Discovery window passed to asm. Default: 30.",
    )
    parser.add_argument(
        "--diagnostics",
        action="store_true",
        help=(
            "Collect aggregate ASM_STARTUP_DIAG_FILE events. This adds timing "
            "overhead and is not the public wall-time acceptance run."
        ),
    )
    return parser.parse_args(argv)


def validate_args(args: argparse.Namespace) -> tuple[Path, Path]:
    if args.cold_runs < 1:
        raise BenchmarkError("--cold-runs must be at least 1")
    if args.warmup_runs < 0:
        raise BenchmarkError("--warmup-runs must be at least 0")
    if args.warm_runs < 1:
        raise BenchmarkError("--warm-runs must be at least 1")
    if args.since_days < 0:
        raise BenchmarkError("--since-days must be at least 0")

    binary = Path(args.asm_bin).expanduser()
    resolved = shutil.which(str(binary))
    if resolved is None:
        raise BenchmarkError(f"asm executable not found: {args.asm_bin}")
    binary = Path(resolved).resolve()

    output_dir = Path(args.output_dir).expanduser().resolve()
    if output_dir.exists() and any(output_dir.iterdir()):
        raise BenchmarkError(
            f"output directory is not empty: {output_dir}; choose a new directory"
        )
    output_dir.mkdir(parents=True, exist_ok=True)
    return binary, output_dir


def percentile_nearest_rank(values: Sequence[int], percentile: float) -> int:
    ordered = sorted(values)
    index = max(0, math.ceil(percentile * len(ordered)) - 1)
    return ordered[index]


def distribution(values: Sequence[int], scale: float = 1.0) -> dict[str, float | int]:
    if not values:
        raise BenchmarkError("cannot summarize an empty sample set")
    mean = statistics.fmean(values)
    stdev = statistics.stdev(values) if len(values) > 1 else 0.0
    return {
        "n": len(values),
        "min": min(values) / scale,
        "median": statistics.median(values) / scale,
        "mean": mean / scale,
        "p95": percentile_nearest_rank(values, 0.95) / scale,
        "max": max(values) / scale,
        "stdev": stdev / scale,
        "cv_pct": 0.0 if mean == 0 else stdev / mean * 100,
    }


def probe_command(binary: Path, since_days: int) -> list[str]:
    return [
        str(binary),
        "--since-days",
        str(since_days),
        "--resume",
        PROBE_ID,
    ]


def json_command(binary: Path, since_days: int) -> list[str]:
    return [str(binary), "--since-days", str(since_days), "--json"]


def isolated_env(cache_dir: Path, diagnostic_file: Path | None = None) -> dict[str, str]:
    env = os.environ.copy()
    env["XDG_CACHE_HOME"] = str(cache_dir)
    if diagnostic_file is not None:
        env["ASM_STARTUP_DIAG_FILE"] = str(diagnostic_file)
    else:
        env.pop("ASM_STARTUP_DIAG_FILE", None)
    return env


def run_probe(
    binary: Path,
    since_days: int,
    cache_dir: Path,
    diagnostic_file: Path | None,
) -> int:
    cache_dir.mkdir(parents=True, exist_ok=True)
    started = time.perf_counter_ns()
    result = subprocess.run(
        probe_command(binary, since_days),
        env=isolated_env(cache_dir, diagnostic_file),
        check=False,
        stdout=subprocess.DEVNULL,
        stderr=subprocess.PIPE,
        text=True,
    )
    elapsed = time.perf_counter_ns() - started
    if result.returncode == 0 or result.stderr.strip() != EXPECTED_PROBE_ERROR:
        raise BenchmarkError(
            "startup probe did not fail with the expected missing-session result; "
            "inspect the binary manually before trusting measurements"
        )
    if diagnostic_file is not None:
        validate_diagnostic_file(diagnostic_file)
    return elapsed


def directory_bytes(root: Path) -> int:
    return sum(path.stat().st_size for path in root.rglob("*") if path.is_file())


def validate_diagnostic_file(path: Path) -> None:
    if not path.is_file():
        raise BenchmarkError(
            "--diagnostics was requested but asm did not write ASM_STARTUP_DIAG_FILE"
        )
    try:
        events = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        raise BenchmarkError(f"invalid aggregate diagnostic file {path}: {exc}") from exc
    if not isinstance(events, list):
        raise BenchmarkError(f"diagnostic file must contain a JSON array: {path}")
    allowed = {"provider", "stage", "nanos", "count"}
    for event in events:
        if not isinstance(event, dict) or not set(event).issubset(allowed):
            raise BenchmarkError(f"diagnostic file contains unsupported fields: {path}")
        if not isinstance(event.get("provider"), str) or not isinstance(event.get("stage"), str):
            raise BenchmarkError(f"diagnostic provider/stage must be strings: {path}")
        if not isinstance(event.get("nanos"), int) or event["nanos"] < 0:
            raise BenchmarkError(f"diagnostic nanos must be a non-negative integer: {path}")
        if "count" in event and (not isinstance(event["count"], int) or event["count"] < 0):
            raise BenchmarkError(f"diagnostic count must be a non-negative integer: {path}")


def run_json(binary: Path, since_days: int, cache_dir: Path) -> dict[str, Any]:
    cache_dir.mkdir(parents=True, exist_ok=True)
    result = subprocess.run(
        json_command(binary, since_days),
        env=isolated_env(cache_dir),
        check=False,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
    )
    if result.returncode != 0:
        raise BenchmarkError(
            "asm --json failed during correctness verification; inspect the binary manually"
        )
    try:
        payload = json.loads(result.stdout)
    except json.JSONDecodeError as exc:
        raise BenchmarkError("asm --json returned invalid JSON") from exc
    if not isinstance(payload, dict):
        raise BenchmarkError("asm --json root must be an object")
    return payload


def canonical_hash(rows: Sequence[Sequence[Any]]) -> str:
    digest = hashlib.sha256()
    for row in sorted(tuple(item) for item in rows):
        digest.update(json.dumps(row, ensure_ascii=False, separators=(",", ":")).encode())
        digest.update(b"\n")
    return digest.hexdigest()


def aggregate_correctness(payload: dict[str, Any]) -> dict[str, Any]:
    sessions = payload.get("sessions")
    projects = payload.get("projects")
    provider_errors = payload.get("provider_errors") or []
    if not isinstance(sessions, list) or not isinstance(projects, list):
        raise BenchmarkError("asm --json must contain sessions and projects arrays")
    if not isinstance(provider_errors, list):
        raise BenchmarkError("asm --json provider_errors must be an array")

    provider_counts: collections.Counter[str] = collections.Counter()
    session_rows: list[tuple[str, str]] = []
    for item in sessions:
        if not isinstance(item, dict) or not isinstance(item.get("provider"), str) or not isinstance(item.get("id"), str):
            raise BenchmarkError("asm --json session lacks a string provider or id")
        provider_counts[item["provider"]] += 1
        session_rows.append((item["provider"], item["id"]))

    project_rows: list[tuple[str, int]] = []
    for item in projects:
        if not isinstance(item, dict) or not isinstance(item.get("cwd"), str) or not isinstance(item.get("count"), int):
            raise BenchmarkError("asm --json project lacks a string cwd or integer count")
        project_rows.append((item["cwd"], item["count"]))

    return {
        "sessions": len(sessions),
        "projects": len(projects),
        "provider_errors": len(provider_errors),
        "providers": dict(sorted(provider_counts.items())),
        "session_set_sha256": canonical_hash(session_rows),
        "project_set_sha256": canonical_hash(project_rows),
    }


def diagnostic_summary(root: Path, runs: dict[str, int]) -> dict[str, Any]:
    output: dict[str, Any] = {}
    for mode, expected_runs in runs.items():
        files = sorted((root / mode).glob("*.json"))
        if len(files) != expected_runs:
            raise BenchmarkError(
                f"diagnostic {mode} files={len(files)}, expected {expected_runs}"
            )
        events_by_run: list[dict[tuple[str, str], dict[str, Any]]] = []
        totals_by_run: list[dict[str, int]] = []
        for path in files:
            events = json.loads(path.read_text(encoding="utf-8"))
            run_events = {
                (event["provider"], event["stage"]): event for event in events
            }
            events_by_run.append(run_events)
            run_totals = {
                event["provider"]: event["nanos"]
                for event in events
                if event["stage"] == "provider_total"
            }
            totals_by_run.append(run_totals)
        keys = sorted({key for run_events in events_by_run for key in run_events})
        rows: list[dict[str, Any]] = []
        for provider, stage in keys:
            values = [
                run_events.get((provider, stage), {}).get("nanos", 0)
                for run_events in events_by_run
            ]
            counts = [
                run_events.get((provider, stage), {}).get("count", 0)
                for run_events in events_by_run
            ]
            shares = []
            if stage != "provider_total":
                for index, value in enumerate(values):
                    total = totals_by_run[index].get(provider, 0)
                    shares.append(0.0 if total == 0 else value / total * 100)
            rows.append(
                {
                    "provider": provider,
                    "stage": stage,
                    "duration_ms": distribution(values, 1_000_000),
                    "count_mean": statistics.fmean(counts),
                    "count_median": statistics.median(counts),
                    "share_of_provider_total_pct_median": (
                        100.0 if stage == "provider_total" else statistics.median(shares)
                    ),
                }
            )
        output[mode] = rows
    return output


def format_seconds(value: float | int) -> str:
    return f"{float(value):.6f}"


def markdown_report(summary: dict[str, Any]) -> str:
    cold = summary["wall_time_seconds"]["cold"]
    warm = summary["wall_time_seconds"]["warm"]
    correctness = summary["correctness"]["cold"]
    lines = [
        "# asm 真实启动性能报告",
        "",
        f"- Revision: `{summary['revision']}`",
        f"- 采集时间: {summary['captured_at']}",
        f"- 默认窗口: {summary['since_days']} 天",
        f"- Diagnostic mode: {str(summary['diagnostics']).lower()}",
        "",
        "## Wall time",
        "",
        "| 场景 | n | min | median | mean | p95 | max | stdev | CV |",
        "|---|---:|---:|---:|---:|---:|---:|---:|---:|",
    ]
    for label, stats in (("冷启动", cold), ("热启动", warm)):
        lines.append(
            f"| {label} | {stats['n']} | {format_seconds(stats['min'])} s | "
            f"{format_seconds(stats['median'])} s | {format_seconds(stats['mean'])} s | "
            f"{format_seconds(stats['p95'])} s | {format_seconds(stats['max'])} s | "
            f"{format_seconds(stats['stdev'])} s | {stats['cv_pct']:.3f}% |"
        )
    lines.extend(
        [
            "",
            "## 正确性聚合",
            "",
            f"- Session: {correctness['sessions']}",
            f"- Project: {correctness['projects']}",
            f"- Provider error: {correctness['provider_errors']}",
            f"- Provider counts: `{json.dumps(correctness['providers'], sort_keys=True)}`",
            f"- Session set SHA-256: `{correctness['session_set_sha256']}`",
            f"- Project set SHA-256: `{correctness['project_set_sha256']}`",
            f"- 冷热聚合与哈希一致: {str(summary['correctness']['matches']).lower()}",
            "",
            "原始 session JSON 与隔离 cache 未保留；`samples.json` 仅包含计时和 cache bytes。",
            "",
        ]
    )
    if summary["diagnostics"]:
        lines.extend(["## Provider 诊断阶段", ""])
        for mode in ("cold", "warm"):
            lines.extend(
                [
                    f"### {mode}",
                    "",
                    "| Provider | Stage | median | p95 | provider 占比 | count mean |",
                    "|---|---|---:|---:|---:|---:|",
                ]
            )
            for row in summary["diagnostic_stages"][mode]:
                duration = row["duration_ms"]
                if duration["median"] == 0 and row["count_mean"] == 0:
                    continue
                lines.append(
                    f"| {row['provider']} | {row['stage']} | "
                    f"{duration['median']:.3f} ms | {duration['p95']:.3f} ms | "
                    f"{row['share_of_provider_total_pct_median']:.2f}% | "
                    f"{row['count_mean']:.2f} |"
                )
            lines.append("")
    return "\n".join(lines)


def write_json(path: Path, value: Any) -> None:
    path.write_text(
        json.dumps(value, ensure_ascii=False, indent=2, sort_keys=True) + "\n",
        encoding="utf-8",
    )


def run_benchmark(args: argparse.Namespace, binary: Path, output_dir: Path) -> dict[str, Any]:
    samples: dict[str, Any] = {
        "cold_ns": [],
        "warm_ns": [],
        "cold_cache_bytes": [],
        "warm_cache_bytes": 0,
    }
    diagnostics_root = output_dir / "diagnostics"
    if args.diagnostics:
        (diagnostics_root / "cold").mkdir(parents=True)
        (diagnostics_root / "warm").mkdir(parents=True)

    with tempfile.TemporaryDirectory(prefix="asm-real-startup-") as temp:
        temp_root = Path(temp)
        cold_root = temp_root / "cold"
        for index in range(args.cold_runs):
            cache_dir = cold_root / f"{index + 1:02d}"
            diag_file = (
                diagnostics_root / "cold" / f"{index + 1:02d}.json"
                if args.diagnostics
                else None
            )
            elapsed = run_probe(binary, args.since_days, cache_dir, diag_file)
            samples["cold_ns"].append(elapsed)
            samples["cold_cache_bytes"].append(directory_bytes(cache_dir))
            print(f"INFO: cold {index + 1}/{args.cold_runs}: {elapsed / 1e9:.3f}s")

        warm_cache = temp_root / "warm"
        for index in range(args.warmup_runs):
            run_probe(binary, args.since_days, warm_cache, None)
            print(f"INFO: warmup {index + 1}/{args.warmup_runs} complete")
        for index in range(args.warm_runs):
            diag_file = (
                diagnostics_root / "warm" / f"{index + 1:02d}.json"
                if args.diagnostics
                else None
            )
            elapsed = run_probe(binary, args.since_days, warm_cache, diag_file)
            samples["warm_ns"].append(elapsed)
            print(f"INFO: warm {index + 1}/{args.warm_runs}: {elapsed / 1e9:.3f}s")
        samples["warm_cache_bytes"] = directory_bytes(warm_cache)

        verify_cache = temp_root / "verify"
        cold_correctness = aggregate_correctness(
            run_json(binary, args.since_days, verify_cache)
        )
        warm_correctness = aggregate_correctness(
            run_json(binary, args.since_days, verify_cache)
        )

    matches = cold_correctness == warm_correctness
    summary: dict[str, Any] = {
        "revision": args.revision,
        "captured_at": dt.datetime.now().astimezone().isoformat(),
        "binary": binary.name,
        "platform": platform.platform(),
        "cpu_count": os.cpu_count(),
        "since_days": args.since_days,
        "diagnostics": args.diagnostics,
        "protocol": {
            "cold_runs": args.cold_runs,
            "warmup_runs": args.warmup_runs,
            "warm_runs": args.warm_runs,
            "probe": "public missing-session resume",
            "cache": "isolated temporary XDG_CACHE_HOME",
        },
        "wall_time_seconds": {
            "cold": distribution(samples["cold_ns"], 1_000_000_000),
            "warm": distribution(samples["warm_ns"], 1_000_000_000),
        },
        "cache_bytes": {
            "cold": distribution(samples["cold_cache_bytes"]),
            "warm": samples["warm_cache_bytes"],
        },
        "correctness": {
            "cold": cold_correctness,
            "warm": warm_correctness,
            "matches": matches,
        },
    }
    if args.diagnostics:
        summary["diagnostic_stages"] = diagnostic_summary(
            diagnostics_root,
            {"cold": args.cold_runs, "warm": args.warm_runs},
        )

    write_json(output_dir / "samples.json", samples)
    write_json(output_dir / "summary.json", summary)
    (output_dir / "summary.md").write_text(markdown_report(summary), encoding="utf-8")
    if not matches:
        raise BenchmarkError(
            "cold/warm session, project, provider aggregates, or hashes differ; "
            f"inspect aggregate report: {output_dir / 'summary.json'}"
        )
    if cold_correctness["provider_errors"] != 0:
        raise BenchmarkError(
            "provider errors were present; inspect aggregate report before trusting timings"
        )
    return summary


def main(argv: Sequence[str] | None = None) -> int:
    args = parse_args(sys.argv[1:] if argv is None else argv)
    try:
        binary, output_dir = validate_args(args)
        print(f"INFO: asm binary: {binary}")
        print(f"INFO: output directory: {output_dir}")
        summary = run_benchmark(args, binary, output_dir)
    except BenchmarkError as exc:
        print(f"ERROR: {exc}", file=sys.stderr)
        return 1

    cold = summary["wall_time_seconds"]["cold"]
    warm = summary["wall_time_seconds"]["warm"]
    print(
        "INFO: result: "
        f"cold median={cold['median']:.3f}s p95={cold['p95']:.3f}s; "
        f"warm median={warm['median']:.3f}s p95={warm['p95']:.3f}s"
    )
    print(f"INFO: report: {output_dir / 'summary.md'}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
