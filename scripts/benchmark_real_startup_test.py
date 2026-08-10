from __future__ import annotations

import importlib.util
import json
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path


SCRIPT = Path(__file__).with_name("benchmark-real-startup.py").resolve()


def load_module():
    spec = importlib.util.spec_from_file_location("benchmark_real_startup", SCRIPT)
    if spec is None or spec.loader is None:
        raise RuntimeError("unable to load benchmark script")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


class BenchmarkRealStartupTest(unittest.TestCase):
    def setUp(self) -> None:
        self.temp = tempfile.TemporaryDirectory()
        self.root = Path(self.temp.name)
        self.fake_asm = self.root / "fake-asm.py"
        self.fake_asm.write_text(
            """#!/usr/bin/env python3
import json
import os
import pathlib
import sys

cache = pathlib.Path(os.environ["XDG_CACHE_HOME"])
cache.mkdir(parents=True, exist_ok=True)
(cache / "marker").write_text("cached", encoding="utf-8")
if len(sys.argv) > 1 and sys.argv[1] == "report":
    print(json.dumps({
        "totals": {"sessions": 1, "projects": 1, "providers": {"codex": 1}},
        "sessions": [{
            "provider": "codex", "id": "session-a",
            "evidence": [{"at": "2026-08-09T00:00:00Z", "source": "codex:user", "text": "work"}],
            "previews": [{"text": "work"}],
        }],
        "projects": [{"cwd": "/fixture/project", "count": 1}],
    }))
    raise SystemExit(0)
if "--json" in sys.argv:
    print(json.dumps({
        "sessions": [
            {"provider": "codex", "id": "session-a"},
            {"provider": "claude", "id": "session-b"},
        ],
        "projects": [{"cwd": "/fixture/project", "count": 2}],
        "provider_errors": [],
    }))
    raise SystemExit(0)
diag = os.environ.get("ASM_STARTUP_DIAG_FILE")
if diag:
    pathlib.Path(diag).write_text(json.dumps([
        {"provider": "codex", "stage": "provider_total", "nanos": 1000},
        {"provider": "codex", "stage": "primary_parse", "nanos": 750, "bytes": 2048},
    ]), encoding="utf-8")
error = "session not found: __asm_real_startup_probe_missing__"
if "--provider" in sys.argv:
    error += " for provider " + sys.argv[sys.argv.index("--provider") + 1]
print(error, file=sys.stderr)
raise SystemExit(1)
""",
            encoding="utf-8",
        )
        self.fake_asm.chmod(0o755)

    def tearDown(self) -> None:
        self.temp.cleanup()

    def test_runner_writes_only_aggregate_artifacts(self) -> None:
        output = self.root / "output"
        result = subprocess.run(
            [
                sys.executable,
                str(SCRIPT),
                "--asm-bin",
                str(self.fake_asm),
                "--output-dir",
                str(output),
                "--revision",
                "fixture",
                "--cold-runs",
                "2",
                "--warmup-runs",
                "1",
                "--warm-runs",
                "3",
                "--diagnostics",
                "--provider-breakdown",
                "--report-period",
                "last-week",
            ],
            check=False,
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        summary = json.loads((output / "summary.json").read_text(encoding="utf-8"))
        samples = json.loads((output / "samples.json").read_text(encoding="utf-8"))
        self.assertEqual(summary["wall_time_seconds"]["cold"]["n"], 2)
        self.assertEqual(summary["wall_time_seconds"]["warm"]["n"], 3)
        self.assertTrue(summary["correctness"]["matches"])
        self.assertEqual(summary["correctness"]["cold"]["providers"], {"claude": 1, "codex": 1})
        self.assertEqual(len(samples["cold_ns"]), 2)
        self.assertEqual(len(samples["warm_ns"]), 3)
        self.assertIn("diagnostic_stages", summary)
        self.assertEqual(set(summary["provider_breakdown"]), {
            "claude", "codebuddy", "codex", "cursor", "kimi", "kiro",
            "openclaw", "opencode", "zcode",
        })
        self.assertTrue((output / "benchstat.txt").is_file())
        self.assertEqual(summary["report_correctness"]["evidence"], 1)
        self.assertEqual(len(summary["report_correctness"]["evidence_sha256"]), 64)
        self.assertFalse(any(output.rglob("marker")))
        self.assertFalse(any(path.name.endswith("raw.json") for path in output.rglob("*")))

    def test_nonempty_output_directory_fails_without_deleting_it(self) -> None:
        output = self.root / "existing"
        output.mkdir()
        sentinel = output / "keep.txt"
        sentinel.write_text("keep", encoding="utf-8")
        result = subprocess.run(
            [
                sys.executable,
                str(SCRIPT),
                "--asm-bin",
                str(self.fake_asm),
                "--output-dir",
                str(output),
                "--cold-runs",
                "1",
                "--warm-runs",
                "1",
            ],
            check=False,
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
        )
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(sentinel.read_text(encoding="utf-8"), "keep")

    def test_distribution_uses_nearest_rank_p95(self) -> None:
        module = load_module()
        stats = module.distribution(list(range(1, 21)))
        self.assertEqual(stats["median"], 10.5)
        self.assertEqual(stats["p95"], 19)

    def test_parse_strace_read_bytes_aggregates_only_successful_reads(self) -> None:
        module = load_module()
        trace = """123 read(3, \"x\", 10) = 10
124 pread64(4, \"y\", 20, 0) = 7
125 read(5, 0x1, 10) = -1 EAGAIN (Resource temporarily unavailable)
126 read(6, \"\", 10) = 0
"""
        self.assertEqual(module.parse_strace_read_bytes(trace), 17)

    def test_benchstat_uses_each_warm_cache_measurement(self) -> None:
        module = load_module()
        output = self.root / "benchstat.txt"
        samples = {
            "cold_ns": [100],
            "warm_ns": [200, 300],
            "cold_cache_bytes": [10],
            "warm_cache_bytes_samples": [20, 30],
            "warm_cache_bytes": 30,
        }
        module.write_benchstat(output, {}, samples)
        warm_lines = [
            line for line in output.read_text(encoding="utf-8").splitlines()
            if "RealStartup/warm" in line
        ]
        self.assertEqual(len(warm_lines), 2)
        self.assertIn("200 ns/op 20 cache-bytes", warm_lines[0])
        self.assertIn("300 ns/op 30 cache-bytes", warm_lines[1])


if __name__ == "__main__":
    unittest.main()
