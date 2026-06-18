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
