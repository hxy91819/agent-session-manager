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
- Repository-maintainer skills: `.agents/skills/`
- User-facing skills distributed with `asm`: `skills/`

Keep development and review workflows under `.agents/skills/`. Put a skill
under `skills/` only when it is part of the product's installed or released
user-facing surface.

Current providers:

- Codex scans `$CODEX_HOME/sessions` or `~/.codex/sessions`, reads native
  titles from `session_index.jsonl`, and falls back to `history.jsonl` and
  rollout user messages.
- Claude Code scans `$CLAUDE_HOME/projects` or `~/.claude/projects`, and resumes
  with `claude --resume <session-id>` from the original cwd.
- Kimi Code scans `$KIMI_CODE_HOME` / `$KIMI_HOME` or `~/.kimi-code`, using
  `session_index.jsonl` plus per-session `state.json`, and resumes with
  `kimi --session <session-id>` from the original cwd.
- Kiro CLI scans `$KIRO_HOME/sessions/cli` or `~/.kiro/sessions/cli`, using
  per-session JSON metadata plus companion JSONL `Prompt` records, and resumes
  with `kiro-cli chat --resume-id <session-id>` from the original cwd.
- opencode reads `$OPENCODE_HOME/opencode.db` or
  `~/.local/share/opencode/opencode.db`, the drizzle-managed SQLite store used
  since opencode v1.18. Pre-migration stores without `opencode.db` fall back
  to scanning `storage/session/**.json` plus project and message fallback
  files. Resume uses `opencode -s <session-id>` from the original cwd.
- ZCode scans `$ZCODE_HOME/cli/db/db.sqlite` or `~/.zcode/cli/db/db.sqlite`, a
  SQLite store. It reads the `session` table and falls back to the first user
  message (via the `message` and `part` tables) for titles. ZCode has no CLI or
  documented resume path, so resume is a future-compatible placeholder
  (`zcode --resume <session-id>`) and the provider is effectively discover-only.
- dsh (DeepSeek Harness) scans `$DSH_HOME/sessions` or `~/.dsh/sessions`. Each
  session is an append-only `session.jsonl.zstd` (Zstandard, or plaintext
  `session.jsonl` when a deployment disables compression) under
  `<project-dir>/<session-id>/`. The first record is the session header (id,
  cwd, `createdAt` in epoch milliseconds, `origin`, `agentPreset`); titles come
  from the latest `session/title` event, falling back to the first human
  `user/message`, and resume uses the documented
  `dsh --profile tui --resume <session-id>` form.

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
go run ./cmd/asm --json --query kiro
go run ./cmd/asm --json --query opencode
```

Do not rely only on manual TUI inspection. Add focused tests for provider
parsing, index behavior, launcher behavior, and UI model behavior.

## Design Rules

- When implementing, tuning, or evaluating repository code-health metrics, read
  `docs/agent-code-health-experiment.md` and preserve its staged, diff-aware
  experiment design.

- Keep provider-specific storage formats inside `internal/provider/<name>/`.
- Keep cross-provider concepts in `internal/session`.
- Keep sorting/search/project grouping in `internal/index`, not in providers.
- Keep command execution in `internal/launcher`, not in the TUI.
- Keep Bubble Tea state transitions deterministic and covered by model tests.
- Prefer metadata flags for provider-specific details, for example
  `title_source`, `cwd_missing`, or `cwd_error`.
- Do not hide stale or missing-cwd sessions by default. Mark them clearly and
  prevent unsafe resume attempts.
- Default-hidden classifications require a stable, producer-persisted
  discriminator whose semantics have been verified. A client name, prompt
  text, transcript shape, or other ambiguous heuristic is not enough; keep
  ambiguous sessions visible.

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
  - `n`: choose an agent for a new session in the current project
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

## Kiro CLI Provider Notes

- Treat `$KIRO_HOME/sessions/cli` or `~/.kiro/sessions/cli` as the supported
  Kiro CLI store. Do not scan Kiro IDE session directories.
- The per-session `<session-id>.json` file is the primary source for
  `session_id`, `cwd`, titles, RFC3339 timestamps, `session_created_reason`, and
  `parent_session_id`; the companion `<session-id>.jsonl` file contains
  timestamped `Prompt` records for title fallback and report evidence.
- Kiro prompt timestamps are Unix seconds in `data.meta.timestamp`. Ignore
  assistant and tool records when collecting user previews.
- Cache only the primary session JSON with `internal/sessioncache`; re-read
  prompt fallback, report previews, and cwd status on every discovery pass.
- Keep Kiro resume as `kiro-cli chat --resume-id <session-id>` from the original cwd.

## opencode Provider Notes

- Modern store: `$OPENCODE_HOME/opencode.db` or
  `~/.local/share/opencode/opencode.db` (opencode v1.18+). The one-time
  migration imports legacy JSON sessions into the DB, so when the DB exists it
  is authoritative — do not also scan `storage/session`, or migrated sessions
  surface twice with stale JSON shadows.
- DB sessions come from the `session` table: `id`, `directory` (cwd), `title`,
  `version`, `project_id`, `parent_id`, `time_created`, and `time_updated` are
  millisecond Unix epochs. Read read-only (`?mode=ro`) so concurrent opencode
  writes are safe, and use `modernc.org/sqlite` (pure Go) so the release build
  stays `CGO_ENABLED=0`. Skip archived sessions (`time_archived` set).
- Empty `directory` falls back to `project.worktree`. Empty titles and the
  auto-generated `New session - <timestamp>` placeholder fall back to the
  first user message text (title_source `first_input`) via the `message` and
  `part` tables; keep the placeholder visible when no user text exists.
- Child sessions (`parent_id` set) must record `parent_thread_id` metadata so
  reports can deduplicate delegated subagent work, mirroring zcode.
- `sessioncache` is not required for the DB path because discovery reads a
  single database with indexed queries; declare the exemption with a reason.
- Legacy pre-migration stores: scan `storage/session/**.json` as the primary
  per-session file, cached with `internal/sessioncache`. Project worktree
  fallback and message title fallback are dynamic side inputs; reapply them
  after cache hits instead of storing their derived values as the cached
  primary parse result.
- Keep opencode resume as `opencode -s <session-id>` from the original cwd.

## ZCode Provider Notes

- Treat `$ZCODE_HOME/cli/db/db.sqlite` or `~/.zcode/cli/db/db.sqlite` as the
  supported store. ZCode is an Electron desktop app, not a CLI.
- The `session` table is the primary source: `id`, `directory` (cwd), `title`,
  `title_source`, `time_created`, and `time_updated` are millisecond Unix
  epochs. Read the database read-only (`?mode=ro`) so concurrent app writes are
  safe.
- User message text lives in the `part` table (`data.type == "text"`,
  `data.text`) joined to `message` rows whose `data.role == "user"`. The first
  user message is the `first_input` title fallback, mirroring ZCode's own
  semantics.
- Prefer `session.title` when present; record the DB `title_source` in
  metadata. Skip archived sessions (`time_archived` set) since they are not
  active history.
- Use `modernc.org/sqlite` (pure Go) so the release build stays `CGO_ENABLED=0`.
  Do not introduce a CGO sqlite driver.
- `sessioncache` is not required because discovery reads a single SQLite
  database with indexed queries, not per-session files; declare the exemption
  with a reason.
- ZCode has no CLI or documented deep-link resume path. Keep
  `ResumeCommand` as the future-compatible `zcode --resume <session-id>` from
  the original cwd, and document that resume is effectively unsupported today.

## dsh Provider Notes

- Treat `$DSH_HOME/sessions` or `~/.dsh/sessions` as the supported store. Logs
  group under human-readable `<project-dir>` directories, then per-session
  `<session-id>` directories holding `session.jsonl.zstd` (default) or
  `session.jsonl` (compression `none`); prefer the zstd artifact when both
  exist.
- The first record is the session header: `id`, `createdAt` (millisecond Unix
  epoch), `cwd`, optional `parentSession`, `origin` (`subagent`),
  `delegationDepth`, and `agentPreset`. Reject any `version` other than `0`
  quietly; dsh itself refuses foreign format versions on load.
- Decode Zstandard logs frame-by-frame tolerantly: a crash-torn final frame
  drops only its tail, so keep the complete prefix and never reject the whole
  session. Timestamps come from event `time` (epoch milliseconds), with the
  file mtime as the UpdatedAt fallback.
- Titles come from the latest `session/title` event (user renames append
  later events, so the last valid one wins). Without a title event, use the
  first `user/message` whose `source.kind` is `user`; dsh tags injected
  context with a non-`user` source kind, so no text-shape heuristics are
  needed.
- Cache the primary log parse with `internal/sessioncache`; re-read report
  previews and reapply cwd status on every discovery pass.
- Keep dsh resume as the documented `dsh --profile tui --resume <session-id>`
  from the original cwd. asm cannot verify the deployment's tui profile is
  installed, so dsh is effectively discover-only when it is not.

## Testing Guidance

Repository maintenance skills:

- Use `.agents/skills/behavior-e2e-validation` for user-observable behavior
  changes and bug fixes. If a public-boundary reproduction is feasible, add or
  update an end-to-end test and prove the base failure and fixed pass.
- Use `.agents/skills/cross-agent-pr-review` when reviewing provider or
  shared-session PRs. Check whether the bug class affects other agents;
  contributors generally do not need to fix every provider, but maintainers
  must create follow-up PRs for confirmed sibling bugs with end-to-end coverage.

For every bug fix, first add or update a focused regression test that reproduces
the broken behavior, then change the implementation to make that test pass. Do
not treat a bug fix as complete without a regression test unless the behavior is
not testable in this repository; in that case, document why in the change.

Cache parity tests compare normalized sessions with
`internal/sessiontest.RequireEqual`. This assertion compares timestamps by
instant across location/offset changes while keeping every other session field
exact.

Every change to an external interface or other user-observable behavior must
add or update an end-to-end test that exercises the public boundary and locks in
the intended user behavior. This includes CLI contracts, output formats, user
flows, and integration adapter behavior. Assert the behavior contract rather
than internal implementation details. If an end-to-end test is not feasible in
this repository, document the reason and the alternative coverage in the
change.

CLI end-to-end tests must isolate every unrelated provider store. Use explicit
`--<provider>-home` flags for the stores under test and either flags or the
shared test runner's sanitized environment for the rest. Inspect the runner
before claiming that a test can read ambient developer data.

When a change hides sessions by default, add paired coverage: the intended
target is excluded and a plausible normal or ambiguous session remains visible.
Validate the discriminator against producer documentation, source, or
representative local records; report aggregate field values and counts rather
than exposing transcript content. A base failure counts only when the expected
product assertion fails, not when compilation, setup, or the test harness fails.

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
- Before tagging, run
  `python3 scripts/generate-release-changelog.py --version <tag> --target HEAD
  --mode prepend --output CHANGELOG.md`. Commit the generated section before
  creating the tag.
- The generator resolves merged PR titles and original authors through GitHub,
  adds `Thanks @author`, and includes credited direct commits. The release
  workflow rejects a tag when its committed changelog section differs from the
  generated first-parent history.
- Run `actionlint .github/workflows/release.yml` after changing workflow files.
- The installed binary name is `asm`; keep release archive names aligned with
  that entrypoint.

## Git Hygiene

- Commit after coherent development stages.
- Do not revert unrelated user changes.
- When the primary worktree already has unrelated changes, use a separate
  worktree from the intended base for PR work. Do not switch or reset the dirty
  checkout to make room.
- Before aligning a dirty checkout with its remote branch, inventory the local
  changes and create a recoverable snapshot branch or commit. Fast-forward the
  branch, then replay only changes that are still unique and intended.
- Track which worktrees existed before the task and which the task created.
  After merge, remove only task-created worktrees that have no unique content;
  never clean up pre-existing worktrees by assumption.
- Keep generated binaries and local artifacts out of commits unless explicitly
  requested.
- Keep `.gitignore` patterns anchored for root binaries, for example `/asm`,
  so directories such as `cmd/asm/` remain trackable.
- Prefer concise commit messages that describe the behavior change.
