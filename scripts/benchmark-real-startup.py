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
import re
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
PROVIDERS = (
    "codex",
    "claude",
    "kimi",
    "kiro",
    "opencode",
    "codebuddy",
    "cursor",
    "openclaw",
    "zcode",
)


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
    parser.add_argument(
        "--provider-breakdown",
        action="store_true",
        help=(
            "Also measure every provider through the public provider-qualified "
            "resume boundary with the same cold/warm protocol."
        ),
    )
    parser.add_argument(
        "--resource-runs",
        type=int,
        default=0,
        help="Separate GNU time peak-RSS samples per cold/warm mode. Default: 0.",
    )
    parser.add_argument(
        "--read-runs",
        type=int,
        default=0,
        help="Separate aggregate read/pread syscall-byte samples per cold/warm mode. Default: 0.",
    )
    parser.add_argument(
        "--report-period",
        default="",
        help="Also hash report evidence and aggregate output for this period, for example last-week.",
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
    if args.resource_runs < 0:
        raise BenchmarkError("--resource-runs must be at least 0")
    if args.read_runs < 0:
        raise BenchmarkError("--read-runs must be at least 0")

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


def probe_command(binary: Path, since_days: int, provider: str | None = None) -> list[str]:
    if provider is not None:
        return [
            str(binary),
            "resume",
            "--provider",
            provider,
            "--since-days",
            str(since_days),
            PROBE_ID,
        ]
    return [
        str(binary),
        "--since-days",
        str(since_days),
        "--resume",
        PROBE_ID,
    ]


def json_command(binary: Path, since_days: int) -> list[str]:
    return [str(binary), "--since-days", str(since_days), "--json"]


def report_command(binary: Path, period: str) -> list[str]:
    return [str(binary), "report", "--period", period]


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
    provider: str | None = None,
) -> int:
    cache_dir.mkdir(parents=True, exist_ok=True)
    started = time.perf_counter_ns()
    result = subprocess.run(
        probe_command(binary, since_days, provider),
        env=isolated_env(cache_dir, diagnostic_file),
        check=False,
        stdout=subprocess.DEVNULL,
        stderr=subprocess.PIPE,
        text=True,
    )
    elapsed = time.perf_counter_ns() - started
    validate_probe_result(result.returncode, result.stderr, provider)
    if diagnostic_file is not None:
        validate_diagnostic_file(diagnostic_file)
    return elapsed


def validate_probe_result(returncode: int, stderr: str, provider: str | None = None) -> None:
    expected_error = EXPECTED_PROBE_ERROR
    if provider is not None:
        expected_error += f" for provider {provider}"
    if returncode == 0 or stderr.strip() != expected_error:
        raise BenchmarkError(
            "startup probe did not fail with the expected missing-session result; "
            "inspect the binary manually before trusting measurements"
        )


def run_rss_probe(binary: Path, since_days: int, cache_dir: Path, output: Path) -> int:
    time_binary = shutil.which("/usr/bin/time")
    if time_binary is None:
        raise BenchmarkError("GNU /usr/bin/time is required for --resource-runs")
    result = subprocess.run(
        [time_binary, "-q", "-f", "%M", "-o", str(output), *probe_command(binary, since_days)],
        env=isolated_env(cache_dir),
        check=False,
        stdout=subprocess.DEVNULL,
        stderr=subprocess.PIPE,
        text=True,
    )
    validate_probe_result(result.returncode, result.stderr)
    try:
        return int(output.read_text(encoding="utf-8").strip())
    except (OSError, ValueError) as exc:
        raise BenchmarkError(f"invalid GNU time RSS output: {output}") from exc


def parse_strace_read_bytes(text: str) -> int:
    total = 0
    for line in text.splitlines():
        match = re.search(r"=\s+(-?\d+)\s*$", line)
        if match and int(match.group(1)) > 0:
            total += int(match.group(1))
    return total


def run_read_probe(binary: Path, since_days: int, cache_dir: Path, output: Path) -> int:
    strace_binary = shutil.which("strace")
    if strace_binary is None:
        raise BenchmarkError("strace is required for --read-runs")
    result = subprocess.run(
        [
            strace_binary,
            "-f",
            "-qq",
            "-e",
            "trace=read,pread64",
            "-o",
            str(output),
            *probe_command(binary, since_days),
        ],
        env=isolated_env(cache_dir),
        check=False,
        stdout=subprocess.DEVNULL,
        stderr=subprocess.PIPE,
        text=True,
    )
    validate_probe_result(result.returncode, result.stderr)
    try:
        return parse_strace_read_bytes(output.read_text(encoding="utf-8"))
    except OSError as exc:
        raise BenchmarkError(f"cannot read strace output: {output}") from exc


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
    allowed = {"provider", "stage", "nanos", "count", "bytes"}
    for event in events:
        if not isinstance(event, dict) or not set(event).issubset(allowed):
            raise BenchmarkError(f"diagnostic file contains unsupported fields: {path}")
        if not isinstance(event.get("provider"), str) or not isinstance(event.get("stage"), str):
            raise BenchmarkError(f"diagnostic provider/stage must be strings: {path}")
        if not isinstance(event.get("nanos"), int) or event["nanos"] < 0:
            raise BenchmarkError(f"diagnostic nanos must be a non-negative integer: {path}")
        if "count" in event and (not isinstance(event["count"], int) or event["count"] < 0):
            raise BenchmarkError(f"diagnostic count must be a non-negative integer: {path}")
        if "bytes" in event and (not isinstance(event["bytes"], int) or event["bytes"] < 0):
            raise BenchmarkError(f"diagnostic bytes must be a non-negative integer: {path}")


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


def run_report(binary: Path, period: str, cache_dir: Path) -> dict[str, Any]:
    cache_dir.mkdir(parents=True, exist_ok=True)
    result = subprocess.run(
        report_command(binary, period),
        env=isolated_env(cache_dir),
        check=False,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
    )
    if result.returncode != 0:
        raise BenchmarkError("asm report failed during correctness verification")
    try:
        payload = json.loads(result.stdout)
    except json.JSONDecodeError as exc:
        raise BenchmarkError("asm report returned invalid JSON") from exc
    if not isinstance(payload, dict):
        raise BenchmarkError("asm report root must be an object")
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


def strip_report_evidence(value: Any) -> Any:
    if isinstance(value, dict):
        return {
            key: strip_report_evidence(item)
            for key, item in value.items()
            if key not in {"evidence", "previews"}
        }
    if isinstance(value, list):
        return [strip_report_evidence(item) for item in value]
    return value


def aggregate_report_correctness(payload: dict[str, Any], period: str) -> dict[str, Any]:
    sessions = payload.get("sessions")
    projects = payload.get("projects")
    totals = payload.get("totals")
    if not isinstance(sessions, list) or not isinstance(projects, list) or not isinstance(totals, dict):
        raise BenchmarkError("asm report must contain sessions, projects, and totals")
    evidence_rows: list[tuple[str, str, str, str, str]] = []
    for item in sessions:
        if not isinstance(item, dict):
            raise BenchmarkError("asm report session must be an object")
        provider = item.get("provider")
        session_id = item.get("id")
        if not isinstance(provider, str) or not isinstance(session_id, str):
            raise BenchmarkError("asm report session lacks provider or id")
        evidence = item.get("evidence") or []
        if not isinstance(evidence, list):
            raise BenchmarkError("asm report evidence must be an array")
        for row in evidence:
            if not isinstance(row, dict):
                raise BenchmarkError("asm report evidence row must be an object")
            evidence_rows.append(
                (
                    provider,
                    session_id,
                    str(row.get("at") or ""),
                    str(row.get("source") or ""),
                    str(row.get("text") or ""),
                )
            )
    aggregate = strip_report_evidence(payload)
    aggregate_encoded = json.dumps(
        aggregate, ensure_ascii=False, sort_keys=True, separators=(",", ":")
    ).encode()
    return {
        "period": period,
        "sessions": len(sessions),
        "projects": len(projects),
        "evidence": len(evidence_rows),
        "providers": totals.get("providers") or {},
        "unverified_sessions": totals.get("unverified_sessions") or 0,
        "evidence_sha256": canonical_hash(evidence_rows),
        "aggregate_without_evidence_sha256": hashlib.sha256(aggregate_encoded).hexdigest(),
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
            byte_counts = [
                run_events.get((provider, stage), {}).get("bytes", 0)
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
                    "bytes": distribution(byte_counts),
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
    if summary.get("provider_breakdown"):
        lines.extend(
            [
                "## Provider 冷热分解",
                "",
                "| Provider | cold median | cold p95 | warm median | warm p95 |",
                "|---|---:|---:|---:|---:|",
            ]
        )
        for provider, item in summary["provider_breakdown"].items():
            cold_stats = item["wall_time_seconds"]["cold"]
            warm_stats = item["wall_time_seconds"]["warm"]
            lines.append(
                f"| {provider} | {format_seconds(cold_stats['median'])} s | "
                f"{format_seconds(cold_stats['p95'])} s | "
                f"{format_seconds(warm_stats['median'])} s | "
                f"{format_seconds(warm_stats['p95'])} s |"
            )
        lines.append("")
    if summary.get("resources"):
        lines.extend(["## 独立资源采样", ""])
        resources = summary["resources"]
        if "peak_rss_kib" in resources:
            lines.append(
                f"- Peak RSS median cold/warm: "
                f"{resources['peak_rss_kib']['cold']['median']:.0f}/"
                f"{resources['peak_rss_kib']['warm']['median']:.0f} KiB"
            )
        if "syscall_read_bytes" in resources:
            lines.append(
                f"- read/pread bytes median cold/warm: "
                f"{resources['syscall_read_bytes']['cold']['median']:.0f}/"
                f"{resources['syscall_read_bytes']['warm']['median']:.0f} bytes"
            )
        lines.append("")
    if summary.get("report_correctness"):
        report = summary["report_correctness"]
        lines.extend(
            [
                "## Report 正确性哈希",
                "",
                f"- Period: `{report['period']}`",
                f"- Sessions/projects/evidence: {report['sessions']}/{report['projects']}/{report['evidence']}",
                f"- Evidence SHA-256: `{report['evidence_sha256']}`",
                f"- Aggregate SHA-256: `{report['aggregate_without_evidence_sha256']}`",
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
                    "| Provider | Stage | median | p95 | provider 占比 | count mean | bytes median |",
                    "|---|---|---:|---:|---:|---:|---:|",
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
                    f"{row['count_mean']:.2f} | {row['bytes']['median']:.0f} |"
                )
            lines.append("")
    return "\n".join(lines)


def write_json(path: Path, value: Any) -> None:
    path.write_text(
        json.dumps(value, ensure_ascii=False, indent=2, sort_keys=True) + "\n",
        encoding="utf-8",
    )


def write_benchstat(path: Path, summary: dict[str, Any], samples: dict[str, Any]) -> None:
    lines = [
        f"goos: {sys.platform}",
        f"goarch: {platform.machine()}",
        "pkg: github.com/hxy91819/agent-session-manager/real-startup",
        f"cpu: {platform.processor() or 'unknown'}",
    ]
    cpu_suffix = os.cpu_count() or 1
    for mode in ("cold", "warm"):
        cache_values = samples.get(f"{mode}_cache_bytes")
        if not isinstance(cache_values, list):
            cache_values = [samples["warm_cache_bytes"]] * len(samples[f"{mode}_ns"])
        for elapsed, cache_bytes in zip(samples[f"{mode}_ns"], cache_values, strict=True):
            lines.append(
                f"BenchmarkRealStartup/{mode}-{cpu_suffix} 1 {elapsed} ns/op "
                f"{cache_bytes} cache-bytes"
            )
    for provider, item in summary.get("provider_breakdown", {}).items():
        provider_samples = item["samples"]
        for mode in ("cold", "warm"):
            for elapsed in provider_samples[f"{mode}_ns"]:
                lines.append(
                    f"BenchmarkProviderStartup/{provider}/{mode}-{cpu_suffix} "
                    f"1 {elapsed} ns/op"
                )
    path.write_text("\n".join(lines) + "\n", encoding="utf-8")


def run_benchmark(args: argparse.Namespace, binary: Path, output_dir: Path) -> dict[str, Any]:
    samples: dict[str, Any] = {
        "cold_ns": [],
        "warm_ns": [],
        "cold_cache_bytes": [],
        "warm_cache_bytes_samples": [],
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
            samples["warm_cache_bytes_samples"].append(directory_bytes(warm_cache))
            print(f"INFO: warm {index + 1}/{args.warm_runs}: {elapsed / 1e9:.3f}s")
        samples["warm_cache_bytes"] = directory_bytes(warm_cache)

        verify_cache = temp_root / "verify"
        cold_correctness = aggregate_correctness(
            run_json(binary, args.since_days, verify_cache)
        )
        warm_correctness = aggregate_correctness(
            run_json(binary, args.since_days, verify_cache)
        )
        report_correctness = None
        if args.report_period:
            report_correctness = aggregate_report_correctness(
                run_report(binary, args.report_period, temp_root / "report"),
                args.report_period,
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
        "provider_breakdown_enabled": args.provider_breakdown,
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
    if report_correctness is not None:
        summary["report_correctness"] = report_correctness
    if args.provider_breakdown:
        provider_breakdown: dict[str, Any] = {}
        with tempfile.TemporaryDirectory(prefix="asm-real-startup-providers-") as temp:
            temp_root = Path(temp)
            for provider in PROVIDERS:
                provider_samples = {"cold_ns": [], "warm_ns": []}
                for index in range(args.cold_runs):
                    elapsed = run_probe(
                        binary,
                        args.since_days,
                        temp_root / provider / "cold" / f"{index + 1:02d}",
                        None,
                        provider,
                    )
                    provider_samples["cold_ns"].append(elapsed)
                warm_cache = temp_root / provider / "warm"
                for _ in range(args.warmup_runs):
                    run_probe(binary, args.since_days, warm_cache, None, provider)
                for _ in range(args.warm_runs):
                    provider_samples["warm_ns"].append(
                        run_probe(binary, args.since_days, warm_cache, None, provider)
                    )
                provider_breakdown[provider] = {
                    "wall_time_seconds": {
                        "cold": distribution(provider_samples["cold_ns"], 1_000_000_000),
                        "warm": distribution(provider_samples["warm_ns"], 1_000_000_000),
                    },
                    "samples": provider_samples,
                }
                cold_median = provider_breakdown[provider]["wall_time_seconds"]["cold"]["median"]
                warm_median = provider_breakdown[provider]["wall_time_seconds"]["warm"]["median"]
                print(
                    f"INFO: provider {provider}: cold median={cold_median:.3f}s; "
                    f"warm median={warm_median:.3f}s"
                )
        summary["provider_breakdown"] = provider_breakdown
    resources: dict[str, Any] = {}
    with tempfile.TemporaryDirectory(prefix="asm-real-startup-resources-") as temp:
        temp_root = Path(temp)
        if args.resource_runs:
            rss_samples: dict[str, list[int]] = {"cold": [], "warm": []}
            for index in range(args.resource_runs):
                rss_samples["cold"].append(
                    run_rss_probe(
                        binary,
                        args.since_days,
                        temp_root / "rss-cold" / f"{index + 1:02d}",
                        temp_root / f"rss-cold-{index + 1:02d}.txt",
                    )
                )
            warm_cache = temp_root / "rss-warm"
            for _ in range(args.warmup_runs):
                run_probe(binary, args.since_days, warm_cache, None)
            for index in range(args.resource_runs):
                rss_samples["warm"].append(
                    run_rss_probe(
                        binary,
                        args.since_days,
                        warm_cache,
                        temp_root / f"rss-warm-{index + 1:02d}.txt",
                    )
                )
            resources["peak_rss_kib"] = {
                mode: distribution(values) for mode, values in rss_samples.items()
            }
        if args.read_runs:
            read_samples: dict[str, list[int]] = {"cold": [], "warm": []}
            for index in range(args.read_runs):
                read_samples["cold"].append(
                    run_read_probe(
                        binary,
                        args.since_days,
                        temp_root / "read-cold" / f"{index + 1:02d}",
                        temp_root / f"read-cold-{index + 1:02d}.txt",
                    )
                )
            warm_cache = temp_root / "read-warm"
            for _ in range(args.warmup_runs):
                run_probe(binary, args.since_days, warm_cache, None)
            for index in range(args.read_runs):
                read_samples["warm"].append(
                    run_read_probe(
                        binary,
                        args.since_days,
                        warm_cache,
                        temp_root / f"read-warm-{index + 1:02d}.txt",
                    )
                )
            resources["syscall_read_bytes"] = {
                mode: distribution(values) for mode, values in read_samples.items()
            }
    if resources:
        summary["resources"] = resources

    write_json(output_dir / "samples.json", samples)
    write_json(output_dir / "summary.json", summary)
    write_benchstat(output_dir / "benchstat.txt", summary, samples)
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
