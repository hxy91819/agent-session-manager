# Provider Expansion Process Record

## Decisions

- Extra Codex and Claude homes are environment-only (`ASM_CODEX_EXTRA_HOMES`, `ASM_CLAUDE_EXTRA_HOMES`). Explicit `--codex-home` and `--claude-home` keep test-isolation semantics and do not append extra homes.
- Codex and Claude multi-home discovery sorts all files newest-first before applying `--limit`, then de-duplicates by session ID. Codex title enrichment checks all configured homes so a newest rollout file in a partial mirror can still use native titles from another home.
- Cursor uses `worker.log` `workspacePath=` as the authoritative cwd source. The project directory key is lossy because `-` is both the separator encoding and a valid path character, so ambiguous fallback keys are indexed with empty `CWD` and `cwd_error` instead of guessed paths.
- OpenClaw is index-only. It only treats `spawnedCwd` as an original session cwd; workspace fields are metadata because they can describe mutable agent state rather than the session's original project. OpenClaw sessions include `resume_unsupported` and return `UnsupportedReason`.
- Sessions with unavailable cwd remain indexed with metadata (`cwd_missing`, `cwd_error`, or `resume_unsupported`). Report output and CLI resume suppress or reject resume commands for those sessions.

## Autoreview Findings

- Cursor project-key decoding was initially lossy for hyphenated paths. Fixed by preferring `worker.log` and marking lossy fallback keys unavailable rather than guessing.
- OpenClaw initially applied `--limit` after parsing all agent indexes. Fixed by limiting newest-first `sessions.json` files before parsing.
- Report output initially emitted resume commands for non-resumable Cursor sessions. Fixed by gating report commands on per-session resumability metadata.
- OpenClaw initially appeared selectable in the TUI despite unsupported resume. Fixed with `resume_unsupported` metadata and TUI unavailable-session handling.
- Cursor root-level keys, paths with spaces, and non-ENOENT stat errors needed precise handling. Fixed with leading-slash restoration, full `workspacePath` parsing, and `cwd_error` preservation.
- OpenClaw initially used workspace/state paths as cwd fallbacks. Fixed by leaving `CWD` empty when no session-level `spawnedCwd` exists.
- CodeBuddy initially dropped sessions with missing cwd. Fixed by indexing them with `cwd_missing=true`.
