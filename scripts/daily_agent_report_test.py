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
OLLAMA_GENERATOR = REPO_ROOT / "scripts" / "report-generators" / "ollama.sh"
LOCAL_DELIVERY = REPO_ROOT / "scripts" / "report-deliveries" / "local-file.sh"
COMBINED_DELIVERY = REPO_ROOT / "scripts" / "report-deliveries" / "local-file-and-telegram.sh"
TELEGRAM_DELIVERY = REPO_ROOT / "scripts" / "report-deliveries" / "telegram.sh"
MEETING_COLLECTOR = (
    REPO_ROOT
    / "skills"
    / "tencent-meeting-summary"
    / "scripts"
    / "collect-tencent-meeting-context.py"
)


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
        self.empty_env = self.root / "empty.env"
        self.empty_env.write_text("", encoding="utf-8")
        self._write_executable(
            "fake-asm",
            """\
            #!/usr/bin/env python3
            import json
            import sys

            if "--period" in sys.argv:
                period = sys.argv[sys.argv.index("--period") + 1]
                custom_start = None
                custom_end = None
            else:
                period = "custom"
                custom_start = sys.argv[sys.argv.index("--start") + 1]
                custom_end = sys.argv[sys.argv.index("--end") + 1]
            weekly = period in {"last-week", "last-7-days"}
            start = custom_start or ("2026-07-13T00:00:00+08:00" if weekly else "2026-07-23T00:00:00+08:00")
            end = custom_end or ("2026-07-20T00:00:00+08:00" if weekly else "2026-07-24T00:00:00+08:00")
            evidence_at = "2026-07-31T10:00:00+08:00" if custom_start else (
                "2026-07-15T10:00:00+08:00" if weekly else "2026-07-23T10:00:00+08:00"
            )
            session = {
                "id": "session-1",
                "provider": "codex",
                "cwd": "/workspace/project",
                "created_at": evidence_at,
                "updated_at": evidence_at,
                "path": "/private/provider/path",
                "evidence": [{
                    "text": "清理 IPv6 合并限速与锐驰 COS 免费套餐包两项已结束的灰度控制",
                    "at": evidence_at,
                }],
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
            import os
            from pathlib import Path

            parser = argparse.ArgumentParser()
            parser.add_argument("--start")
            parser.add_argument("--end")
            parser.add_argument("--output", required=True)
            args = parser.parse_args()
            Path(args.output).write_text(json.dumps({
                "status": os.environ.get("FAKE_MEETING_STATUS", "ok"),
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
            import json
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
            if args_path := os.environ.get("FAKE_CODEBUDDY_ARGS"):
                Path(args_path).write_text(
                    json.dumps(sys.argv[1:]), encoding="utf-8"
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
            "curl",
            """\
            #!/usr/bin/env python3
            import json
            import os
            from pathlib import Path
            import sys

            Path(os.environ["FAKE_CURL_ARGS"]).write_text(
                json.dumps(sys.argv[1:], ensure_ascii=False),
                encoding="utf-8",
            )
            print("{}")
            """,
        )
        self._write_executable(
            "fake-ollama-curl",
            """\
            #!/usr/bin/env python3
            import json
            import os
            from pathlib import Path
            import sys

            args = sys.argv[1:]
            if args_path := os.environ.get("FAKE_OLLAMA_ARGS"):
                Path(args_path).write_text(
                    json.dumps(args, ensure_ascii=False), encoding="utf-8"
                )

            def option_value(name):
                return args[args.index(name) + 1]

            request_path = option_value("--data-binary")[1:]
            response_path = option_value("--output")
            config_path = option_value("--config")
            Path(os.environ["FAKE_OLLAMA_REQUEST"]).write_text(
                Path(request_path).read_text(encoding="utf-8"), encoding="utf-8"
            )
            Path(os.environ["FAKE_OLLAMA_CONFIG"]).write_text(
                Path(config_path).read_text(encoding="utf-8"), encoding="utf-8"
            )
            Path(response_path).write_text(json.dumps({
                "choices": [{"message": {"content": (
                    "## 工作概览\\n"
                    "1. [中投入] Ollama 试验：完成生成验证；下一步：持续推送\\n\\n"
                    "## 后续跟进\\n- 持续推送\\n\\n"
                    "## 风险与阻塞\\n- 暂无明确阻塞"
                )}}]
            }, ensure_ascii=False), encoding="utf-8")
            print("200")
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
            "fake-granularity-generator",
            """\
            #!/usr/bin/env python3
            import argparse
            from pathlib import Path

            parser = argparse.ArgumentParser()
            parser.add_argument("--prompt", required=True)
            args = parser.parse_args()
            prompt = Path(args.prompt).read_text(encoding="utf-8")
            preserves_scope = "抽象实现细节，不得抽象业务范围" in prompt
            has_business_objects = (
                "IPv6 合并限速" in prompt and "锐驰 COS 免费套餐包" in prompt
            )
            if preserves_scope and has_business_objects:
                progress = (
                    "完成 IPv6 合并限速与锐驰 COS 免费套餐包两项灰度控制清理"
                )
            else:
                progress = "完成核心服务冗余代码清理"
            print(
                "## 工作概览\\n"
                f"1. [高投入] Lighthouse：{progress}；下一步：完成回归验证\\n\\n"
                "## 后续跟进\\n- 完成回归验证\\n\\n"
                "## 风险与阻塞\\n- 暂无明确阻塞"
            )
            """,
        )
        self._write_executable(
            "fake-meeting-fallback-generator",
            """\
            #!/usr/bin/env python3
            import argparse
            from pathlib import Path

            parser = argparse.ArgumentParser()
            parser.add_argument("--prompt", required=True)
            args = parser.parse_args()
            prompt = Path(args.prompt).read_text(encoding="utf-8")
            legacy_coverage_warning = "在风险与阻塞中简要说明会议覆盖不完整" in prompt
            if legacy_coverage_warning:
                report = (
                    "## 工作概览\\n"
                    "1. [中投入] 工作事项：继续推进；下一步：补充确认\\n\\n"
                    "## 后续跟进\\n- 补充确认会议结论\\n\\n"
                    "## 风险与阻塞\\n- 会议覆盖不完整"
                )
            else:
                report = (
                    "## 工作概览\\n"
                    "1. [中投入] 项目协作会：围绕项目协作进行宽泛沟通（据会议名称推测）；下一步：结合实际讨论补充确认\\n\\n"
                    "## 后续跟进\\n- 如需会议结论，补充确认具体讨论内容\\n\\n"
                    "## 风险与阻塞\\n- 暂无明确阻塞"
                )
            print(report)
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
        generator_provider: str = "codebuddy",
        delivery_provider: str = "both",
        local_report_dir: Path | None = None,
        custom_start: str | None = None,
        custom_end: str | None = None,
        extra_env: dict[str, str] | None = None,
        env_file: Path | None = None,
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
        if extra_env:
            env.update(extra_env)
        command = [
            "bash",
            str(REPORT_SCRIPT),
            "--asm-bin",
            str(self.bin_dir / "fake-asm"),
            "--codebuddy-bin",
            str(self.bin_dir / "fake-codebuddy"),
            "--generator-provider",
            generator_provider,
            "--delivery-provider",
            delivery_provider,
            "--config",
            str(self.config),
            "--out-dir",
            str(run_dir),
            "--meeting-context-script",
            str(self.bin_dir / "fake-meetings.py"),
            "--codebuddy-attempts",
            "2",
        ]
        if local_report_dir is not None:
            command.extend(["--local-report-dir", str(local_report_dir)])
        if custom_start is not None or custom_end is not None:
            if custom_start is None or custom_end is None:
                raise ValueError("custom_start and custom_end must be provided together")
            command.extend(["--start", custom_start, "--end", custom_end])
        else:
            command.extend(["--period", period])
        if env_file is not None:
            command.extend(["--env-file", str(env_file)])
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
        self.assertIn('"subject": "项目协作会"', prompt)
        self.assertIn("据会议名称推测", prompt)
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

    def test_custom_window_can_generate_last_friday_report(self) -> None:
        result, prompt_dir = self.run_report(
            "last-friday",
            custom_start="2026-07-31",
            custom_end="2026-08-01",
        )

        self.assertEqual(result.returncode, 0, result.stderr)
        prompt = (prompt_dir / "prompt-1.txt").read_text(encoding="utf-8")
        self.assertIn('"start": "2026-07-31"', prompt)
        self.assertIn('"end": "2026-08-01"', prompt)

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

    def test_default_delivery_writes_one_authoritative_local_markdown_file(self) -> None:
        local_report_dir = self.root / "local-reports"
        result, _ = self.run_report(
            "today",
            dry_run=False,
            delivery_provider="local-file",
            local_report_dir=local_report_dir,
        )

        self.assertEqual(result.returncode, 0, result.stderr)
        report_path = local_report_dir / "daily-2026-07-23.md"
        self.assertTrue(report_path.is_file())
        self.assertIn("[高投入] 项目：推进主要交付", report_path.read_text(encoding="utf-8"))
        self.assertIn("Local report written", result.stdout)

        second = self.run_report(
            "today-second-run",
            dry_run=False,
            delivery_provider="local-file",
            local_report_dir=local_report_dir,
        )[0]
        self.assertEqual(second.returncode, 0, second.stderr)
        self.assertIn("already exists", second.stdout)
        self.assertEqual(len(list(local_report_dir.glob("*.md"))), 1)

    def test_default_delivery_writes_local_and_sends_telegram(self) -> None:
        local_report_dir = self.root / "combined-reports"
        curl_args_path = self.root / "combined-curl-args.json"
        delivery_env = {
            "PATH": f"{self.bin_dir}{os.pathsep}{os.environ['PATH']}",
            "TELEGRAM_BOT_TOKEN": "fixture-token",
            "TELEGRAM_CHAT_ID": "fixture-chat",
            "FAKE_CURL_ARGS": str(curl_args_path),
        }
        result, _ = self.run_report(
            "combined-delivery",
            dry_run=False,
            local_report_dir=local_report_dir,
            extra_env=delivery_env,
        )

        self.assertEqual(result.returncode, 0, result.stderr)
        report_path = local_report_dir / "daily-2026-07-23.md"
        self.assertTrue(report_path.is_file())
        self.assertIn("Local report written", result.stdout)
        self.assertIn("Telegram report sent.", result.stdout)
        curl_args = json.loads(curl_args_path.read_text(encoding="utf-8"))
        self.assertIn(
            "https://api.telegram.org/botfixture-token/sendMessage",
            curl_args,
        )

        second, _ = self.run_report(
            "combined-delivery-second",
            dry_run=False,
            local_report_dir=local_report_dir,
            extra_env=delivery_env,
        )
        self.assertEqual(second.returncode, 0, second.stderr)
        self.assertIn("already exists", second.stdout)
        self.assertIn("Telegram report sent.", second.stdout)
        self.assertEqual(len(list(local_report_dir.glob("*.md"))), 1)

    def test_report_preserves_evidence_backed_business_scope(self) -> None:
        result, _ = self.run_report(
            "today",
            generator_script=self.bin_dir / "fake-granularity-generator",
        )

        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("IPv6 合并限速", result.stdout)
        self.assertIn("锐驰 COS 免费套餐包", result.stdout)
        self.assertNotIn("核心服务冗余代码清理", result.stdout)

    def test_failed_meeting_context_uses_subject_fallback_without_coverage_risk(self) -> None:
        for status in ("partial", "unavailable"):
            with self.subTest(status=status):
                result, _ = self.run_report(
                    f"meeting-title-fallback-{status}",
                    generator_script=self.bin_dir / "fake-meeting-fallback-generator",
                    extra_env={"FAKE_MEETING_STATUS": status},
                )

                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertIn("项目协作会", result.stdout)
                self.assertIn("据会议名称推测", result.stdout)
                self.assertNotIn("会议覆盖不完整", result.stdout)

    def test_bundled_adapters_expose_help(self) -> None:
        for adapter in (
            CODEBUDDY_GENERATOR,
            OLLAMA_GENERATOR,
            LOCAL_DELIVERY,
            COMBINED_DELIVERY,
            TELEGRAM_DELIVERY,
        ):
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

    def test_local_delivery_keeps_existing_report_as_authoritative(self) -> None:
        report = self.root / "local-report.md"
        report.write_text("第一版报告\n", encoding="utf-8")
        output_dir = self.root / "local-output"
        env = {
            **os.environ,
            "REPORT_WINDOW_START": "2026-07-31T00:00:00+08:00",
            "REPORT_REPORT_KIND": "daily",
        }

        first = subprocess.run(
            [str(LOCAL_DELIVERY), "--report", str(report), "--output-dir", str(output_dir)],
            cwd=REPO_ROOT,
            env=env,
            check=False,
            capture_output=True,
            text=True,
        )
        self.assertEqual(first.returncode, 0, first.stderr)

        report.write_text("第二版报告\n", encoding="utf-8")
        second = subprocess.run(
            [str(LOCAL_DELIVERY), "--report", str(report), "--output-dir", str(output_dir)],
            cwd=REPO_ROOT,
            env=env,
            check=False,
            capture_output=True,
            text=True,
        )
        self.assertEqual(second.returncode, 0, second.stderr)
        self.assertIn("already exists", second.stdout)
        self.assertEqual(
            (output_dir / "daily-2026-07-31.md").read_text(encoding="utf-8"),
            "第一版报告\n",
        )

    def test_local_delivery_rejects_symlink_target(self) -> None:
        report = self.root / "symlink-report.md"
        report.write_text("新报告\n", encoding="utf-8")
        output_dir = self.root / "symlink-output"
        output_dir.mkdir()
        symlink_target = self.root / "authoritative-target.md"
        symlink_target.write_text("原始文件\n", encoding="utf-8")
        (output_dir / "daily-2026-07-31.md").symlink_to(symlink_target)

        result = subprocess.run(
            [
                str(LOCAL_DELIVERY),
                "--report",
                str(report),
                "--output-dir",
                str(output_dir),
                "--key",
                "daily-2026-07-31",
            ],
            cwd=REPO_ROOT,
            check=False,
            capture_output=True,
            text=True,
        )

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("symlink", result.stderr)
        self.assertEqual(symlink_target.read_text(encoding="utf-8"), "原始文件\n")

    def test_ollama_adapter_uses_openai_compatible_cloud_contract(self) -> None:
        prompt = self.root / "ollama-prompt.txt"
        prompt.write_text("只根据这里的证据生成报告", encoding="utf-8")
        request_path = self.root / "ollama-request.json"
        config_path = self.root / "ollama-curl.conf"
        args_path = self.root / "ollama-curl-args.json"
        env = {
            **os.environ,
            "PATH": f"{self.bin_dir}{os.pathsep}{os.environ['PATH']}",
            "OLLAMA_API_KEY": "fixture-secret",
            "OLLAMA_BASE_URL": "https://ollama.example/v1",
            "OLLAMA_MODEL": "deepseek-v4-flash:0731-cloud",
            "FAKE_OLLAMA_REQUEST": str(request_path),
            "FAKE_OLLAMA_CONFIG": str(config_path),
            "FAKE_OLLAMA_ARGS": str(args_path),
        }

        result = subprocess.run(
            [str(OLLAMA_GENERATOR), "--prompt", str(prompt)],
            cwd=REPO_ROOT,
            env={**env, "OLLAMA_CURL_BIN": str(self.bin_dir / "fake-ollama-curl")},
            check=False,
            capture_output=True,
            text=True,
        )

        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("## 工作概览", result.stdout)
        request = json.loads(request_path.read_text(encoding="utf-8"))
        self.assertEqual(request["model"], "deepseek-v4-flash:0731-cloud")
        self.assertEqual(request["reasoning_effort"], "max")
        self.assertEqual(request["messages"], [{
            "role": "user",
            "content": "只根据这里的证据生成报告",
        }])
        curl_args = json.loads(args_path.read_text(encoding="utf-8"))
        self.assertEqual(curl_args[-1], "https://ollama.example/v1/chat/completions")
        self.assertNotIn("fixture-secret", curl_args)
        self.assertIn(
            'header = "Authorization: Bearer fixture-secret"',
            config_path.read_text(encoding="utf-8"),
        )

    def test_ollama_provider_can_replace_codebuddy_in_orchestrator(self) -> None:
        request_path = self.root / "orchestrator-ollama-request.json"
        config_path = self.root / "orchestrator-ollama-curl.conf"
        result, _ = self.run_report(
            "today",
            generator_provider="ollama",
            extra_env={
                "PATH": f"{self.bin_dir}{os.pathsep}{os.environ['PATH']}",
                "OLLAMA_API_KEY": "fixture-secret",
                "OLLAMA_BASE_URL": "https://ollama.example/v1",
                "OLLAMA_MODEL": "fixture-model",
                "OLLAMA_CURL_BIN": str(self.bin_dir / "fake-ollama-curl"),
                "FAKE_OLLAMA_REQUEST": str(request_path),
                "FAKE_OLLAMA_CONFIG": str(config_path),
            },
            env_file=self.empty_env,
        )

        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("report-generators/ollama.sh", result.stdout)
        request = json.loads(request_path.read_text(encoding="utf-8"))
        self.assertEqual(request["model"], "fixture-model")
        self.assertEqual(request["reasoning_effort"], "max")

    def test_codebuddy_adapter_preserves_safe_default_generation_contract(self) -> None:
        prompt = self.root / "adapter-prompt.txt"
        prompt.write_text("只根据这里的证据生成报告", encoding="utf-8")
        prompt_dir = self.root / "adapter-prompts"
        prompt_dir.mkdir()
        args_path = self.root / "codebuddy-args.json"
        env = {
            **os.environ,
            "REPORT_CODEBUDDY_BIN": str(self.bin_dir / "fake-codebuddy"),
            "FAKE_CODEBUDDY_STATE": str(self.root / "adapter-state"),
            "FAKE_CODEBUDDY_PROMPT_DIR": str(prompt_dir),
            "FAKE_CODEBUDDY_ARGS": str(args_path),
        }

        result = subprocess.run(
            [
                str(CODEBUDDY_GENERATOR),
                "--prompt",
                str(prompt),
                "--model",
                "fixture-model",
            ],
            cwd=REPO_ROOT,
            env=env,
            check=False,
            capture_output=True,
            text=True,
        )

        self.assertEqual(result.returncode, 0, result.stderr)
        args = json.loads(args_path.read_text(encoding="utf-8"))
        self.assertIn("--print", args)
        self.assertEqual(args[args.index("--permission-mode") + 1], "dontAsk")
        self.assertEqual(args[args.index("--tools") + 1], "")
        self.assertIn("--strict-mcp-config", args)
        self.assertEqual(args[args.index("--max-turns") + 1], "50")
        self.assertEqual(args[args.index("--model") + 1], "fixture-model")
        self.assertEqual(
            (prompt_dir / "prompt-1.txt").read_text(encoding="utf-8"),
            "只根据这里的证据生成报告",
        )

    def test_telegram_adapter_delivers_rendered_report_with_env_credentials(self) -> None:
        report = self.root / "telegram-report.md"
        report.write_text(
            "## 工作概览\n"
            "1. [高投入] 项目：完成 `验证`；下一步：继续推进\n\n"
            "## 风险与阻塞\n"
            "- 暂无\n",
            encoding="utf-8",
        )
        args_path = self.root / "curl-args.json"
        env = {
            **os.environ,
            "PATH": f"{self.bin_dir}{os.pathsep}{os.environ['PATH']}",
            "TELEGRAM_BOT_TOKEN": "fixture-token",
            "TELEGRAM_CHAT_ID": "fixture-chat",
            "FAKE_CURL_ARGS": str(args_path),
        }

        result = subprocess.run(
            [
                str(TELEGRAM_DELIVERY),
                "--report",
                str(report),
                "--title",
                "<晨会日报>",
            ],
            cwd=REPO_ROOT,
            env=env,
            check=False,
            capture_output=True,
            text=True,
        )

        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("Telegram report sent.", result.stdout)
        args = json.loads(args_path.read_text(encoding="utf-8"))
        self.assertIn(
            "https://api.telegram.org/botfixture-token/sendMessage",
            args,
        )
        self.assertIn("chat_id=fixture-chat", args)
        self.assertIn("parse_mode=HTML", args)
        text_arg = next(arg for arg in args if arg.startswith("text="))
        self.assertIn("<b>&lt;晨会日报&gt;</b>", text_arg)
        self.assertIn("<b>工作概览</b>", text_arg)
        self.assertIn("<code>验证</code>", text_arg)
        self.assertIn("• 暂无", text_arg)


class TencentMeetingCollectorTests(unittest.TestCase):
    def setUp(self) -> None:
        self.temp_dir = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp_dir.cleanup)
        self.root = Path(self.temp_dir.name)
        self.skill_dir = self.root / "meeting-skill"
        scripts_dir = self.skill_dir / "scripts"
        scripts_dir.mkdir(parents=True)
        tool = scripts_dir / "tencent_meeting.py"
        tool.write_text(
            textwrap.dedent(
                """\
                #!/usr/bin/env python3
                import json
                import os
                from pathlib import Path
                import sys

                request = json.loads(sys.argv[2])
                name = request["name"]
                arguments = request["arguments"]
                with Path(os.environ["FAKE_MEETING_CALLS"]).open("a", encoding="utf-8") as log:
                    log.write(json.dumps(request, ensure_ascii=False) + "\\n")

                status_code = 200
                if name == "get_user_ended_meetings":
                    if arguments.get("page_token") == "second":
                        body = {
                            "meeting_info_list": [{
                                "subject": "发布评审",
                                "start_time": "2026-07-23T15:00:00+08:00",
                                "end_time": "2026-07-23T16:00:00+08:00",
                                "meeting_type": "快速会议",
                            }],
                            "has_more": False,
                        }
                    else:
                        body = {
                            "meeting_info_list": [{
                                "subject": "项目晨会",
                                "start_time": "2026-07-23T09:00:00+08:00",
                                "end_time": "2026-07-23T09:30:00+08:00",
                                "meeting_type": "周期性会议",
                            }],
                            "has_more": True,
                            "next_page_token": "second",
                        }
                elif name == "get_records_list":
                    body = {
                        "record_meetings": [
                            {
                                "meeting_id": "meeting-1",
                                "subject": "项目晨会",
                                "record_type": "智能录制",
                                "record_files": [{"record_file_id": "fallback-file"}],
                            },
                            {
                                "meeting_id": "meeting-1",
                                "subject": "项目晨会",
                                "record_type": "云录制",
                                "record_files": [{"record_file_id": "preferred-file"}],
                            },
                            {
                                "meeting_id": "meeting-2",
                                "subject": "发布评审",
                                "record_type": "云录制",
                                "record_files": [{"record_file_id": "unavailable-file"}],
                            },
                        ],
                        "has_more": False,
                    }
                elif arguments["record_file_id"] == "preferred-file":
                    body = {
                        "meeting_minute": {
                            "minute": "明确本周交付范围",
                            "todo": "跟进验收安排",
                        }
                    }
                else:
                    status_code = 500
                    body = {"message": "minutes unavailable"}

                print(json.dumps({
                    "status_code": status_code,
                    "body": json.dumps(body, ensure_ascii=False),
                    "headers": {"X-Tc-Trace": f"trace-{name}", "rpcUuid": f"rpc-{name}"},
                }, ensure_ascii=False))
                """
            ),
            encoding="utf-8",
        )
        tool.chmod(tool.stat().st_mode | stat.S_IXUSR)

    def test_collects_paginated_meetings_and_keeps_partial_summary_context(self) -> None:
        output = self.root / "meeting-context.json"
        calls_path = self.root / "meeting-calls.jsonl"
        env = {
            **os.environ,
            "TENCENT_MEETING_TOKEN": "fixture-token",
            "FAKE_MEETING_CALLS": str(calls_path),
        }

        result = subprocess.run(
            [
                "python3",
                str(MEETING_COLLECTOR),
                "--start",
                "2026-07-23T00:00:00+08:00",
                "--end",
                "2026-07-24T00:00:00+08:00",
                "--output",
                str(output),
                "--meeting-skill-dir",
                str(self.skill_dir),
            ],
            cwd=REPO_ROOT,
            env=env,
            check=False,
            capture_output=True,
            text=True,
        )

        self.assertEqual(result.returncode, 0, result.stderr)
        payload = json.loads(output.read_text(encoding="utf-8"))
        self.assertEqual(payload["status"], "partial")
        self.assertEqual(
            [meeting["subject"] for meeting in payload["meetings"]],
            ["项目晨会", "发布评审"],
        )
        self.assertEqual(
            payload["smart_minutes"],
            [{
                "subject": "项目晨会",
                "minute": "明确本周交付范围",
                "todo": "跟进验收安排",
            }],
        )
        self.assertIn("smart minutes for 发布评审", payload["errors"][0])

        calls = [
            json.loads(line)
            for line in calls_path.read_text(encoding="utf-8").splitlines()
        ]
        ended_calls = [
            call for call in calls if call["name"] == "get_user_ended_meetings"
        ]
        self.assertEqual(len(ended_calls), 2)
        self.assertEqual(ended_calls[1]["arguments"]["page_token"], "second")
        minute_file_ids = [
            call["arguments"]["record_file_id"]
            for call in calls
            if call["name"] == "get_smart_minutes"
        ]
        self.assertEqual(
            minute_file_ids,
            ["preferred-file", "unavailable-file"],
        )


if __name__ == "__main__":
    unittest.main()
