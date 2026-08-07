# TUI 启动性能遗留工作建议

## 当前结论

阶段 E-H 已消除超长 title、历史 cache 和 changed-large Codex rollout 的专项瓶颈，
但 2026-08-07 对 `origin/master@8cc4af8` 的真实分解诊断确认，当前冷启动中位数仍为
31.265 秒，热启动中位数为 2.717 秒。两条关键路径都在 Codex primary parse，而不是
cache、cwd status 或 TUI 建模。

热路径首先是一个正确性问题：producer 已持久化对象形态的
`session_meta.source={"subagent": ...}`，当前解析器却要求字符串。257 个唯一 session
因此漏发现，158,313,074 bytes rollout 每次热启动重复解析约 2.61 秒。冷路径则顺序
解析约 1.96 GB rollout；只读单样本中，8 worker 将相同 parser 工作从 32.02 秒降至
10.27 秒。完整统计、provider/stage 占比、正确性哈希和 worker sweep 见
`docs/tui-startup-performance-baseline.md`。

## 建议尽快做

### 1. P0：兼容 Codex subagent source schema（已完成，待合并）

先补失败测试，再修改 `internal/provider/codex`。解析器必须同时接受历史字符串 source
和当前对象 source，不能因为一个可选字段形态变化而丢弃 `id`、`cwd`、timestamp、
parent relation 或其他 session metadata。需要先明确对象 source 如何映射到现有
`entrypoint`/`interaction_mode`，不得仅为性能把 subagent 默认隐藏。

最低覆盖：

- provider fixture：字符串 source、对象 `subagent` source、对象 source 且没有后续
  session meta、parent/child inherited history、普通交互和 `exec` 分类；
- cache cold/warm parity、cache version/invalidation、title/cwd/model/parent metadata；
- 公共 CLI JSON E2E：目标 subagent 被发现，普通 session 继续可见，provider/project
  聚合和 resume/report 语义符合预期；
- focused benchmark：修复前稳定复现重复 parse，修复后第二轮 cache hit；
- 同一时点真实 base/after：冷启动各至少 10 次，热启动各预热 2 次后至少 20 次。

该项同时修复 session 漏发现和热启动，预计可移除当前约 2.61 秒 Codex 重复 parse。

2026-08-07 已按统一门槛完成：测试提交 `cd804de` 在公共 CLI 上稳定复现 child 消失和
继承 parent 误归属，生产提交 `dd8642b` 修复后通过；真实同机 A/B 的冷启动为
`-26.53%`，热启动为 `-96.94%`。紧邻正确性快照新增 447 个目标 subagent、移除 0，
排除目标 schema 后 session/project 哈希均一致。完整测试、性能和 provider 影响证据见
性能基线文档“P0”一节。P1 必须等待 P0 合并后，以当时主线重新采集独立 base，不能
复用本项 after。

### 2. P1：Codex cache miss 有界并行解析（已完成）

P0 合并并重新建立真实基线后，再并行 primary cache miss。首个候选上限为 8 worker；
cache Get/GetLatest、最终 Put/Save、newest-first 输出、重复 ID 去重和 dynamic side input
应保持确定性。不要让多个 worker 直接并发修改当前非线程安全 cache。

最低覆盖：

- 先添加串行/并行结果等价测试，覆盖 newest-first、limit、重复 ID、解析失败、parent/
  child、incremental state 和 cache parity；
- `go test -race`，并记录峰值 RSS、B/op、allocs/op 和 1/2/4/8/16 worker sweep；
- provider benchmark 使用大小混合且符合 producer schema 的 rollout；
- 公共 CLI E2E 锁定 JSON、project grouping、missing-cwd 和 resume safety；
- 独立真实 base/after，不复用 P0 的 after 数字；冷 10、热 20、正确性哈希逐项一致。

单样本 8 worker parser 上限为 `-67.9%`，但只有公共 CLI p95 和内存风险同时可接受时
才能保留。

2026-08-07 已从 P0 合并后的 `origin/master@b6b2a23` 独立完成 P1：默认 8 worker
只并行 primary file parse，cache 读写、去重和 dynamic inputs 继续按原顺序串行处理。
真实冷启动 median/p95 从 23.246/23.620 秒降至 5.476/5.684 秒，benchstat 为
`-76.44%`；热启动无显著变化。base/after 的 1207 个 session、89 个 project、provider
计数、0 error 和两类不可逆哈希完全一致。真实峰值 RSS 从 49.9 MiB 增至 71.1 MiB；
16 worker 相对 8 worker 收益很小且 RSS 进一步升至约 112 MiB，因此保留 8。完整
focused base/after、worker sweep、race、正确性和 cross-agent 证据见性能基线文档 P1 节。
本项完成后停止，不在同一任务进入 P2。

### 3. P2：Codex metadata/turn-context 快路径（已完成）

只有 P1 后真实冷启动仍由 Codex 大文件解析主导时才进入。当前 293 个可见 Codex title
中，289 个来自 `session_index` 或 `history`，只有 4 个依赖 rollout；可评估避免深度
解码无关 assistant/tool payload，但仍必须获得正确的最新 cwd/model、parent boundary
和 rollout fallback。

产品边界决策（2026-08-07）：P2 只允许在 `DiscoverOptions.Preview` 未启用时使用
metadata/turn-context 快路径，以优化 TUI 启动和普通 discovery。`asm report` 启用 preview
时必须继续走现有完整 primary parse 和 user-preview evidence 路径；P2 不以加速周报为
目标，也不得改变周报 evidence、parent/child 去重或项目归组。实现前先用公共 report
E2E 锁定这些输出，并验证 base/after 的 evidence 与聚合哈希一致。

最低覆盖包括最新 turn context、session index/history 优先级、rollout fallback、
oversized/partial JSONL、truncate/replace、parent/child inherited history 和 report
preview。需要同时报告实际读取 bytes、B/op、allocs/op 以及完整真实 A/B；不能通过减少
report evidence、降低 title 上限或改变可见 session 集合换取性能。

2026-08-07 已从 P1 合并后的 `origin/master@07243f0` 独立完成 P2：测试提交
`f6c2f9d`，生产实现 `a433aab`。门槛诊断确认 Codex-only 冷启动中位数 5.490 秒，
约占完整冷启动 99.2%，并实际读取约 1.49 GB rollout。仅 Preview 未启用时，parser
根据 producer header 流式丢弃已明确为 assistant/tool 的 payload；header 不明确时保守
完整解码。report 遇到 metadata-only cache 会重做完整 primary parse，并继续原有
user-preview evidence 路径。

符合 producer schema 的大小混合 benchmark wall time `-94.70%`、B/op `-96.54%`；
真实冷启动中位数从 5.540 降至 3.951 秒（`-28.68%`），冷 p95 `-18.45%`，热态
交替 30 对无显著变化。1210 sessions、90 projects、provider counts、0 error 和两类
不可逆哈希一致；last-week report 的 311 条 evidence 哈希及聚合哈希完全一致。
完整资源、raw path、cache/RSS 取舍和 cross-agent matrix 见性能基线文档 P2 节。
本项保留并停止，不进入 P3。

### 4. P3：Codex dynamic title index 增量化

状态：待从 P2 合并提交 `4053965` 开始独立执行，工作分支为
`agent/tui-startup-p3`。P0 后 Codex 非 primary 热成本中，`session_index`/`history`
dynamic side input 约 36 ms；P2 后真实热启动中位数约 82 ms，因此 P3 值得重新验证，
但该历史估算不能代替本项 base。

先测试和测量，后实现。必须重新采集独立 P3 base，并分解普通 discovery 的热路径；
只有确认 `session_index.jsonl`/`history.jsonl` 的重复全量解析仍是剩余关键路径，且预期
收益相对实现与 cache 复杂度有意义时，才继续生产实现。若门槛不成立，停止实现并记录
分解、raw path 和否决证据。

实施范围只限 Codex dynamic title side inputs：持久化足以安全恢复 append-only 解析的
索引状态，热路径只消费已验证的新完整 records；不得改变 primary rollout cache、P2
metadata/turn-context 快路径、其他 provider 或 report evidence 路径。动态 title 仍须在
每次 discovery 后正确覆盖缓存的 primary 结果，并保持
`session_index > history > rollout`、最新非空 native name wins、可见 session、项目归组、
newest-first、limit、cwd/model、parent/child 去重和 resume safety。

正确性最低覆盖：cold/warm parity；两个 index 的 append 与 latest-wins；空 title；重复
ID；跨文件 title 优先级与 rollout fallback；文件缺失、创建、truncate、atomic replace、
prefix/boundary rewrite；partial、corrupt 和 oversized JSONL；旧 cache/version 兼容；
增量状态原子保存及写入失败后的安全回退；普通 discovery 连续运行中的动态输入变化；
公共 CLI JSON 与 report E2E。不得因索引状态损坏或身份不明确而使用可能过期的 title，
此时应保守重建。

性能验收使用符合 producer schema、包含大小和重复 ID 混合的 title indexes，报告实际
读取 bytes、sec/op、B/op、allocs/op 和峰值 RSS。同机独立真实 base/after 冷各至少
10 次，热各预热 2 次后至少 20 次；校验 session/project/provider 数量、provider error、
两类不可逆哈希，以及周报 evidence/聚合哈希。PR 描述必须写入测试命令、样本数、
base/after 数据、哈希和保留或否决结论，不能只链接本地 raw 文件。

按 coherent stages 提交测试、实现和文档；完成 focused tests、`go test -race ./...`、
provider performance contract、`golangci-lint`、`go test ./...`、build、pre-commit，使用
`behavior-e2e-validation`、`cross-agent-pr-review` 和 `autoreview`。将 raw 临时路径、
独立 base/after、cache/RSS 取舍及最终决策追加到性能基线和本文档。P3 不顺带实施其他
provider 优化或后续性能项。

### 每个优化项的统一执行门槛

每项必须独立完成以下闭环，不能把多个优化合并后只测一次：

1. 开始前阅读本文件、`docs/tui-startup-performance-baseline.md` 和 `AGENTS.md`；
2. 先提交可观察行为或性能回归测试，再实现；bug fix 使用
   `.agents/skills/behavior-e2e-validation`，provider/shared-session 变更完成后使用
   `.agents/skills/cross-agent-pr-review` 检查 sibling bug class；
3. 跑 focused tests、provider benchmark、`go test ./...`、provider performance contract、
   lint/build 和适用的 pre-commit；
4. 在同一时点对该项 base/after 运行公共 runner：冷各至少 10 次，热各预热 2 次后
   至少 20 次；报告 benchstat、median、mean、p95、cache bytes；
5. 验证 session/project/provider 数量、provider error 和两类不可逆哈希一致；若该项
   有意修复漏发现，必须记录预期集合差异并证明非目标 session 保持不变；
6. 将该项实现 commit、测试证据、raw output 临时路径、真实 A/B 和保留/否决决策追加
   到性能基线文档，再开始下一项。

## 有触发条件再做

### 加强 Codex append-only 身份校验

当前通过文件增长、旧前缀首尾各 64 KiB 指纹和完整 record 边界验证快路径。这符合
Codex rollout 的 append-only producer 契约，并能覆盖 truncate、普通 replace、
prefix rewrite 和 append-boundary rewrite。

理论上，如果同一文件在中部原地改写，同时保持旧 size 和首尾指纹，再追加内容，
快路径无法识别。只有出现 producer 行为证据或真实错误时，才考虑持久化 inode/file
ID、增加稀疏采样或使用文件系统变更标识；不要恢复完整前缀哈希，因为实测只能把
16 MiB internal 场景降到约 13 ms，未达到 70% 目标。

### 建立跨版本启动性能趋势

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

## 目前不建议做

- 暂不评估异步 TUI 首屏。异步加载会引入 loading state、selection stability、
  provider error 展示和 PTY/public-boundary 测试等额外复杂度，却不会减少 discovery
  本身的工作量。当前优先通过 provider/file-discovery 分解测量，寻找并实施合理的同步
  冷启动、热启动优化；只有这些空间基本穷尽后，真实 pre-TUI p95 仍明显影响交互，
  才重新评估异步方案。
- 不把 Codex parser state 抽象成通用 JSONL incremental parser；格式边界尚不一致。
- 不为 Kimi、Kiro、opencode、OpenClaw、ZCode 添加虚假增量状态；其 primary store
  是 compact index/state、小 metadata/消息文件或 SQLite。
- 不降低 title 截断上限、隐藏历史 session 或减少 report evidence 来继续换取性能。
- 不为当前解析失败的 257 个唯一 Codex session 添加负缓存或默认隐藏；先修 producer
  schema 兼容，避免把漏发现固化为性能优化。
- 暂不实现 Claude append-only：真实热 primary parse 约 6 ms、provider 总计约 21 ms；
  等 Codex 优化后真实关键路径切换到 Claude，或 changed-large 进入常见 workload 再做。
- CodeBuddy/Cursor changed-large benchmark 仍可作为 provider 防回退覆盖，但当前真实热
  provider 总计约 18/27 ms，不是下一项启动优化。
- 不删除 legacy cache；当前只读兼容和失败回退仍是安全迁移边界。

## 建议的后续 PR 顺序

1. `fix: parse Codex subagent session metadata`；
2. `perf: parse Codex cold cache misses concurrently`；
3. `perf: avoid deep Codex payload parsing`（仅在 P1 后真实数据仍达到门槛）；
4. `perf: parse Codex title indexes incrementally`（仅在 dynamic side input 成为热主导项）。

每个后续 PR 都应独立报告 public contract、base/fixed 行为证据、provider 影响矩阵、
冷启动至少 10 个 base/after、热启动至少 20 个 base/after，以及未处理 sibling provider
的明确结论。不得用上一项 after 代替下一项同一时点的 base。
