# asm

A local TUI for finding, inspecting, and resuming coding-agent sessions across
projects.

Providers:

- Codex CLI sessions stored under `$CODEX_HOME/sessions` or `~/.codex/sessions`.
  Titles prefer Codex's native `$CODEX_HOME/session_index.jsonl` thread names,
  then fall back to `history.jsonl` and rollout user messages.
- Claude Code sessions stored under `$CLAUDE_HOME/projects` or
  `~/.claude/projects`. Resume runs from the original session cwd with
  `claude --resume <session-id>`.
- Kimi Code sessions stored under `$KIMI_CODE_HOME` / `$KIMI_HOME` or
  `~/.kimi-code`. Resume runs from the original session cwd with
  `kimi --session <session-id>`.
- Kiro CLI sessions stored under `$KIRO_HOME/sessions/cli` or
  `~/.kiro/sessions/cli`. Resume runs from the original session cwd with
  `kiro-cli --resume-id <session-id>`.
- opencode sessions stored under `$OPENCODE_HOME/storage` or
  `~/.local/share/opencode/storage`. Resume runs from the original session cwd
  with `opencode -s <session-id>`.
- ZCode sessions stored in a SQLite database under `$ZCODE_HOME/cli/db/db.sqlite`
  or `~/.zcode/cli/db/db.sqlite`. ZCode is an Electron desktop app without a CLI
  or documented resume path, so asm treats zcode as discover-only; the reported
  resume command (`zcode --resume <session-id>`) is a future-compatible
  placeholder.

## Usage

```sh
go run ./cmd/asm
```

## Install

Download a prebuilt binary from the
[latest GitHub Release](https://github.com/hxy91819/agent-session-manager/releases/latest).
Release archives are published for Linux, macOS, and Windows on amd64 and arm64,
with checksums in `sha256sums.txt`.

Linux and macOS:

```sh
version="${ASM_VERSION:-$(curl -fsSL https://api.github.com/repos/hxy91819/agent-session-manager/releases/latest | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p')}"
case "$(uname -s)" in
  Linux) os="linux" ;;
  Darwin) os="darwin" ;;
  *) echo "unsupported OS: $(uname -s)" >&2; exit 1 ;;
esac
case "$(uname -m)" in
  x86_64|amd64) arch="amd64" ;;
  arm64|aarch64) arch="arm64" ;;
  *) echo "unsupported architecture: $(uname -m)" >&2; exit 1 ;;
esac
tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT
curl -fL -o "${tmpdir}/asm.tar.gz" "https://github.com/hxy91819/agent-session-manager/releases/download/${version}/asm_${version}_${os}_${arch}.tar.gz"
tar -C "${tmpdir}" -xzf "${tmpdir}/asm.tar.gz"
sudo install -m 0755 "${tmpdir}/asm_${version}_${os}_${arch}/asm" /usr/local/bin/asm
```

Windows PowerShell:

```powershell
$Version = (Invoke-RestMethod "https://api.github.com/repos/hxy91819/agent-session-manager/releases/latest").tag_name
$Arch = switch ([System.Runtime.InteropServices.RuntimeInformation]::ProcessArchitecture) {
  "X64" { "amd64" }
  "Arm64" { "arm64" }
  default { throw "unsupported architecture: $_" }
}
$Zip = Join-Path $env:TEMP "asm.zip"
$Extract = Join-Path $env:TEMP "asm-release"
$InstallDir = Join-Path $env:LOCALAPPDATA "Programs\asm"
Invoke-WebRequest -Uri "https://github.com/hxy91819/agent-session-manager/releases/download/$Version/asm_${Version}_windows_${Arch}.zip" -OutFile $Zip
Expand-Archive -Path $Zip -DestinationPath $Extract -Force
New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
Copy-Item -Force (Join-Path $Extract "asm_${Version}_windows_${Arch}\asm.exe") (Join-Path $InstallDir "asm.exe")
$UserPath = [Environment]::GetEnvironmentVariable("Path", "User")
if (($UserPath -split ";") -notcontains $InstallDir) {
  $NewUserPath = if ([string]::IsNullOrWhiteSpace($UserPath)) { $InstallDir } else { "$UserPath;$InstallDir" }
  [Environment]::SetEnvironmentVariable("Path", $NewUserPath, "User")
  $env:Path = "$env:Path;$InstallDir"
}
```

Developers with Go installed can also install from source:

```sh
go install github.com/hxy91819/agent-session-manager/cmd/asm@latest
```

Useful non-interactive checks:

```sh
go run ./cmd/asm --json --query openclaw
go run ./cmd/asm --resume <session-id> --print-exec
go run ./cmd/asm resume --provider codex <session-id>
go run ./cmd/asm --claude-home /tmp/fake-claude --json
go run ./cmd/asm --kimi-home /tmp/fake-kimi --json
go run ./cmd/asm --kiro-home /tmp/fake-kiro --json
go run ./cmd/asm --opencode-home /tmp/fake-opencode --json
go run ./cmd/asm --zcode-home /tmp/fake-zcode --json
go run ./cmd/asm report --period yesterday
go run ./cmd/asm report --period today
go run ./cmd/asm report --period last-week --query openclaw
go run ./cmd/asm skills install agent-work-report
go run ./cmd/asm skills install tencent-meeting-summary
```

Developer checks:

```sh
pre-commit install
pre-commit run --all-files
go test ./...
go build ./cmd/asm
go run ./tools/check-provider-performance
go test -run '^$' -bench 'BenchmarkDiscover' -benchmem ./internal/provider/codex ./internal/provider/claude ./internal/provider/kiro ./internal/provider/opencode ./internal/provider/zcode
python3 -m unittest scripts/generate_release_changelog_test.py
```

The pre-commit setup expects `gitleaks` and `golangci-lint` to be installed.
It runs staged secret scanning, basic file hygiene checks, `gofmt`, `go vet`,
`go test`, and a small Go lint set.

Release preparation:

```sh
git switch master
git pull --ff-only
python3 scripts/generate-release-changelog.py \
  --version v0.8.0 \
  --target HEAD \
  --mode prepend \
  --output CHANGELOG.md
git add CHANGELOG.md
git commit -m "Prepare v0.8.0 changelog"
git tag v0.8.0
git push origin master v0.8.0
```

Generate and commit the changelog only after every intended feature and fix has
merged into `master`. The generator reads first-parent history, resolves each
merged PR's original GitHub author, and writes explicit `Thanks @author`
credit. The tag workflow verifies that committed section before publishing.

Performance controls:

```sh
go run ./cmd/asm --limit 1000 --since-days 30
```

`--limit` caps how many session files are parsed per provider after newest-first
ordering. Use `--codex-home`, `--claude-home`, `--kimi-home`, `--kiro-home`,
`--opencode-home`, or `--zcode-home` to point at alternate provider stores. By
default only sessions active in the last 30 days are shown.
`--since-days 0` disables the modification-time filter.
Automated and one-shot sessions are hidden when their provider exposes a
reliable non-interactive marker. Pass `--include-non-interactive` to include
them in JSON output or the TUI.

Discovery is isolated per provider. If one local provider store cannot be read,
JSON and report output keep sessions from healthy providers and add a
`provider_errors` array with the affected provider and error. The TUI shows the
same diagnostic in its status line. `asm resume --provider <name>` scans only
the selected provider; unqualified resume refuses to guess while any provider
could not be scanned.

macOS Gatekeeper:

Release binaries are not Apple Developer ID signed or notarized yet. If macOS
shows "Apple could not verify `asm` is free of malware", remove the quarantine
attribute after you have verified the release checksum:

```sh
grep 'asm_v0.5.0_darwin_arm64.tar.gz' sha256sums.txt
shasum -a 256 asm_v0.5.0_darwin_arm64.tar.gz
tar -xzf asm_v0.5.0_darwin_arm64.tar.gz
xattr -dr com.apple.quarantine asm_v0.5.0_darwin_arm64/asm
```

The long-term fix is to sign and notarize the Darwin release artifacts with an
Apple Developer ID certificate. Until then, building locally with
`go install github.com/hxy91819/agent-session-manager/cmd/asm@latest` also
avoids the browser-download quarantine path.

Direct resume:

```sh
go run ./cmd/asm resume --provider codex <session-id>
go run ./cmd/asm resume --provider claude <session-id> --print-exec
go run ./cmd/asm resume --provider kiro <session-id> --print-exec
```

The provider flag disambiguates session IDs across agent providers. Report JSON
includes a `resume_command` for each session so agents can surface copyable
commands in follow-up sections.

Skill install:

```sh
go run ./cmd/asm skills install agent-work-report
go run ./cmd/asm skills install tencent-meeting-mcp
go run ./cmd/asm skills install tencent-meeting-summary
go run ./cmd/asm skills install --all
go run ./cmd/asm skills install agent-work-report --scope current --target agents
go run ./cmd/asm skills install hxy91819/agent-session-manager --path skills/agent-work-report --scope current --target agents
go run ./cmd/asm skills install hxy91819/agent-session-manager --path skills --all --scope both --target both
```

By default, `asm skills install` downloads the standalone skills bundle from
the latest `agent-session-manager` GitHub Release. When `--scope` or `--target`
is omitted, `asm` prompts for current directory vs user directory and `.agents`
vs `.claude`. Use `--yes` for defaults (`current` + `.agents`) in scripts.

Example workflow:

```sh
asm skills install agent-work-report --scope current --target agents --yes
```

After installing the bundled `agent-work-report` skill, ask your coding agent
for "生成上周 Agent 工作周报" or "总结昨天的工作". The skill calls
`asm report --period last-week` or `asm report --period yesterday`, classifies
the session previews by project and topic, and returns a Chinese work report
with a project-oriented morning-standup overview, follow-ups, and risks. Every
overview item is labeled `[高投入]`, `[中投入]`, or `[低投入]` using relative
signals from meeting duration, session timing, and evidence content; the labels
are estimates rather than measured working hours.

For meeting-enriched reports, install `tencent-meeting-mcp` and the lightweight
`tencent-meeting-summary` skill, then export `TENCENT_MEETING_TOKEN`. The
summary skill lists ended meetings for the report window and reads available
Tencent Meeting smart minutes without downloading or reprocessing full
transcripts. Missing minutes may contribute only a clearly labeled,
title-inferred broad topic; they are never treated as proof of completed work.

The tracked nightly report entrypoint is:

```sh
cp .env.example .env
bash scripts/daily-agent-report.sh --dry-run
```

It loads the latest bundled skills from this checkout, keeps `asm report` as
the coding-work evidence source, and adds meeting context when the ignored
root `.env` provides `TENCENT_MEETING_TOKEN`. Generated output is validated
before delivery so an overview without effort levels, or one using effort
percentages, is retried instead of being sent.

Generation and delivery are separate executable adapters. The bundled
[`codebuddy.sh`](scripts/report-generators/codebuddy.sh) adapter receives
`--prompt <path>` and writes Markdown to stdout. The orchestrator also exports
`REPORT_MODEL`, `REPORT_MAX_TURNS`, and `REPORT_CODEBUDDY_BIN` for generators
that need them. The bundled
[`telegram.sh`](scripts/report-deliveries/telegram.sh) adapter receives
`--report <path>`; delivery adapters also receive `REPORT_DELIVERY_CONFIG` and
`REPORT_TITLE`.

Replace either side without changing report collection, validation, or retry
behavior:

```sh
bash scripts/daily-agent-report.sh \
  --generator-script ./scripts/my-report-generator \
  --delivery-script ./scripts/my-tencent-doc-delivery
```

A replacement generator must accept `--prompt <path>`, write only the Markdown
report to stdout, and use stderr for diagnostics. A replacement delivery
adapter must accept `--report <path>` and return non-zero when delivery fails.
Copy the bundled adapters as starting points for another model or a Tencent
Docs API integration. Keep all service credentials in the ignored `.env`,
user-local configuration, or a secret manager; `.env.example` intentionally
contains no values.

Agent report export:

```sh
go run ./cmd/asm report --period yesterday
go run ./cmd/asm report --period today
go run ./cmd/asm report --period last-week
go run ./cmd/asm report --start "2026-06-17" --end "2026-06-18"
go run ./cmd/asm report --start "2026-06-17 09:00" --end "2026-06-17 18:30"
go run ./cmd/asm report --period yesterday --preview-messages-per-edge 4 --preview-max-chars 1000
go run ./cmd/asm report --period yesterday --preview-messages-per-edge 2 --preview-edge-offset 2
```

`asm report` prints JSON for agent consumption. It uses local-time natural
windows and includes bounded user-message previews only for the report path.
Report discovery scans every session file modified since the start of the
requested window before applying `--limit`, so newer activity after a historical
window cannot displace matching sessions. For reports, `--limit` caps matching
sessions per provider after in-window evidence selection; `0` includes all.
When this result limit omits matching sessions, the provider's `coverage` entry
sets `truncated: true` and reports `matched_sessions` and `included_sessions`.
Detected non-interactive generator sessions are excluded by default so report
automation does not count its own work; use `--include-non-interactive` when
those sessions are intentionally part of the report.
`today` covers local midnight through the command's current time.
Use `--start` and `--end` for custom windows; accepted formats are
`YYYY-MM-DD`, local `YYYY-MM-DD HH:MM[:SS]`, and RFC3339. Custom report
windows are half-open (`start <= item < end`), so `--end 2026-06-18` excludes
events at local midnight on June 18.
For report writing, `sessions[].evidence` is the authoritative in-window work
evidence. The main `sessions`, `projects`, and their totals contain only sessions
with timestamped user-message evidence inside the requested half-open window.
Session titles are omitted from report output because a long-lived session can
have a title from another day. Session records updated within the window without
a user-authored message whose original timestamp falls in the window are placed
in `unverified_sessions`. Each item includes a `reason_code` and
`may_hide_user_work`. This is primarily a transcript-activity diagnostic, not a
count of missing work: only `may_hide_user_work: true` means a known provider
limitation could conceal in-window prompts.
Codex subagent threads remain discoverable and resumable, but reports exclude
them because their rollout files inherit the parent thread's history and would
otherwise duplicate the parent's work evidence.

`coverage` describes known provider limitations. Kimi is currently marked
`partial` because its state exposes only the latest prompt, and OpenClaw is
marked `unavailable` until its transcript is parsed. opencode messages without
an original message timestamp are excluded from evidence rather than dated by
filesystem mtime.
If the default previews are not enough for a reliable summary, increase
`--preview-messages-per-edge` or `--preview-max-chars` and rerun the report.
For incremental context loading, keep `--preview-messages-per-edge` fixed and
increase `--preview-edge-offset` to fetch the next layer from both ends.
Oversized user records from the transcript-backed Codex, Claude, CodeBuddy, and
Cursor providers are recovered as bounded head/tail previews with their
original timestamp. Oversized known assistant/tool outputs are drained without
reducing user-evidence coverage; an explicit partial-coverage warning remains
only when a record may contain user evidence that cannot be identified safely.

TUI keys:

- `enter`: resume selected session
- `left` / `right`: switch projects
- `up` / `down`: switch sessions
- `pgup` / `pgdown`: switch session pages
- `home` / `end`: jump to first or last session in the project
- `/`: search sessions
- `s`: cycle sort mode
- `m`: load 30 more days of history
- `q`: quit

Sessions or project counts marked with `!` have a missing or unavailable cwd and
cannot be resumed until the path exists again.

## Design Notes

Session discovery parses provider stores directly instead of asking provider
CLIs to list sessions. See
[`docs/session-discovery-design.md`](docs/session-discovery-design.md) for the
provider discovery, parsing, concurrency, and cache model.
