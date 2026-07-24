#!/usr/bin/env python3
"""Behavior tests for daily-agent-report output validation and retry handling."""

from __future__ import annotations

import json
import os
import stat
import subprocess
import tempfile
import textwrap
import unittest
from pathlib import Path


REPO_ROOT = Path(__file__).resolve().parents[1]
REPORT_SCRIPT = REPO_ROOT / "scripts" / "daily-agent-report.sh"
VALIDATOR = REPO_ROOT / "scripts" / "validate-agent-work-report.py"
CODEBUDDY_GENERATOR = REPO_ROOT / "scripts" / "report-generators" / "codebuddy.sh"
TELEGRAM_DELIVERY = REPO_ROOT / "scripts" / "report-deliveries" / "telegram.sh"


class ReportValidatorTests(unittest.TestCase):
    def validate(self, markdown: str) -> subprocess.CompletedProcess[str]:
        with tempfile.TemporaryDirectory() as temp_dir:
            report = Path(temp_dir) / "report.md"
            report.write_text(markdown, encoding="utf-8")
            return subprocess.run(
                ["python3", str(VALIDATOR), str(report)],
                check=False,
                capture_output=True,
                text=True,
            )

    def test_accepts_all_effort_levels(self) -> None:
        result = self.validate(
            textwrap.dedent(
                """\
                ## 工作概览
                1. [高投入] 核心项目：持续推进主要交付；下一步：完成验证
                2. [中投入] 协作事项：明确后续安排；下一步：跟进结论
                3. [低投入] 临时支持：完成简短确认；下一步：暂无

                ## 后续跟进
                - 继续推进核心项目

                ## 风险与阻塞
                - 暂无明确阻塞
                """
            )
        )
        self.assertEqual(result.returncode, 0, result.stderr)

    def test_rejects_missing_misplaced_or_percentage_effort(self) -> None:
        invalid_items = (
            "1. 核心项目：持续推进",
            "1. 核心项目 [高投入]：持续推进",
            "1. [高投入·约50%] 核心项目：持续推进",
            "1. [中投入] 核心项目：投入约 20％",
        )
        for item in invalid_items:
            with self.subTest(item=item):
                result = self.validate(
                    f"## 工作概览\n{item}\n\n"
                    "## 后续跟进\n- 暂无\n\n"
                    "## 风险与阻塞\n- 暂无明确阻塞\n"
                )
                self.assertNotEqual(result.returncode, 0)


class DailyReportScriptTests(unittest.TestCase):
    def setUp(self) -> None:
        self.temp_dir = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp_dir.cleanup)
        self.root = Path(self.temp_dir.name)
        self.bin_dir = self.root / "bin"
        self.bin_dir.mkdir()
        self.config = self.root / "config.json"
        self.config.write_text("{}\n", encoding="utf-8")
        self._write_executable(
            "fake-asm",
            """\
            #!/usr/bin/env python3
            import json
            import sys

            period = sys.argv[sys.argv.index("--period") + 1]
            weekly = period in {"last-week", "last-7-days"}
            start = "2026-07-13T00:00:00+08:00" if weekly else "2026-07-23T00:00:00+08:00"
            end = "2026-07-20T00:00:00+08:00" if weekly else "2026-07-24T00:00:00+08:00"
            evidence_at = "2026-07-15T10:00:00+08:00" if weekly else "2026-07-23T10:00:00+08:00"
            session = {
                "id": "session-1",
                "provider": "codex",
                "cwd": "/workspace/project",
                "created_at": evidence_at,
                "updated_at": evidence_at,
                "path": "/private/provider/path",
                "evidence": [{"text": "推进项目交付并完成跨团队确认", "at": evidence_at}],
                "evidence_count": 1,
            }
            print(json.dumps({
                "period": period,
                "start": start,
                "end": end,
                "timezone": "Asia/Shanghai",
                "evidence_rule": "fixture",
                "totals": {"sessions": 1, "projects": 1, "providers": {"codex": 1}},
                "projects": [{"cwd": session["cwd"], "count": 1, "updated": evidence_at}],
                "sessions": [session],
            }, ensure_ascii=False))
            """,
        )
        self._write_executable(
            "fake-meetings.py",
            """\
            #!/usr/bin/env python3
            import argparse
            import json
            from pathlib import Path

            parser = argparse.ArgumentParser()
            parser.add_argument("--start")
            parser.add_argument("--end")
            parser.add_argument("--output", required=True)
            args = parser.parse_args()
            Path(args.output).write_text(json.dumps({
                "status": "ok",
                "start": args.start,
                "end": args.end,
                "meetings": [{
                    "subject": "项目协作会",
                    "start_time": "2026-07-23T14:00:00+08:00",
                    "end_time": "2026-07-23T15:30:00+08:00",
                    "meeting_type": "快速会议",
                }],
                "smart_minutes": [],
                "errors": [],
                "traces": [],
            }, ensure_ascii=False), encoding="utf-8")
            """,
        )
        self._write_executable(
            "fake-codebuddy",
            """\
            #!/usr/bin/env python3
            import os
            from pathlib import Path
            import sys

            state = Path(os.environ["FAKE_CODEBUDDY_STATE"])
            count = int(state.read_text(encoding="utf-8")) if state.exists() else 0
            count += 1
            state.write_text(str(count), encoding="utf-8")
            prompt = sys.stdin.read()
            Path(os.environ["FAKE_CODEBUDDY_PROMPT_DIR"], f"prompt-{count}.txt").write_text(
                prompt, encoding="utf-8"
            )
            invalid = os.environ.get("FAKE_CODEBUDDY_ALWAYS_INVALID") == "1"
            invalid = invalid or (
                os.environ.get("FAKE_CODEBUDDY_INVALID_FIRST") == "1" and count == 1
            )
            if invalid:
                print("## 工作概览\\n1. 项目：缺少投入等级\\n\\n"
                      "## 后续跟进\\n- 暂无\\n\\n"
                      "## 风险与阻塞\\n- 暂无明确阻塞")
            else:
                print("## 工作概览\\n1. [高投入] 项目：推进主要交付；下一步：完成验证\\n\\n"
                      "## 后续跟进\\n- 继续推进\\n\\n"
                      "## 风险与阻塞\\n- 暂无明确阻塞")
            """,
        )
        self._write_executable(
            "fake-generator",
            """\
            #!/usr/bin/env python3
            import argparse
            import os
            from pathlib import Path

            parser = argparse.ArgumentParser()
            parser.add_argument("--prompt", required=True)
            args = parser.parse_args()
            prompt = Path(args.prompt).read_text(encoding="utf-8")
            Path(os.environ["FAKE_CUSTOM_PROMPT"]).write_text(prompt, encoding="utf-8")
            print("## 工作概览\\n1. [中投入] 自定义生成：完成替换验证；下一步：接入正式模型\\n\\n"
                  "## 后续跟进\\n- 暂无\\n\\n"
                  "## 风险与阻塞\\n- 暂无明确阻塞")
            """,
        )
        self._write_executable(
            "fake-delivery",
            """\
            #!/usr/bin/env python3
            import argparse
            import os
            from pathlib import Path

            parser = argparse.ArgumentParser()
            parser.add_argument("--report", required=True)
            args = parser.parse_args()
            Path(os.environ["FAKE_DELIVERY_SINK"]).write_text(
                Path(args.report).read_text(encoding="utf-8"),
                encoding="utf-8",
            )
            """,
        )

    def _write_executable(self, name: str, content: str) -> Path:
        path = self.bin_dir / name
        path.write_text(textwrap.dedent(content), encoding="utf-8")
        path.chmod(path.stat().st_mode | stat.S_IXUSR)
        return path

    def run_report(
        self,
        period: str,
        *,
        invalid_first: bool = False,
        always_invalid: bool = False,
        dry_run: bool = True,
        generator_script: Path | None = None,
        delivery_script: Path | None = None,
    ) -> tuple[subprocess.CompletedProcess[str], Path]:
        run_dir = self.root / f"run-{period}"
        prompt_dir = self.root / f"prompts-{period}"
        prompt_dir.mkdir()
        env = {
            **os.environ,
            "FAKE_CODEBUDDY_STATE": str(self.root / f"state-{period}"),
            "FAKE_CODEBUDDY_PROMPT_DIR": str(prompt_dir),
            "FAKE_CODEBUDDY_INVALID_FIRST": "1" if invalid_first else "0",
            "FAKE_CODEBUDDY_ALWAYS_INVALID": "1" if always_invalid else "0",
            "FAKE_CUSTOM_PROMPT": str(self.root / f"custom-prompt-{period}.txt"),
            "FAKE_DELIVERY_SINK": str(self.root / f"delivery-{period}.md"),
        }
        command = [
            "bash",
            str(REPORT_SCRIPT),
            "--period",
            period,
            "--asm-bin",
            str(self.bin_dir / "fake-asm"),
            "--codebuddy-bin",
            str(self.bin_dir / "fake-codebuddy"),
            "--config",
            str(self.config),
            "--out-dir",
            str(run_dir),
            "--meeting-context-script",
            str(self.bin_dir / "fake-meetings.py"),
            "--codebuddy-attempts",
            "2",
        ]
        if generator_script is not None:
            command.extend(["--generator-script", str(generator_script)])
        if delivery_script is not None:
            command.extend(["--delivery-script", str(delivery_script)])
        if dry_run:
            command.append("--dry-run")
        result = subprocess.run(
            command,
            cwd=REPO_ROOT,
            env=env,
            check=False,
            capture_output=True,
            text=True,
        )
        return result, prompt_dir

    def test_invalid_first_report_retries_and_keeps_meeting_times(self) -> None:
        result, prompt_dir = self.run_report("today", invalid_first=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("attempt 2 succeeded", result.stdout)
        prompt = (prompt_dir / "prompt-1.txt").read_text(encoding="utf-8")
        self.assertIn('"start_time": "2026-07-23T14:00:00+08:00"', prompt)
        self.assertIn('"end_time": "2026-07-23T15:30:00+08:00"', prompt)
        retry_prompt = (prompt_dir / "prompt-2.txt").read_text(encoding="utf-8")
        self.assertIn("上一版格式校验失败", retry_prompt)

    def test_consecutive_invalid_reports_fail_without_delivery(self) -> None:
        result, _ = self.run_report("today", always_invalid=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("failed after 2 attempts", result.stderr)
        self.assertNotIn("Dry run enabled", result.stdout)

    def test_daily_and_weekly_prompts_use_relative_effort_window(self) -> None:
        daily, daily_prompts = self.run_report("today")
        weekly, weekly_prompts = self.run_report("last-week")
        self.assertEqual(daily.returncode, 0, daily.stderr)
        self.assertEqual(weekly.returncode, 0, weekly.stderr)
        self.assertIn(
            "日报按当天相对投入判断",
            (daily_prompts / "prompt-1.txt").read_text(encoding="utf-8"),
        )
        self.assertIn(
            "周报按整个统计周相对投入判断",
            (weekly_prompts / "prompt-1.txt").read_text(encoding="utf-8"),
        )

    def test_custom_generator_and_delivery_replace_defaults(self) -> None:
        result, _ = self.run_report(
            "today",
            dry_run=False,
            generator_script=self.bin_dir / "fake-generator",
            delivery_script=self.bin_dir / "fake-delivery",
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        delivered = (self.root / "delivery-today.md").read_text(encoding="utf-8")
        self.assertIn("[中投入] 自定义生成", delivered)
        custom_prompt = (self.root / "custom-prompt-today.txt").read_text(encoding="utf-8")
        self.assertIn("UNTRUSTED REPORT EVIDENCE", custom_prompt)

    def test_bundled_adapters_expose_help(self) -> None:
        for adapter in (CODEBUDDY_GENERATOR, TELEGRAM_DELIVERY):
            with self.subTest(adapter=adapter):
                result = subprocess.run(
                    [str(adapter), "--help"],
                    cwd=REPO_ROOT,
                    check=False,
                    capture_output=True,
                    text=True,
                )
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertIn("Examples:", result.stdout)


if __name__ == "__main__":
    unittest.main()
