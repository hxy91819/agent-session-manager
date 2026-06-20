# asm 多 Provider 与额外扫描路径方案

## Summary

- 新增公开 provider：`codebuddy`、`cursor`、`openclaw`，默认扫描标准本地目录；目录不存在时静默返回空列表。
- 不新增、不展示 `claude-internal` / `codex-internal` 这类 provider 名称；内部封装版本通过 generic 环境变量并入现有 `claude` / `codex` provider。
- OpenClaw v1 仅索引，不支持 resume；执行 resume 时返回明确错误，不伪造不可验证的恢复命令。

## Key Changes

- Codex/Claude 额外路径只走环境变量：
  - `ASM_CODEX_EXTRA_HOMES`: path-list，追加扫描 Codex home，支持 `$home/sessions`、`session_index.jsonl`、`history.jsonl`。
  - `ASM_CLAUDE_EXTRA_HOMES`: path-list，追加扫描 Claude home，支持 `$home/projects`。
  - 使用 `filepath.SplitList`，Linux/macOS 用 `:`，Windows 用 `;`。
  - 显式 `--codex-home` / `--claude-home` 保持测试隔离语义：只扫描 flag 指定 home，不自动追加 extra homes。
  - 正常无 flag 时：默认 home + extra homes。所有条目的 `Provider` 仍为 `codex` 或 `claude`。
- 多 home 去重：
  - 同一 provider 内相同 session ID 只保留 newest file。
  - metadata 增加 generic 字段，如 `source_home`、`title_source`；不引入任何 `internal` 命名。
- `codebuddy` provider：
  - 默认 home：`CODEBUDDY_HOME`，否则 `~/.codebuddy`。
  - 扫描 `$home/projects/**/*.jsonl`。
  - ID 用 `sessionId`，CWD 用 record `cwd`，标题优先 `ai-title`，再 `summary`，再最后真实 user message。
  - resume：`codebuddy --resume <session-id>`，从 session CWD 执行。
- `cursor` provider：
  - 默认 home：`CURSOR_HOME`，否则 `~/.cursor`。
  - 扫描 `$home/projects/*/agent-transcripts/<chat-id>/<chat-id>.jsonl`，跳过 `subagents`。
  - ID 用 `<chat-id>`；标题从第一条/最后一条 user message 提取；更新时间用 transcript file mtime。
  - CWD 从 project 目录名按现有 Cursor 编码反推并 `os.Stat` 校验；失败时标记 `cwd_missing=true` 并禁止 resume。
  - resume：`cursor-agent --resume <chat-id>`，从可用 CWD 执行。
- `openclaw` provider：
  - 默认 state dir：`OPENCLAW_STATE_DIR`，否则 `OPENCLAW_HOME/.openclaw`，否则 `~/.openclaw`。
  - 扫描 `$state/agents/*/sessions/sessions.json`。
  - ID 用 session key，例如 `agent:main:main`；metadata 保存 `native_session_id`、`agent_id`、`session_file`、`kind`。
  - CWD 优先 `spawnedCwd`、`spawnedWorkspaceDir`、agent workspace；都没有则用 state dir 并标记 `cwd_missing=true`。
  - resume 明确 unsupported；`asm resume --provider openclaw <id>` 返回 “OpenClaw resume is not supported by asm yet”。

## Interface/API Notes

- CLI 增加 provider home flags：`--codebuddy-home`、`--cursor-home`、`--openclaw-home`，并同步到主命令、`resume`、`report`。
- `session.ExecSpec` 增加 `UnsupportedReason string`；`resumeSession` 在运行前检查，避免空命令或误执行。
- `report` 输出中 unsupported provider 不填 `resume_command`，避免生成不可用恢复命令。
- UI/JSON provider 标签保持公开名称：`codex`、`claude`、`kimi`、`opencode`、`codebuddy`、`cursor`、`openclaw`。

## Test Plan

- Codex/Claude：
  - 单测覆盖 extra homes 合并、flag 覆盖 extra env、同 ID newest 去重。
  - e2e 清理 `ASM_CODEX_EXTRA_HOMES` / `ASM_CLAUDE_EXTRA_HOMES`，避免本机环境污染。
- CodeBuddy：
  - fake `$CODEBUDDY_HOME/projects/<cwd>/<id>.jsonl`，验证 ID/CWD/title/model/mtime/resume command。
  - 覆盖 `ai-title`、summary、last user fallback。
- Cursor：
  - fake `agent-transcripts/<id>/<id>.jsonl`，验证标题提取、跳过 subagents、CWD 缺失标记、resume command。
- OpenClaw：
  - fake `agents/<agent>/sessions/sessions.json`，验证 session key ID、metadata、updatedAt epoch-ms、unsupported resume。
- 全局：
  - 更新 CLI e2e，确保新增 provider 不污染既有测试。
  - 跑 `gofmt -w cmd internal tests`、`go run ./tools/check-provider-performance`、`golangci-lint run ./...`、`go test ./...`、`go build ./cmd/asm`。

## Assumptions

- 你选择了：额外 Codex/Claude 路径只用环境变量；新增公开 provider 默认扫描标准目录；OpenClaw v1 仅索引不恢复。
- 不做路径脱敏：当前 asm JSON/TUI 已暴露本地 session `Path`，本方案只保证 provider/flag/env/metadata 命名不出现公司内部 provider 名称。

## 验收标准

- 验证所有功能实现，可用
- 通过 autoreview skills，结合当前方案，避免扩大范围。

## 过程记录

- 执行过程中，如果有做重要决策，需要记录下来（问题、决策、原因）。
- autoreview过程中，每一轮 review 出来的问题，你的取舍，原因，需要记录下来。
