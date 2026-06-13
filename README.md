# session-manager

A local TUI for browsing and resuming coding-agent sessions.

First provider: Codex CLI sessions stored under `$CODEX_HOME/sessions`.

## Usage

```sh
go run ./cmd/session-manager
```

Useful non-interactive checks:

```sh
go run ./cmd/session-manager --json --query openclaw
go run ./cmd/session-manager --resume <session-id> --print-exec
```

Performance controls:

```sh
go run ./cmd/session-manager --limit 1000 --since-days 45
```

`--limit` caps how many session files are parsed per provider after newest-first
ordering. By default only sessions active in the last 45 days are shown.
`--since-days 0` disables the modification-time filter.

TUI keys:

- `enter`: resume selected session
- `/`: search sessions
- `s`: cycle sort mode
- `m`: load 45 more days of history
- `q`: quit
