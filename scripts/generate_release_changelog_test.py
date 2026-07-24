from __future__ import annotations

import json
import os
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path


SCRIPT = Path(__file__).with_name("generate-release-changelog.py").resolve()


class GenerateReleaseChangelogTest(unittest.TestCase):
    def setUp(self) -> None:
        self.temp = tempfile.TemporaryDirectory()
        self.repo = Path(self.temp.name)
        self.run_cmd("git", "init", "-q")
        self.run_cmd("git", "config", "user.name", "Release Tester")
        self.run_cmd("git", "config", "user.email", "release@example.invalid")
        self.commit("initial", "initial release", "Initial Author")
        self.run_cmd("git", "tag", "--no-sign", "-a", "v0.1.0", "-m", "v0.1.0")
        self.commit("feature", "Add provider support (#7)", "Merge Author")
        self.commit("docs", "Polish release documentation", "Direct Author")
        self.fixture = self.repo / "prs.json"
        self.fixture.write_text(
            json.dumps(
                [
                    {
                        "number": 7,
                        "title": "Add provider support",
                        "author": "original-contributor",
                        "is_bot": False,
                        "url": "https://github.com/acme/asm/pull/7",
                    }
                ]
            ),
            encoding="utf-8",
        )

    def tearDown(self) -> None:
        self.temp.cleanup()

    def run_cmd(
        self,
        *command: str,
        check: bool = True,
        env: dict[str, str] | None = None,
    ) -> subprocess.CompletedProcess[str]:
        result = subprocess.run(
            command,
            cwd=self.repo,
            env=env,
            check=False,
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
        )
        if check and result.returncode != 0:
            self.fail(f"{' '.join(command)} failed:\n{result.stderr}\n{result.stdout}")
        return result

    def commit(self, filename: str, subject: str, author: str) -> None:
        (self.repo / filename).write_text(subject, encoding="utf-8")
        self.run_cmd("git", "add", filename)
        commit_env = os.environ.copy()
        commit_env.update(
            {
                "GIT_AUTHOR_NAME": author,
                "GIT_AUTHOR_EMAIL": f"{author.replace(' ', '.')}@example.invalid",
                "GIT_AUTHOR_DATE": "2026-07-24T00:00:00Z",
                "GIT_COMMITTER_DATE": "2026-07-24T00:00:00Z",
            }
        )
        self.run_cmd("git", "commit", "-q", "-m", subject, env=commit_env)

    def generator(
        self,
        mode: str,
        output: Path,
        *,
        check: bool = True,
        target: str = "HEAD",
    ) -> subprocess.CompletedProcess[str]:
        return self.run_cmd(
            sys.executable,
            str(SCRIPT),
            "--version",
            "v0.2.0",
            "--target",
            target,
            "--mode",
            mode,
            "--output",
            str(output),
            "--repo",
            "acme/asm",
            "--pr-data",
            str(self.fixture),
            check=check,
        )

    def test_notes_credit_original_pr_and_direct_commit_authors(self) -> None:
        notes = self.repo / "notes.md"
        self.generator("notes", notes)
        content = notes.read_text(encoding="utf-8")
        self.assertIn("## v0.2.0", content)
        self.assertIn("Add provider support ([#7](https://github.com/acme/asm/pull/7))", content)
        self.assertIn("Thanks @original-contributor.", content)
        self.assertIn("Polish release documentation (`", content)
        self.assertIn("Thanks Direct Author.", content)

    def test_prepend_and_check_lock_the_generated_section(self) -> None:
        changelog = self.repo / "CHANGELOG.md"
        changelog.write_text("# Changelog\n\n## v0.1.0\n\n- Initial.\n", encoding="utf-8")
        self.generator("prepend", changelog)
        content = changelog.read_text(encoding="utf-8")
        self.assertLess(content.index("## v0.2.0"), content.index("## v0.1.0"))
        self.run_cmd("git", "add", "CHANGELOG.md")
        self.run_cmd("git", "commit", "-q", "-m", "Prepare v0.2.0 changelog (#8)")
        self.generator("check", changelog)

        changelog.write_text(
            content.replace("Thanks @original-contributor.", "Thanks @someone-else."),
            encoding="utf-8",
        )
        result = self.generator("check", changelog, check=False)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("differs from generated release history", result.stderr)

    def test_annotated_tag_does_not_leak_tag_metadata_or_signature(self) -> None:
        self.run_cmd("git", "tag", "--no-sign", "-a", "v0.2.0", "-m", "v0.2.0")
        notes = self.repo / "tag-notes.md"
        self.generator("notes", notes, target="v0.2.0")
        content = notes.read_text(encoding="utf-8")
        self.assertIn("## v0.2.0", content)
        self.assertNotIn("BEGIN SSH SIGNATURE", content)

    def test_release_with_only_changelog_commit_has_no_contributor_crash(self) -> None:
        self.run_cmd("git", "tag", "--no-sign", "-a", "v0.2.0", "-m", "v0.2.0")
        changelog = self.repo / "CHANGELOG.md"
        changelog.write_text("# Changelog\n", encoding="utf-8")
        self.run_cmd("git", "add", "CHANGELOG.md")
        self.run_cmd("git", "commit", "-q", "-m", "Prepare v0.2.1 changelog (#8)")
        notes = self.repo / "empty-notes.md"
        result = self.run_cmd(
            sys.executable,
            str(SCRIPT),
            "--version",
            "v0.2.1",
            "--target",
            "HEAD",
            "--output",
            str(notes),
            "--repo",
            "acme/asm",
            "--pr-data",
            str(self.fixture),
        )
        self.assertEqual(result.returncode, 0)
        self.assertIn("- No user-visible changes.", notes.read_text(encoding="utf-8"))

    def test_empty_fixture_fails_offline_instead_of_calling_github(self) -> None:
        self.fixture.write_text("[]", encoding="utf-8")
        notes = self.repo / "offline-notes.md"
        result = self.generator("notes", notes, check=False)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("--pr-data is missing PR #7", result.stderr)


if __name__ == "__main__":
    unittest.main()
