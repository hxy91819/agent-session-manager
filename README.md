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
- opencode sessions stored under `$OPENCODE_HOME/storage` or
  `~/.local/share/opencode/storage`. Resume runs from the original session cwd
  with `opencode -s <session-id>`.

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
go run ./cmd/asm --opencode-home /tmp/fake-opencode --json
go run ./cmd/asm report --period yesterday
go run ./cmd/asm report --period today
go run ./cmd/asm report --period last-week --query openclaw
go run ./cmd/asm skills install agent-work-report
```

Developer checks:

```sh
pre-commit install
pre-commit run --all-files
go test ./...
go build ./cmd/asm
go run ./tools/check-provider-performance
go test -run '^$' -bench 'BenchmarkDiscover' -benchmem ./internal/provider/codex ./internal/provider/claude ./internal/provider/opencode
```

The pre-commit setup expects `gitleaks` and `golangci-lint` to be installed.
It runs staged secret scanning, basic file hygiene checks, `gofmt`, `go vet`,
`go test`, and a small Go lint set.

Performance controls:

```sh
go run ./cmd/asm --limit 1000 --since-days 30
```

`--limit` caps how many session files are parsed per provider after newest-first
ordering. Use `--codex-home`, `--claude-home`, `--kimi-home`, or
`--opencode-home` to point at alternate provider stores. By default only
sessions active in the last 30 days are shown.
`--since-days 0` disables the modification-time filter.

Direct resume:

```sh
go run ./cmd/asm resume --provider codex <session-id>
go run ./cmd/asm resume --provider claude <session-id> --print-exec
```

The provider flag disambiguates session IDs across agent providers. Report JSON
includes a `resume_command` for each session so agents can surface copyable
commands in follow-up sections.

Skill install:

```sh
go run ./cmd/asm skills install agent-work-report
go run ./cmd/asm skills install --all
go run ./cmd/asm skills install agent-work-report --scope current --target agents
go run ./cmd/asm skills install hxy91819/agent-session-manager --path skills/agent-work-report --scope current --target agents
go run ./cmd/asm skills install hxy91819/agent-session-manager --path skills --all --scope both --target both
```

By default, `asm skills install` downloads the standalone skills bundle from
the latest `agent-session-manager` GitHub Release. When `--scope` or `--target`
is omitted, `asm` prompts for current directory vs user directory and `.agents`
vs `.claude`. Use `--yes` for defaults (`current` + `.agents`) in scripts.

Agent report export:

```sh
go run ./cmd/asm report --period yesterday
go run ./cmd/asm report --period today
go run ./cmd/asm report --period last-week
go run ./cmd/asm report --period yesterday --preview-messages-per-edge 4 --preview-max-chars 1000
go run ./cmd/asm report --period yesterday --preview-messages-per-edge 2 --preview-edge-offset 2
```

`asm report` prints JSON for agent consumption. It uses local-time natural
windows and includes bounded user-message previews only for the report path.
`today` covers local midnight through the command's current time.
If the default previews are not enough for a reliable summary, increase
`--preview-messages-per-edge` or `--preview-max-chars` and rerun the report.
For incremental context loading, keep `--preview-messages-per-edge` fixed and
increase `--preview-edge-offset` to fetch the next layer from both ends.

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
