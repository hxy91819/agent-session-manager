# AGENTS.md

This repository contains `agent-session-manager`, a local TUI for finding,
inspecting, and resuming coding-agent sessions across projects. Keep changes
small, testable, and compatible with future providers such as Claude Code.

## Project Shape

- Entry point: `cmd/session-manager/main.go`
- Provider implementations: `internal/provider/<name>/`
- Shared session model and provider contract: `internal/session/session.go`
- Filtering, sorting, and project grouping: `internal/index/`
- Resume command execution: `internal/launcher/`
- Bubble Tea TUI: `internal/ui/`
- CLI-level integration tests: `tests/`

Current provider: Codex. It scans `$CODEX_HOME/sessions` or
`~/.codex/sessions`, reads Codex native titles from
`$CODEX_HOME/session_index.jsonl`, and falls back to `history.jsonl` and rollout
user messages.

## Development Commands

Run these before finishing changes:

```sh
gofmt -w cmd internal tests
golangci-lint run ./...
go test ./...
go build ./cmd/session-manager
```

For full repository hygiene, run:

```sh
pre-commit run --all-files
```

The pre-commit config intentionally stays small: staged gitleaks scanning,
basic file checks, `gofmt`, `go vet`, `go test`, and the repository's
`.golangci.yml` core linter set.

Useful local checks:

```sh
go run ./cmd/session-manager --json --query openclaw
go run ./cmd/session-manager --resume <session-id> --print-exec
go run ./cmd/session-manager --since-days 0 --json
```

Do not rely only on manual TUI inspection. Add focused tests for provider
parsing, index behavior, launcher behavior, and UI model behavior.

## Design Rules

- Keep provider-specific storage formats inside `internal/provider/<name>/`.
- Keep cross-provider concepts in `internal/session`.
- Keep sorting/search/project grouping in `internal/index`, not in providers.
- Keep command execution in `internal/launcher`, not in the TUI.
- Keep Bubble Tea state transitions deterministic and covered by model tests.
- Prefer metadata flags for provider-specific details, for example
  `title_source`, `cwd_missing`, or `cwd_error`.
- Do not hide stale or missing-cwd sessions by default. Mark them clearly and
  prevent unsafe resume attempts.

## Performance Rules

- Default session discovery should stay cheap. The CLI default is the last
  `30` days, and TUI load-more adds `30` days at a time.
- Avoid scanning all history unless the user passes `--since-days 0`.
- Avoid recursive filesystem checks for project cwd status. A single `os.Stat`
  on each discovered session cwd is acceptable.
- Keep `--limit` effective after newest-first file ordering.
- Do not introduce SQLite or network dependencies casually. If a provider needs
  heavier indexing, make it optional and keep the default path lightweight.

## TUI Rules

- The UI must fit inside the configured viewport.
- Use `lipgloss.Width` for display-width calculations. Do not use `len` or rune
  count for visible width because Chinese text and other wide characters will
  wrap incorrectly.
- Truncate long titles, cwd paths, and file paths before rendering panels.
- Do not rely on `lipgloss.Height` or `Style.Height` to crop overflowing
  content. Crop content lines explicitly, then render.
- Keep the right session list paged. Each page should have a bounded number of
  visible sessions and show page/range status.
- Preserve keyboard ergonomics:
  - `left` / `right` or `h` / `l`: switch projects
  - `up` / `down` or `k` / `j`: switch sessions
  - `pgup` / `pgdown`: switch session pages
  - `home` / `end`: jump within current project
  - `/`: search
  - `s`: cycle sort
  - `m`: load more history
  - `enter`: resume when cwd is available
  - `q`: quit

## Adding A New Provider

Use the `session.Provider` interface:

```go
type Provider interface {
    Name() string
    Discover(DiscoverOptions) ([]Session, error)
    ResumeCommand(Session) ExecSpec
}
```

For a provider such as Claude Code:

1. Create `internal/provider/claude/`.
2. Keep all Claude-specific file parsing, title extraction, and cwd discovery
   inside that package.
3. Return normalized `session.Session` values with:
   - stable `ID`
   - provider name
   - original `CWD`
   - best available display `Title`
   - `CreatedAt`, `UpdatedAt`, and source `Path`
   - provider-specific details in `Metadata`
4. Implement `ResumeCommand` so resume happens from the original session cwd
   when that cwd still exists.
5. Add provider unit tests using temporary fake session stores.
6. Add CLI or index tests only when the shared behavior changes.

Do not make the UI understand provider-specific file formats. The UI should only
consume normalized sessions.

## Codex Provider Notes

- Prefer native Codex thread names from `session_index.jsonl`.
- `session_index.jsonl` is append-only; the latest non-empty name for an ID wins.
- `history.jsonl` is a fallback, not the preferred title source.
- Rollout user-message extraction is a last-resort title fallback.
- Filter injected contexts such as environment blocks, skills, approval
  transcripts, and agent-internal context before using rollout text as a title.
- `state_5.sqlite` can provide richer `threads.title`, `preview`, and
  `first_user_message`, but adding SQLite should be treated as a deliberate
  dependency decision.

## Testing Guidance

Add tests near the behavior being changed:

- Provider parsing and metadata: `internal/provider/<name>/*_test.go`
- Index search/sort/grouping: `internal/index/*_test.go`
- Resume command safety: `internal/launcher/*_test.go`
- TUI key handling, pagination, and layout constraints:
  `internal/ui/*_test.go`
- CLI flows: `tests/e2e_test.go`

For UI layout bugs, include tests that assert rendered width and height using
`lipgloss.Width` and `lipgloss.Height`.

## Git Hygiene

- Commit after coherent development stages.
- Do not revert unrelated user changes.
- Keep generated binaries and local artifacts out of commits unless explicitly
  requested.
- Prefer concise commit messages that describe the behavior change.
