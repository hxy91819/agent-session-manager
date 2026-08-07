# TUI 启动性能遗留工作建议

## 当前结论

阶段 E-H 已达到既定目标：超长 title、历史 cache 和大型 Codex rollout 的主要启动
瓶颈均已消除，常规 CLI 与未参与 provider 没有 5% 以上回退。因此没有阻塞本次
合并的遗留问题；后续应由新增 benchmark 或真实 p95 数据驱动，不继续扩大当前 PR。

## 建议尽快做

### 1. 补齐 CodeBuddy 与 Cursor 的 changed-large benchmark

两者与 Claude、Codex 一样读取 appendable JSONL，文件 identity 改变后会重新解析
primary 文件，但目前只有 history-heavy benchmark，无法判断大型活跃 transcript
是否构成真实启动瓶颈。

建议分别增加 provider-owned benchmark：

- 构造符合 producer schema 的大型 assistant/tool record；
- cold discovery 建 cache，每轮只追加一条小 user record；
- 记录 wall time、B/op、allocs/op，并验证 session title、cwd、metadata 不变；
- 先采集 10 样本，不预先引入共享 incremental parser。

只有 changed-large 相对稳定 hot cache 仍是显著主导成本时，才为对应 provider 设计
独立增量状态和回退规则。建议拆成两个小 PR，避免不同格式互相约束。

### 2. 单独评估 Claude append-only 增量解析

Claude 已有 16 MiB changed-large benchmark，最终仍约 71 ms；本次没有实现，是因为
阶段 H 只验证 Codex 路径，且 Claude 的 content-block、summary、parent/sidechain 和
preview 规则不同，直接复用 Codex 状态会放大正确性风险。

建议另开方案，至少先锁定：

- append tail、truncate、replace、prefix mismatch 和 partial JSONL；
- summary/title 与最后真实 user message 的优先级；
- sidechain/agent transcript 边界；
- report evidence 与 oversized record 恢复；
- public CLI cold/warm/append parity。

验收可沿用 Codex 的 changed-large 降低至少 70%、普通 cold/hot 无 5% 回退，但实现
必须留在 `internal/provider/claude`，除非两个 provider 的持久化状态契约被证明一致。

## 有触发条件再做

### 3. 加强 Codex append-only 身份校验

当前通过文件增长、旧前缀首尾各 64 KiB 指纹和完整 record 边界验证快路径。这符合
Codex rollout 的 append-only producer 契约，并能覆盖 truncate、普通 replace、
prefix rewrite 和 append-boundary rewrite。

理论上，如果同一文件在中部原地改写，同时保持旧 size 和首尾指纹，再追加内容，
快路径无法识别。只有出现 producer 行为证据或真实错误时，才考虑持久化 inode/file
ID、增加稀疏采样或使用文件系统变更标识；不要恢复完整前缀哈希，因为实测只能把
16 MiB internal 场景降到约 13 ms，未达到 70% 目标。

### 4. 建立跨版本启动性能趋势

当前 benchmark 适合本机 before/after 判定，不适合直接用绝对毫秒作为跨机器 CI
门槛。若后续再次修改 cache/provider 热路径，可在固定 runner 上保存 benchstat
趋势，并优先监控：

- `WarmHistoryHeavy`；
- `ChangedLargeCodexSession`；
- provider changed-large/history-heavy；
- `SingleEntryUpdateSave` 与 cache bytes；
- fixture session 数。

只有 runner 噪声和维护成本可控时才设置阻断门槛；fixture correctness 必须继续在
计时外验证。

### 5. 评估异步 TUI 首屏

fixture 中常规启动约 5-18 ms，Codex 16 MiB append 约 6.5 ms；但真实环境安装验证
中，优化后的整体热启动中位数仍约 2.71 s。下一步应先增加 provider/file-discovery
分解计时或 benchmark，定位 native store 枚举、文件筛选和解析的占比，避免根据端到端
总时间猜测瓶颈。只有分解数据和真实 pre-TUI p95 证明异步加载确有必要时，才单独设计
loading state、selection stability、provider error 展示和 PTY/public-boundary 测试。
不要把异步首屏混入当前 provider/cache PR。

## 目前不建议做

- 不把 Codex parser state 抽象成通用 JSONL incremental parser；格式边界尚不一致。
- 不为 Kimi、Kiro、opencode、OpenClaw、ZCode 添加虚假增量状态；其 primary store
  是 compact index/state、小 metadata/消息文件或 SQLite。
- 不降低 title 截断上限、隐藏历史 session 或减少 report evidence 来继续换取性能。
- 不删除 legacy cache；当前只读兼容和失败回退仍是安全迁移边界。

## 建议的后续 PR 顺序

1. `perf: benchmark changed CodeBuddy and Cursor transcripts`；
2. `perf: parse appended Claude transcripts incrementally`（仅在独立方案和行为测试完成后）；
3. 根据真实数据决定是否处理 Codex 更强身份校验或异步 TUI。

每个后续 PR 都应独立报告 public contract、base/fixed 行为证据、provider 影响矩阵、
10 样本 before/after，以及未处理 sibling provider 的明确结论。
