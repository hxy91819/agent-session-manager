# AGENTS.md

This repository contains `asm`, a local TUI for finding,
inspecting, and resuming coding-agent sessions across projects. Keep changes
small, testable, and compatible with future providers.

## Project Shape

- Entry point: `cmd/asm/main.go`
- Provider implementations: `internal/provider/<name>/`
- Shared session model and provider contract: `internal/session/session.go`
- Filtering, sorting, and project grouping: `internal/index/`
- Resume command execution: `internal/launcher/`
- Bubble Tea TUI: `internal/ui/`
- CLI-level integration tests: `tests/`

Current providers:

- Codex scans `$CODEX_HOME/sessions` or `~/.codex/sessions`, reads native
  titles from `session_index.jsonl`, and falls back to `history.jsonl` and
  rollout user messages.
- Claude Code scans `$CLAUDE_HOME/projects` or `~/.claude/projects`, and resumes
  with `claude --resume <session-id>` from the original cwd.
- Kimi Code scans `$KIMI_CODE_HOME` / `$KIMI_HOME` or `~/.kimi-code`, using
  `session_index.jsonl` plus per-session `state.json`, and resumes with
  `kimi --session <session-id>` from the original cwd.
- opencode scans `$OPENCODE_HOME/storage` or
  `~/.local/share/opencode/storage`, using session JSON plus project and message
  fallback files, and resumes with `opencode -s <session-id>` from the original
  cwd.

## Development Commands

Run these before finishing changes:

```sh
gofmt -w cmd internal tests
go run ./tools/check-provider-performance
golangci-lint run ./...
go test ./...
go build ./cmd/asm
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
go run ./cmd/asm --json --query openclaw
go run ./cmd/asm --resume <session-id> --print-exec
go run ./cmd/asm --since-days 0 --json
go run ./cmd/asm --json --query kimi
go run ./cmd/asm --json --query opencode
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
- Prefer `internal/cwdstatus` so repeated cwd checks are deduplicated within one
  discovery pass.
- Providers that repeatedly parse per-session files should use
  `internal/sessioncache` with path, size, and mtime identity. Cache only stable
  primary session-file parse results, and reapply dynamic side inputs such as
  title indexes, project worktree files, message fallback files, and cwd status
  after every cache hit.
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
- Keep the left project list explicitly cropped and show a range when projects
  overflow the panel.
- Search filters sessions first. The left project list should show only projects
  that still have matching sessions, and the right session panel should refresh
  when switching projects while search is active.
- Do not make search a project-only filter; titles, provider names, ids, paths,
  and metadata must remain searchable through `internal/index`.
- Print a short startup notice before executing a real resume command. Slow
  agent startup otherwise looks like a hung TUI. Do not print the notice for
  `--json` or `--print-exec`.
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

For a provider such as another coding agent:

1. Create `internal/provider/<name>/`.
2. Keep all provider-specific file parsing, title extraction, and cwd discovery
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
6. Add provider benchmarks for cold and hot discovery.
7. Make `go run ./tools/check-provider-performance` pass. Use
   `internal/sessioncache` for repeated per-session parsing, or add a
   `sessioncache: not required - <reason>` comment for explicitly lightweight
   stores.
8. Add CLI or index tests only when the shared behavior changes.
9. Register the provider in `cmd/asm/main.go`, add a `--<name>-home`
   flag for test isolation, and update CLI e2e tests so real local stores do
   not pollute test results.

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

## Claude Provider Notes

- Treat `~/.claude/projects/<encoded-cwd>/<session-id>.jsonl` as the primary
  store.
- Parse JSONL records defensively: message content may be plain strings or
  content-block arrays.
- Prefer summary/title records when present, otherwise use the last real user
  message as a fallback title.

## Kimi Provider Notes

- Treat `~/.kimi-code` as the supported Kimi Code store. Do not scan legacy
  `~/.kimi` unless that is added as an explicit compatibility feature.
- `session_index.jsonl` is the source of truth for `sessionId`, `sessionDir`,
  and `workDir`; per-session `state.json` is the source for title, last prompt,
  and timestamps.
- Keep Kimi resume as `kimi --session <session-id>`. Do not add `-y` or change
  permission mode by default.

## opencode Provider Notes

- Treat `$OPENCODE_HOME/storage` or `~/.local/share/opencode/storage` as the
  supported store.
- Session JSON is the primary per-session file and should be cached with
  `internal/sessioncache`.
- Project worktree fallback and message title fallback are dynamic side inputs;
  reapply them after cache hits instead of storing their derived values as the
  cached primary parse result.
- Keep opencode resume as `opencode -s <session-id>` from the original cwd.

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

## Release Guidance

- Releases are handled by `.github/workflows/release.yml`.
- Push a semantic version tag such as `v0.1.0` to publish a GitHub Release.
- The release workflow runs `go test ./...`, builds `asm` for Linux, macOS, and
  Windows on amd64 and arm64, uploads archives, and writes `sha256sums.txt`.
- Run `actionlint .github/workflows/release.yml` after changing workflow files.
- The installed binary name is `asm`; keep release archive names aligned with
  that entrypoint.

## Git Hygiene

- Commit after coherent development stages.
- Do not revert unrelated user changes.
- Keep generated binaries and local artifacts out of commits unless explicitly
  requested.
- Keep `.gitignore` patterns anchored for root binaries, for example `/asm`,
  so directories such as `cmd/asm/` remain trackable.
- Prefer concise commit messages that describe the behavior change.
