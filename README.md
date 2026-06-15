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
go run ./cmd/asm --claude-home /tmp/fake-claude --json
go run ./cmd/asm --kimi-home /tmp/fake-kimi --json
go run ./cmd/asm --opencode-home /tmp/fake-opencode --json
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
