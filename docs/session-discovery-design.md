# Session Discovery Design

This document records the core discovery model used by
`agent-session-manager`. The manager reads provider session stores directly; it
does not call Codex, Claude, or Kimi to list or parse sessions.

## Goals

- Keep startup cheap enough for an interactive TUI.
- Normalize different agent stores into the shared `session.Session` model.
- Keep provider-specific storage formats inside `internal/provider/<name>/`.
- Avoid hiding stale sessions by default; mark missing cwd state and block unsafe
  resume attempts.
- Preserve stable output ordering while allowing safe internal parallelism.

## Provider Discovery

The CLI registers all providers in `cmd/session-manager/main.go`:

- Codex: `internal/provider/codex`
- Claude Code: `internal/provider/claude`
- Kimi Code: `internal/provider/kimi`

`discoverAll` runs providers concurrently. Each provider receives the same
`session.DiscoverOptions`:

- `Since`: modification-time lower bound. The default CLI window is 30 days.
- `LimitFiles`: maximum files to scan per provider after newest-first ordering.

Provider results are merged in registration order after all providers finish, so
the output stays stable even though discovery is concurrent.

Within a provider, session files are currently processed serially. This is
intentional: provider-level concurrency removes the largest startup wait without
adding per-file scheduling, file descriptor pressure, or cache coordination
complexity.

## Direct Parsing

Each provider parses its own local storage format directly:

- Codex scans `$CODEX_HOME/sessions` or `~/.codex/sessions`, opens rollout
  `*.jsonl` files, and parses records such as `session_meta`, `turn_context`,
  and user `response_item` messages. It also reads `history.jsonl` and
  `session_index.jsonl` for title fallback and native thread names.
- Claude scans `$CLAUDE_HOME/projects` or `~/.claude/projects`, opens
  per-session `*.jsonl` files, and parses `sessionId`, `cwd`, native summary or
  title records, user messages, model, and branch metadata.
- Kimi reads `~/.kimi-code/session_index.jsonl` plus per-session `state.json`.

External provider commands are only used for resume:

- `codex resume <session-id>`
- `claude --resume <session-id>`
- `kimi --session <session-id>`

This keeps listing independent from provider CLI startup time and makes JSON
output and tests deterministic.

## File-Level Parse Cache

Codex and Claude JSONL parsing can dominate startup because stores may contain
large active histories. To avoid reparsing unchanged files, discovery uses a
file-level cache in `internal/sessioncache`.

Cache identity:

- provider name
- absolute session file path
- file size
- file modification time
- cache schema version

If all identity fields match, the cached parsed `session.Session` is reused. If
one session file changes, only that file's cache entry misses and only that file
is reparsed; other files still hit cache. The cache is not an incremental JSONL
tail parser. If a large active JSONL changes, that whole JSONL file is reparsed.

The persistent cache is a simple JSON file under the user cache directory:

- `agent-session-manager/codex-sessions.json`
- `agent-session-manager/claude-sessions.json`

The cache stores parse results only. Discovery still reapplies dynamic state on
each run:

- CWD availability is refreshed every run, including cache hits. Within one
  discovery pass, identical CWD paths share one `os.Stat` result to avoid
  repeating the same filesystem check for many sessions from the same project.
- Codex title overrides from `history.jsonl` and `session_index.jsonl` are
  applied every time, including cache hits.

Default 30-day discovery does not prune older cache entries because those files
were intentionally not scanned and may be needed by TUI load-more. Cache pruning
only runs when discovery is effectively unbounded: no `Since` filter and no
effective `LimitFiles` truncation.

## Performance Model

Startup work is layered:

1. Providers run concurrently.
2. Each provider scans its store and selects recent files.
3. Each selected file is checked against the file-level cache.
4. Cache hits avoid JSONL parsing.
5. Cache misses parse one full session file.
6. Dynamic metadata such as missing cwd state is refreshed, with CWD stat
   results deduplicated inside the discovery pass.

The hot path after cache warmup is mostly filesystem walking, file stat checks,
cache lookups, unique CWD checks, and final filtering/sorting.

Benchmarks live next to the providers:

- `internal/provider/codex/codex_benchmark_test.go`
- `internal/provider/claude/claude_benchmark_test.go`

Run them with:

```sh
go test -run '^$' -bench 'BenchmarkDiscover' -benchmem ./internal/provider/codex ./internal/provider/claude
```

Every new discovery optimization should add or update a benchmark that captures
the specific scenario being optimized, for example cold cache, hot cache,
single changed large file, or many sessions sharing one cwd. The CWD status
checker benchmark lives in `internal/cwdstatus`.

## Non-Goals

- Do not call provider CLIs to list sessions.
- Do not add SQLite or heavier indexing unless a later benchmark justifies it.
- Do not make the UI understand provider storage formats.
- Do not add file-level parsing concurrency unless benchmarks show cold or
  changed-file parsing remains a real bottleneck after caching.
- Do not implement append-only incremental JSONL parsing unless the additional
  complexity is justified by measured large-file changed-session costs.
