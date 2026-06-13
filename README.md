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
go run ./cmd/session-manager --limit 1000 --since-days 90
```

`--limit` caps how many session files are parsed per provider after newest-first
ordering. `--since-days 0` disables the modification-time filter.
