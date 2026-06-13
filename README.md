# session-manager

A local TUI for browsing and resuming coding-agent sessions.

First provider: Codex CLI sessions stored under `$CODEX_HOME/sessions`.
Titles prefer Codex's native `$CODEX_HOME/session_index.jsonl` thread names,
then fall back to `history.jsonl` and rollout user messages.

## Usage

```sh
go run ./cmd/session-manager
```

Useful non-interactive checks:

```sh
go run ./cmd/session-manager --json --query openclaw
go run ./cmd/session-manager --resume <session-id> --print-exec
```

Developer checks:

```sh
pre-commit install
pre-commit run --all-files
go test ./...
go build ./cmd/session-manager
```

The pre-commit setup expects `gitleaks` and `golangci-lint` to be installed.
It runs staged secret scanning, basic file hygiene checks, `gofmt`, `go vet`,
`go test`, and a small Go lint set.

Performance controls:

```sh
go run ./cmd/session-manager --limit 1000 --since-days 30
```

`--limit` caps how many session files are parsed per provider after newest-first
ordering. By default only sessions active in the last 30 days are shown.
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
