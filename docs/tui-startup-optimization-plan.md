# asm TUI 启动性能优化方案

## 0. 文档状态与执行入口

- 状态：阶段 A-H 已完成，最终实现与性能证据见第 10 节和
  `docs/tui-startup-performance-baseline.md`。
- 最后按代码核对日期：2026-08-07。
- 当前产物：persistent-cache E2E runner、行为与性能 benchmark、阶段 D baseline、
  阶段 E-H 生产优化和逐阶段 before/after 记录均已提交。
- 执行范围：阶段 A-D 是生产重构前置工作；阶段 E-H 分别作为独立生产变更，
  不合并成一个大 PR。

新会话开始后按以下顺序执行：

1. 完整阅读仓库根目录 `AGENTS.md` 和本方案；
2. 执行 `git status --short`，确认现有修改的归属；
3. 如果主 worktree 有与本任务无关的修改，按 `AGENTS.md` 从目标 base 创建独立
   worktree，不切换或重置脏 checkout；
4. 确认本方案已进入目标 base；如果它仍是 untracked 文件，先由维护者决定提交
   方案文档或继续在当前 worktree 工作，不能假定新 worktree 会包含它；
5. 重新清点当前 provider 和 cache consumer：

   ```sh
   rg -n "func newProviders|sessioncache\.Load|internal/sessioncache|sessioncache: not required" \
     cmd internal/provider --glob '*.go'
   ```

6. 将清点结果与第 3 节矩阵比较；如果 provider、存储格式或 cache interface 已
   变化，先更新影响矩阵和测试计划，再开始编码；
7. 依次完成阶段 A、B、C、D，并为每个阶段创建 coherent commit；
8. 阶段 D 的 before baseline 和报告存在后，才允许修改生产性能路径；
9. 每个生产阶段独立比较 correctness 和 performance，再决定是否进入下一阶段。

阶段完成标准：

- A：persistent-cache e2e runner 和 build-once benchmark runner 可用，生产代码
  无 diff；
- B：受影响模块/provider 的现有行为测试全部在 base revision 通过；
- C：除 title 新契约外，公共行为 e2e 全部在 base revision 通过；
- D：benchmark suite 已提交，before raw output 和持久化 baseline report 已生成；
- E-H：对应模块测试、provider tests、e2e、完整仓库检查和 before/after 性能证据
  全部完成。

### 0.1 必须暂停确认的产品决策

阶段 A-D 不依赖以下决策，可以直接执行。进入阶段 E 前必须获得维护者明确选择：

1. `Session.Title` 的 rune/byte 上限以及 ellipsis 是否计入上限；
2. 被截断的 title suffix 是否继续可搜索。

推荐默认方案是最多 512 rune、同时最多 2048 byte，ellipsis 计入上限，截断
suffix 不再搜索；完整用户文本只保留在受 preview/report options 管理的 evidence
路径中。该默认方案会改变 JSON title 和 query 行为，不能仅因写在本方案中就视为
已获授权。

以下是实现决策，不要求用户预先选择，但必须用 benchmark 和测试记录理由：

- cache shard 数量：至少比较 16、32、64，选择满足目标的最小值；
- legacy migration：优先 lazy/read-compatible migration，失败时回退 native store
  discovery；
- incremental parsing：只有 changed-file benchmark 在 cache 优化后仍显示显著瓶颈
  才实施。

## 1. 目标

本方案优化 `asm` 从进程启动到进入 Bubble Tea TUI 之前的同步路径，重点处理：

- 历史 session cache 的全量加载和全量重写；
- 超长 session title 对缓存体积、内存和搜索的放大；
- 活跃大型 JSONL 每次追加后发生的整文件重解析；
- discovery、过滤、排序和 UI model 构造中的重复工作。

优化必须满足以下前提：

- 先锁定公共行为和受影响模块的契约，再修改生产代码；
- 先建立可复现的重构前性能基线，再做重构后对比；
- provider-specific 格式继续留在 `internal/provider/<name>/`；
- cache 存储、迁移、分片和原子写入细节继续封装在
  `internal/sessioncache`；
- 不以隐藏 session、弱化 cwd 安全或减少 report evidence 为性能代价；
- 不在首轮优化中引入 SQLite、网络依赖或异步 TUI 加载。

## 2. 当前启动链路

当前主命令在创建 TUI 前完成以下同步工作：

1. 注册所有 provider；
2. 并发执行各 provider 的 `Discover`；
3. 等待全部 provider 返回；
4. 合并结果和 provider errors；
5. 执行可见性过滤、query 过滤和排序；
6. 构造 UI model，并再次刷新 session/project 索引；
7. 调用 `tea.NewProgram(...).Run()`。

provider discovery 已经是并发的，因此热态总耗时主要由最慢 provider 和共享
cache 工作决定，而不是各 provider 耗时之和。

### 2.1 本机观察基线

以下数据只用于说明瓶颈方向，不作为跨机器绝对门槛：

| 场景 | 观察耗时 |
|---|---:|
| 空 provider store、空 cache | 8-9 ms |
| 空 provider store、加载现有历史 cache | 52-55 ms |
| 真实 store、稳定 warm cache、约 500 个 session | 约 66 ms |
| 一次真实 cold/changed-file 峰值 | 最高观察到约 5.3 s |

当前本机 cache 和数据还暴露出：

- Codex cache 约 4.2 MB，1462 个 entry；
- Claude cache 约 1.3 MB，327 个 entry；
- CodeBuddy cache 约 1.2 MB，61 个 entry；
- cache 中最大 title：Codex 约 624 KB、Claude 约 114 KB、CodeBuddy
  约 43 KB；
- 最近 30 天 Codex rollout 最大约 17 MB，Claude transcript 最大约
  5.6 MB。

当前 `sessioncache.Load` 对单个 JSON cache 做完整 `ReadFile` 和
`json.Unmarshal`。任意 entry 发生 cache miss 后，`Save` 会重写整个 provider
cache。JSONL 文件只要 size 或 mtime 改变，就会完整重解析，不支持 append-only
增量解析。

## 3. 影响范围

### 3.1 Provider 矩阵

| Provider | 使用 `sessioncache` | 超长 title 触发可达 | 活跃 JSONL 整体重解析可达 | 首轮处理 |
|---|---:|---:|---:|---|
| Codex | 是 | 是 | 是 | cache、title、后续增量解析 |
| Claude | 是 | 是 | 是 | cache、title |
| Kimi | 否 | 是 | 否，compact index/state | title 对照组 |
| Kiro | 是 | 是 | 主文件为 metadata JSON | cache、title |
| opencode | 是 | 是 | 主要为小 JSON 和动态消息文件 | cache、title |
| CodeBuddy | 是 | 是 | 是 | cache、title |
| Cursor | 是 | 是 | 是 | cache、title |
| OpenClaw | 否 | 是 | 否，compact sessions index | title 对照组 |
| ZCode | 否 | 是 | 否，SQLite 查询 | title 对照组 |

由此得到三类独立变更：

1. cache 重构影响 Codex、Claude、Kiro、opencode、CodeBuddy、Cursor；
2. shared title policy 影响全部 provider，是公开 JSON/search 行为变化；
3. JSONL 增量解析必须由 provider 分别实现，先从 Codex 开始，不做过早的
   通用 parser 抽象。

### 3.2 受影响模块

| 模块 | 影响 |
|---|---|
| `internal/sessioncache` | lazy load、分片、迁移、原子写、corruption fallback |
| 六个缓存 provider | cache hit/miss、primary invalidation、动态输入重算 |
| `internal/session` 或新的 title 模块 | shared title normalization |
| `internal/index` | 截断后的 title 搜索契约 |
| `internal/ui` | 长 title、初始排序、refresh 和 viewport 行为 |
| `internal/report` | title 与 evidence 必须保持独立 |
| `cmd/asm` | discovery orchestration、过滤排序、TUI 初始构造 |
| `tests` | persistent cache e2e runner 和黑盒性能测试 |

`internal/launcher` 和 resume command 生成不应因本轮优化改变；e2e 仍需验证
missing cwd 和 unsupported provider 的安全行为没有回退。

## 4. 测试策略

测试分为四层，各层职责不能互相替代：

| 层次 | 职责 | 断言范围 |
|---|---|---|
| CLI/report e2e | 锁定用户可观察行为 | JSON、退出码、排序、筛选、安全行为 |
| Provider 集成测试 | 验证 native store、cache 和动态输入组合 | normalized session 和 provider metadata |
| 模块测试 | 验证 cache、title、index、UI、report 各自契约 | 模块公开接口和持久化结果 |
| 性能测试 | 量化 pre-TUI、cache、provider parse 成本 | wall time、ns/op、allocations，不代替正确性测试 |

### 4.1 E2E runner 改造

现有 `tests/e2e_test.go` runner 每次调用都会创建新的 `XDG_CACHE_HOME`，无法
覆盖真正的 warm-cache 行为。先增加 test-only runner：

```go
type asmTestEnv struct {
    CacheHome    string
    ProviderHome map[string]string
}

func (e asmTestEnv) Run(t testing.TB, args ...string) (string, error)
```

要求：

- 同一测试中的多次 CLI 调用复用相同 `XDG_CACHE_HOME`；
- 默认隔离全部 provider home、extra homes 和相关环境变量；
- 先从 inherited environment 移除受控 key，再写入测试值；
- 支持第一次运行后修改 native store，再执行第二次命令；
- 行为 e2e 可以继续使用 `go run ./cmd/asm`；
- 性能测试必须预先构建一次 binary，计时中不能包含 `go run` 编译。

### 4.2 公共行为 E2E

新增以下核心 e2e：

#### `TestCLICacheColdAndWarmResultsMatchAcrossProviders`

使用同一个 cache home，fixture 同时包含六个缓存型 provider。连续运行两次
`asm --json`，比较解析后的：

- `sessions`；
- `projects`；
- `provider_errors`；
- session ordering、metadata 和 cwd status。

不得断言 shard 文件名、cache entry 数量或私有 helper 调用。

#### `TestCLICacheInvalidationAndDynamicInputs`

第一次运行建立 cache，随后分别修改：

- primary session 文件；
- Codex `session_index.jsonl` / `history.jsonl`；
- Kiro prompt transcript fallback；
- opencode project/message fallback；
- cwd 的存在状态。

第二次运行必须观察到新值。primary file 未改变时，动态 side input 仍必须重新
应用。

#### `TestCLICachePreservesSinceLimitOrderingAndResumeSafety`

按顺序执行：

1. 默认 30 天；
2. `--since-days 0`；
3. 再次默认 30 天；
4. `--limit 1`；
5. 对 cwd 缺失的 session 尝试 `--print-exec` resume。

锁定：

- bounded discovery 不会错误清除 load-more 所需 cache；
- newest-first limit 不变；
- old session 只在 unbounded 窗口出现；
- missing cwd 仍拒绝 unsafe resume。

#### `TestCLITitleNormalizationAcrossProviders`

一个 CLI fixture 同时放入全部 provider 的普通 title 和超长 Unicode title。
断言：

- 普通 title 保持原样；
- 超长 title 满足统一长度限制且保持合法 UTF-8；
- `title_source` 保持正确；
- 明确尾部被截断后是否仍可搜索。

这是有意行为变化。测试必须先在 base revision 上因产品断言失败，再由实现使其
通过；fixture、编译或 runner 错误不能算 base reproduction。

#### `TestCLIReportKeepsEvidenceIndependentFromNormalizedTitle`

长用户消息可以产生短 title，但 report evidence 仍按 preview options 和时间窗口
选择，不能随 title 一起截断或丢失。

### 4.3 `internal/sessioncache` 模块测试

在重构前补齐当前行为：

- identity 的 path/size/mtime/provider 任一变化均 miss；
- `Get` 返回独立 copy，调用方不能修改 cache 内 entry；
- `Keep` 只删除未保留 entry；
- invalid JSON 和错误 Version 被视为空 cache；
- Save 替换旧内容，不留下 partial file；
- Unicode、大 metadata 和大 title 可以 round-trip；
- bounded discovery 不触发历史 entry pruning。

分片接口确定后，先写新契约测试：

- 只加载请求 identity 所需数据；
- 只持久化 dirty shard；
- unbounded prune 可以跨 shard 清理；
- 单个 corrupt shard 退化为 cache miss，不影响其他 shard；
- legacy single-file cache 可以迁移；
- migration 可重复执行；
- migration 失败时保留 legacy cache；
- 无变更 close/save 不重写文件；
- 不同 provider cache 互不干扰；
- 原子写失败不会破坏最后一份有效 cache。

测试应通过 `sessioncache` 的公开接口验证行为。除专门的 migration/atomic-write
测试外，不固定内部 shard 命名。

### 4.4 六个缓存 Provider 的测试矩阵

每个缓存 provider 至少覆盖：

1. cold discovery；
2. warm cache 输出一致；
3. primary file 改变后 cache invalidation；
4. cache hit 后动态输入重算；
5. since/limit/newest-first；
6. bounded scan 不错误 prune；
7. corrupt cache fallback；
8. cwd status refresh。

Provider-specific 补充如下：

| Provider | 必须补齐的重点 |
|---|---|
| Codex | rollout append、session index/history 更新、extra homes、parent/child 边界 |
| Claude | transcript append、duplicate ID newest file、extra homes |
| Kiro | metadata invalidation、prompt fallback refresh |
| opencode | session JSON invalidation、project/message fallback refresh |
| CodeBuddy | 当前缺少 cache-hit、cwd refresh、since/limit 测试，需完整补齐 |
| Cursor | transcript append、workspace cwd resolution refresh、继续跳过 subagents |

这些测试保持在各 provider package。可以复用 test helper，但不把 native schema
解析搬进共享生产代码。

### 4.5 非缓存 Provider 对照测试

Kimi、OpenClaw、ZCode 不添加虚假 cache 测试。它们作为对照组：

- 保留和运行现有 discovery tests/benchmarks；
- title policy 实施时增加 normal/long Unicode title cases；
- 确认 shared session 变化没有修改其 filtering、cwd 和 resume semantics。

### 4.6 Shared title 模块测试

如果增加 `session.NormalizeTitle` 或独立 title package，至少覆盖：

- empty/whitespace；
- 普通 ASCII 和中文；
- emoji、组合字符和合法 UTF-8；
- 多行及重复空白；
- 正好等于上限；
- 超过 rune 上限；
- 超过 byte 上限；
- ellipsis 计入上限；
- 输入未被原地修改；
- 普通 title 输出不变。

必须明确：

- `Session.Title` 使用 title 上限；
- `MessagePreview.Text` 和 `Evidence.Text` 继续由 preview/report options 控制；
- title normalization 不得用于 report evidence。

### 4.7 `internal/index` 测试

补齐：

- normalized title 可以搜索；
- 被明确丢弃的 title suffix 是否可搜索，按产品决策锁定；
- ID、provider、cwd、path、metadata 搜索不受影响；
- title normalization 不改变 active/created/project 排序；
- large title 不改变 project grouping。

如果决定保留截断尾部搜索，需要单独设计非展示型 search text；不能偶然把完整
prompt 继续塞进 title 或 metadata，从而抵消内存优化。

### 4.8 `internal/ui` 测试

补齐：

- 最大合法 title 的正常渲染；
- source title 异常大时 viewport 宽高仍受控；
- search 遵循 normalized title contract；
- refresh 后 selection、project、session ordering 稳定；
- load-more 替换结果后 selection 合理；
- configured initial sort mode 生效。

所有 layout 测试继续使用 `lipgloss.Width` 和 `lipgloss.Height`。

### 4.9 `cmd/asm` 测试

保留 provider 并发、registration-order merge 和 provider error 测试，并补齐：

- visibility/query/sort 只按预期应用；
- initial discovery 和 load-more 使用一致的 window/limit contract；
- 一个 provider cache/discovery 失败不丢弃其他 provider 的健康结果；
- empty provider result 不阻塞其他 provider；
- UI initial sort 不覆盖显式 CLI sort decision，或明确当前 CLI/TUI contract。

`cmd/asm` 不应知道 shard、migration 或 incremental parser 的实现细节。

### 4.10 `internal/report` 测试

补齐：

- payload 使用 normalized session title；
- title normalization 不截断 evidence；
- evidence counting 与 title 无关；
- resume command 不因 title 变化而改变；
- parent/subagent filtering 不受 cache 格式变化影响。

## 5. 独立性能测试

正确性测试不得用 wall-time assertion。性能测试使用独立 benchmark suite，并在
同一机器、Go 版本、base commit 和 after commit 上对比。

### 5.1 黑盒 CLI 启动 Benchmark

新增 `tests/startup_performance_test.go`。测试预先构建 `asm` binary，通过公开命令：

```sh
asm --resume __startup_probe__
```

不存在的 session 会在 discovery、过滤和排序后退出，可以覆盖 pre-TUI 主路径，
又不把大量 JSON serialization 算入启动耗时。

Benchmark scenarios：

- `EmptyStoresEmptyCache`：进程和 provider 空路径基线；
- `EmptyStoresHistoricalCache`：验证无 session 时历史 cache 固定成本；
- `WarmRecentWindow`：真实多 provider warm-cache；
- `WarmHistoryHeavy`：约 2000 条历史 cache、少量最近 session；
- `ColdPopulatedStores`：完整 parse 和 cache 生成；
- `ChangedLargeCodexSession`：约 16 MB rollout warm 后追加一条记录。

fixture 创建、binary build、warm-up、文件复制和 correctness verification 必须在
计时区间外。黑盒子进程 benchmark 只比较 wall time，不使用无意义的 `B/op`。

### 5.2 模块和 Provider Benchmark

`internal/sessioncache` 新增：

- history-heavy load；
- 少量 active identities lookup；
- 单 entry update/save；
- corrupt cache fallback；
- legacy migration。

Provider benchmark 新增：

- Codex 16 MB changed-file；
- Claude large changed-file；
- CodeBuddy/Cursor history-heavy warm-cache；
- 保留所有现有 cold/hot benchmark 作为回归对照。

`internal/index` / `internal/ui` 增加 2000 个 normalized sessions 的过滤、分组和
model construction benchmark，防止成本从 cache 转移到 UI 构造。

### 5.3 基线采集

测试和 benchmark 代码必须先作为独立 commit 落地。重构前执行：

```sh
go test -run '^$' -bench '^BenchmarkCLIStartup' \
  -count=10 -benchtime=1s ./tests > /tmp/asm-before-cli.txt

go test -run '^$' -bench . -benchmem \
  -count=10 -benchtime=1s \
  ./internal/sessioncache \
  ./internal/provider/codex \
  ./internal/provider/claude \
  ./internal/provider/kiro \
  ./internal/provider/opencode \
  ./internal/provider/codebuddy \
  ./internal/provider/cursor \
  ./internal/index \
  ./internal/ui > /tmp/asm-before-internal.txt
```

重构后用完全相同的命令生成 `after` 文件，通过固定版本的 `benchstat` 比较。
PR 记录：

- base/after commit；
- Go version；
- OS/arch/CPU；
- benchmark 命令；
- before/after 结果和置信区间；
- fixture session count、cache size 和大文件 size。

raw benchmark output 保存在 task-owned 临时目录，不提交机器相关的大文件；同时
新增并提交 `docs/tui-startup-performance-baseline.md`，记录：

- baseline commit SHA；
- benchmark suite commit SHA；
- 环境信息和 fixture 规模；
- 每个 scenario 的统计摘要；
- raw output 的生成命令；
- after comparison 的待填位置。

baseline report 是跨会话的持久化入口。新会话不得仅引用上一次会话中的口头数字
或无法定位的 `/tmp` 文件。

普通 CI 不设置 wall-time 硬阈值，避免共享 runner 抖动产生 flaky failure。CI 负责
保证 benchmark 编译和 correctness fixture 通过，性能验收由同机 benchstat 完成。

## 6. 实施阶段

### 阶段 A：测试设施

只修改测试代码：

- persistent-cache e2e runner；
- 全 provider 环境隔离；
- build-once benchmark runner；
- 公共 fixture helper 接受 `testing.TB`。

生产代码保持不变。

### 阶段 B：模块行为基线

补齐：

- `sessioncache` 当前格式契约；
- 六个缓存 provider 的 cold/warm、invalidation、dynamic inputs；
- CodeBuddy/Cursor 的 since/limit 和 cache-hit 缺口；
- `cmd/asm` orchestration；
- index/UI/report 中本轮会依赖的现有行为。

这些测试必须在当前实现上通过。

### 阶段 C：公共行为 E2E

增加 cold/warm parity、dynamic inputs、since/limit/cwd/resume safety、report
evidence 独立性。除新的 title 上限外，全部在当前实现上通过。

### 阶段 D：性能基线

提交独立 benchmark suite，采集 before 结果并生成
`docs/tui-startup-performance-baseline.md`。没有 baseline commit、raw output 生成
命令和持久化摘要，不进入生产重构。

### 阶段 E：Shared title policy

单独 PR 处理有意行为变化：

- 建立统一 title normalization；
- 普通 title 不变；
- 超长 title rune-safe/UTF-8-safe 截断；
- evidence/previews 不受影响；
- 全 provider、index、UI、report 和 CLI e2e 覆盖；
- cache Version 更新或 migration，清除旧超大 title。

具体 rune/byte 上限必须在实现前作为产品契约确认。尾部搜索语义也必须明确。

### 阶段 F：Cache 快速修复

先做小而可测的优化：

- bounded discovery 没有选中文件时跳过历史 cache load；
- cache 写入前确保 title 已 normalized；
- 无 dirty entry 时不执行持久化。

单独对比 `EmptyStoresHistoricalCache` 和 warm benchmark。

### 阶段 G：Cache 分片

推荐方向：

- identity/path hash 定位 shard；
- discovery 只加载 active identities 所需 shard；
- 单 entry miss 只重写 dirty shard；
- unbounded discovery 才遍历全部 shard pruning；
- shard 使用 temp file + rename 原子替换；
- 单 shard corruption 只造成局部 miss；
- legacy single-file cache 只读迁移，成功前不删除旧 cache。

provider 仍只调用 cache interface，不知道 shard layout。

### 阶段 H：Codex append-only 增量解析

只有 changed-file benchmark 证明它仍是主要瓶颈时才实施：

- cache parse offset、末尾完整 record 边界和必要 parser state；
- 文件只增长且前缀校验通过时解析 tail；
- truncate、replace、prefix mismatch、partial record 时 full parse；
- parent/child inherited-history、turn context、native title、preview 规则保持；
- 不将 Codex parser state 强行抽象给 Claude/CodeBuddy/Cursor。

Claude、CodeBuddy、Cursor 是否跟进，由各自 changed-file benchmark 决定。

### 暂不实施：异步 TUI 首屏

异步 provider result 会改变 loading state、selection stability、provider error 展示和
project/session 列表更新时机。当前先降低同步路径成本和 cold spike；只有优化后
pre-TUI p95 仍不达标时，才单独设计异步 TUI 方案和 PTY/public-boundary 测试。

## 7. 提交与验收门槛

推荐提交顺序：

1. test runner infrastructure；
2. module/provider behavior baselines；
3. public e2e baselines；
4. performance benchmark suite and before results；
5. shared title policy；
6. cache quick wins；
7. cache sharding and migration；
8. Codex incremental parse（如 benchmark 支持）。

每个生产优化 PR 至少报告：

- 公共行为契约；
- 新增或更新的模块测试；
- affected/not-affected provider 分类；
- base failure 和 fixed pass（有意新行为或 bug fix）；
- focused tests 和完整测试命令；
- before/after benchmark；
- 未处理 sibling provider 的后续计划。

### 7.1 行为验收

- 六个缓存 provider cold/warm normalized output 一致；
- cache migration 前后普通 session JSON 一致；
- since、limit、ordering、dynamic title、cwd status 不变；
- corrupt cache 不隐藏 session，不导致 unsafe resume；
- title 变化只限明确的新 normalization contract；
- report evidence 不因 title/cache 优化丢失；
- 全部 e2e 不读取开发者本机 provider store。

### 7.2 性能验收

在建立 before 数据后，以同机 benchstat 为准，初始目标为：

- `EmptyStoresHistoricalCache` 相对 before 降低至少 70%；
- `WarmHistoryHeavy` wall time 和 allocations 降低至少 40%；
- 单 entry cache update 不再与整个 provider cache 大小线性相关；
- Codex 16 MB append 场景在增量解析阶段降低至少 70%；
- 未参与优化的 provider 不出现统计显著的 5% 以上回退；
- index/UI model construction 不出现统计显著回退。

绝对毫秒数不作为跨机器验收标准。

## 8. 验证命令

Focused tests：

```sh
go test ./internal/sessioncache
go test ./internal/provider/codex
go test ./internal/provider/claude
go test ./internal/provider/kiro
go test ./internal/provider/opencode
go test ./internal/provider/codebuddy
go test ./internal/provider/cursor
go test ./internal/provider/kimi
go test ./internal/provider/openclaw
go test ./internal/provider/zcode
go test ./internal/index
go test ./internal/ui
go test ./internal/report
go test ./cmd/asm
go test ./tests
```

完成每个生产变更前执行仓库完整检查：

```sh
gofmt -w cmd internal tests
go run ./tools/check-provider-performance
golangci-lint run ./...
go test ./...
go build ./cmd/asm
pre-commit run --all-files
```

## 9. 决策记录

实施期间在 PR 描述或后续 ADR 中持续记录：

- title rune/byte 上限和尾部搜索语义；
- cache shard 数量与选择依据；
- legacy cache migration/rollback 策略；
- benchmark fixture 规模和选择原因；
- Codex incremental parser state 的正确性约束；
- 每个 sibling provider 的 affected/not-affected 证据；
- 未达到性能目标时保留或回退优化的决定。

本方案本身不授权删除用户 cache 或真实 provider store。所有 migration、corruption
和 destructive test 只能作用于测试创建的临时目录。

## 10. 实施跟踪

本节是阶段 A-D 的任务跟踪记录。状态只依据已提交代码和实际命令输出更新；
阶段 A-D 不修改生产代码。

基线环境：

- base commit：`53dd1e7`；
- Go：`go1.26.5 linux/amd64`；
- OS/arch：Linux `x86_64`；
- 工作树起点：仅本计划文档未跟踪，无生产代码改动。

阶段状态：

- [x] A：persistent-cache e2e runner、全 provider 环境隔离、build-once
  benchmark runner、`testing.TB` fixture helper；
- [x] B：sessioncache、六个缓存 provider、cmd/asm、index、UI、report
  的当前行为基线；
- [x] C：cold/warm parity、dynamic inputs、since/limit/resume safety、
  report evidence 公共行为 E2E；
- [x] D：独立 benchmark suite、重构前 CLI/internal 原始结果及环境记录。

执行记录：

- 2026-08-07：确认现有 `tests/e2e_test.go` 每次调用都会创建新的
  `XDG_CACHE_HOME`，且通过向 inherited environment 追加键值来隔离 provider；
  阶段 A 需要先移除 inherited controlled keys，再设置测试值。
- 2026-08-07：确认六个缓存 provider 为 Codex、Claude、Kiro、opencode、
  CodeBuddy、Cursor；Kimi、OpenClaw、ZCode 的存储模型不使用
  `sessioncache`，只作为共享行为对照组。
- 2026-08-07：阶段 A 完成。`asmTestEnv` 支持复用 cache home、修改 native
  store 后再次运行、预构建/直接运行 binary；公共 fixture helper 已接受
  `testing.TB`。验证：`go test ./tests` 通过。
- 2026-08-07：阶段 B 完成。六个缓存 provider 均已锁定 cold/warm parity、
  primary invalidation、适用的 dynamic input、since/limit、bounded scan 不
  prune、corrupt cache fallback 和 cwd/workspace refresh；sessioncache、
  orchestration、index、UI、report 的依赖契约已有模块测试。Focused tests
  按第 8 节对应包执行并全部通过。
- 2026-08-07：阶段 C 完成。公共 CLI E2E 使用同一 cache home 验证六个缓存
  provider 的 cold/warm JSON parity、primary/dynamic/cwd invalidation、
  bounded/unbounded/limit/ordering 和 missing-cwd resume safety；report E2E
  验证 title 依照现有安全契约被省略而长 evidence 完整保留。阶段 E 才引入的
  title normalization 上限测试未提前加入阶段 C，避免在生产契约确认前留下
  必然失败的测试。验证：`go test ./tests -count=1` 通过。
- 2026-08-07：阶段 D 完成。benchmark suite commit 为 `99522cc`，计时外
  fixture correctness 和 empty-store 契约补强后的 before commit 为
  `8734292`；A-D
  期间生产代码相对 `53dd1e7` 未改变。原始 10 轮结果保存在
  `/tmp/asm-before-cli.txt`（60 个样本）和
  `/tmp/asm-before-internal.txt`（各 package/benchmark 10 个样本）；fixture
  指标保存在 `/tmp/asm-before-fixtures.txt`。汇总使用
  `golang.org/x/perf/cmd/benchstat@v0.0.0-20260709024250-82a0b07e230d`。

重构前 CLI 基线（benchstat 中位数与区间）：

| 场景 | sec/op |
|---|---:|
| EmptyStoresEmptyCache | 5.384 ms ±1% |
| EmptyStoresHistoricalCache | 9.489 ms ±2% |
| WarmRecentWindow | 9.188 ms ±2% |
| WarmHistoryHeavy | 31.26 ms ±1% |
| ColdPopulatedStores | 10.37 ms ±2% |
| ChangedLargeCodexSession | 63.14 ms ±2% |

关键 internal 基线：

| Benchmark | sec/op | B/op | allocs/op |
|---|---:|---:|---:|
| sessioncache/HistoryHeavyLoad | 16.87 ms | 2.977 MiB | 38.05k |
| sessioncache/ActiveIdentityLookup | 4.177 µs | 3.281 KiB | 20 |
| sessioncache/SingleEntryUpdateSave | 7.669 ms | 1.147 MiB | 18.01k |
| sessioncache/CorruptCacheFallback | 331.6 µs | 1.009 MiB | 14 |
| sessioncache/LegacyCacheLoad | 17.24 ms | 2.977 MiB | 38.05k |
| codex/DiscoverChangedLargeSession | 37.33 ms | 40.25 MiB | 1.153k |
| claude/DiscoverChangedLargeSession | 70.49 ms | 35.55 MiB | 399 |
| codebuddy/DiscoverHistoryHeavyWarmCache | 26.54 ms | 4.642 MiB | 42.13k |
| cursor/DiscoverHistoryHeavyWarmCache | 56.36 ms | 6.872 MiB | 56.15k |
| index/FilterAndGroup2000Sessions | 1.125 ms | 1.701 MiB | 365 |
| ui/NewModel2000Sessions | 2.042 ms | 1.932 MiB | 388 |

CLI fixture 规模与生成后 cache 大小：

| 场景 | session 数 | cache bytes | 大文件 |
|---|---:|---:|---:|
| EmptyStoresEmptyCache | 0 | 0 | 0 |
| EmptyStoresHistoricalCache | 500 个 cache entry、0 个 native session | 约 238.3 KiB | 0 |
| WarmRecentWindow | 120（六 provider 各 20） | 约 67.94 KiB | 0 |
| WarmHistoryHeavy | 2000（其中 10 个 recent） | 约 894.6 KiB | 0 |
| ColdPopulatedStores | 120（六 provider 各 20） | 约 69.06 KiB | 0 |
| ChangedLargeCodexSession | 1 | 约 569 bytes | 16 MiB rollout |

最终验证（2026-08-07）：

- `gofmt -w cmd internal tests`；
- `go run ./tools/check-provider-performance`；
- `golangci-lint run ./...`；
- `go test ./...`；
- `go build ./cmd/asm`；
- `pre-commit run --all-files`。

以上命令全部通过；完成审计时工作树无未提交改动，阶段 A-D 未修改生产代码。

### 10.1 发布与后续接手

- 发布分支：`agent/tui-startup-abcd`；
- PR：[#39](https://github.com/hxy91819/agent-session-manager/pull/39)；
- PR 首个提交：`66bff0190d8c69d9b90dc0e4cb9f3324e2082ef8`；
- autoreview：
  `/data/code/openclaw/openclaw/.agents/skills/autoreview/scripts/autoreview
  --mode branch --base origin/master`；TruffleHog clean，完整 bundle 单 pass，
  `no accepted/actionable findings reported`；
- 发布前隐私检查：tracked diff 未包含 secret 或本机日志；gitleaks 的 7 个命中
  全部位于被 `.gitignore` 排除的本机 `.env` 和 `.local/`，未进入 PR；发布提交
  使用 GitHub noreply 邮箱；
- PR 检查和最终 merge commit 以 PR 页面为准，避免在合并前写入推测状态。

PR #39 首轮 macOS/Ubuntu CI 暴露了 cache parity 测试的跨时区误报：cold
session 携带 `time.Local`，JSON cache round-trip 后为 UTC；二者代表同一 instant，
但 `reflect.DeepEqual` 还会比较 `time.Location` 的内部状态。处理规则如下：

- 内部比较、cache 和持久化采用绝对时间语义；生产 JSON 是否统一规范化为 UTC
  留到后续行为阶段单独决策，不在阶段 A-D 的测试修复中顺带改变；
- TUI 展示以及“今天/昨天”等自然日窗口继续使用 Local；
- 六个使用 sessioncache parity 测试的 provider（Codex、Claude、Kiro、
  opencode、CodeBuddy、Cursor）统一使用 `internal/sessiontest.RequireEqual`：所有
  timestamp 按 instant 比较，其余 session 字段保持严格相等；
- Kimi、OpenClaw、ZCode 不经过这条 sessioncache round-trip 路径，无需修改；
- AGENTS.md 固化共享断言规则；现有 pre-commit `go test` 与 Linux、macOS、
  Windows CI 会执行这些测试，无需再增加一套重复 hook。这样本地提交前和远端
  跨平台各有独立拦截点。

修复提交、第二轮 autoreview、最终 checks 与 merge commit 在完成后以 PR 页面
和提交记录为准。

该交接点之后已完成阶段 E-H。title 契约、逐阶段 after 数据、被否决方案和最终
决策均记录在后续小节及 `docs/tui-startup-performance-baseline.md`。

阶段 D 交付物补充核对（2026-08-07）：PR #39 合并到 `origin/master` 后遗漏了计划
要求的 `docs/tui-startup-performance-baseline.md`，但 benchmark suite、原始输出和
本节摘要均存在。阶段 E 开始前已从这些可核对证据恢复持久化基线文档；该修复不
改变 fixture、历史统计或生产代码。后续各生产阶段的 after 数据统一追加到该文档。

### 10.2 阶段 E：Shared title policy

- [x] 产品契约确认：最多 512 rune、最多 2048 byte、`…` 计入上限、截断尾部
  不可搜索；普通 title 不变；
- [x] 9 个 provider 的 native/dynamic title 路径统一归一化，六个 cache provider
  写入 cache 前归一化，cache Version 更新为 6；
- [x] base 公共 CLI 产品断言失败、修复后通过；report evidence 保持独立；
- [x] focused tests、完整仓库检查和 10 样本 CLI/internal benchmark 完成；
- [x] 修复阶段 D 未覆盖 oversized title 的 benchmark 缺口并记录 before/after。

阶段 E 最终生产提交为 `1510a18`，benchmark 补强提交为 `68c26ca`。新增
`WarmOversizedTitles` 补测显示 wall time `-86.32%`、cache bytes `-96.77%`；原六个
CLI 场景最大变化为 `+1.54%`。详细 raw output、provider 分类、完整 internal 结果和
Claude changed-large `+5.81%` paired A/B 观测见
`docs/tui-startup-performance-baseline.md`。该 Claude 项在 F/G 后复测，不影响进入
阶段 F，但不得在最终审计中遗漏。

### 10.3 阶段 F：Cache 快速修复

- [x] bounded discovery 没有 native 文件时跳过六个 provider 的历史 cache load；
- [x] unbounded discovery 继续加载并 prune，bounded/unbounded 和 resume safety
  公共行为不变；
- [x] 无 dirty 不写盘沿用既有实现，title 写 cache 前归一化由阶段 E 保证；
- [x] focused tests、完整仓库检查和 CLI/internal 10 样本对比完成。

最终提交 `dbc7bb8`。`EmptyStoresHistoricalCache` 相对 D 为 `-41.87%`，相对 E
为 `-42.35%`；扣除 `EmptyStoresEmptyCache` 固定成本后，历史 cache 增量开销降低约
`98.6%`。其他 CLI 场景相对 E 无显著变化，internal 无 5% 回退。完整 raw output
和计算口径见 `docs/tui-startup-performance-baseline.md`。

### 10.4 阶段 G：Cache 分片

- [x] lazy shard load、dirty shard save、跨 shard prune 和局部 corruption fallback；
- [x] legacy 单文件只读兼容迁移，manifest 最后原子启用，失败保留最后有效 cache；
- [x] cold/warm parity、dynamic inputs、bounded/unbounded、resume safety 和迁移容错
  测试通过；
- [x] 16/32/64 shard benchmark、CLI/internal 10 样本和完整仓库检查完成。

最终提交 `bc63d8e`。固定 32 shard 因小 cache CLI 回退 `+6.01%` 至 `+15.10%`
被否决；自适应 32-shard 大型布局虽消除回退，但 `WarmHistoryHeavy` 仅 `-34.57%`，
未达到 40% 目标。最终采用不超过 128 entry 内联 manifest、中型 16 shard、大型
64 shard：`WarmHistoryHeavy` 相对 F `-42.55%`、相对 D `-42.33%`，其他 CLI
wall time 无显著回退；single-entry save 相对 D 降低 `-87.52%`。完整数据和容错
证据见 `docs/tui-startup-performance-baseline.md`。

Codex changed-large CLI 仍为 63.19 ms，internal 为 38.16 ms，与 D 无显著变化，
因此满足阶段 H 的实施条件。Claude changed-large 相对 D 仅 `+2.02%`，CodeBuddy、
Cursor 没有对应结果证明需要同步优化；阶段 H 保持 Codex provider-specific，不引入
共享增量 parser 抽象。

### 10.5 阶段 H：Codex append-only 增量解析

- [x] ≥1 MiB rollout 保存 offset 和 64 KiB 首尾指纹，只解析已验证 append tail；
- [x] truncate、replace、prefix mismatch、partial record 和 inherited-history 回退
  full parse；
- [x] native title、turn context、preview/report evidence 的 provider 与 CLI 行为覆盖；
- [x] focused tests、完整仓库检查和最终 CLI/internal 10 样本完成。

最终提交 `d07c0e5`。CLI `ChangedLargeCodexSession` 从 63.194 ms 降至 6.467 ms
（`-89.77%`），Codex internal 从 38.159 ms 降至 0.899 ms（`-97.64%`），超过
70% 目标；其余 CLI 最大变化 `+2.40%`，未参与 provider、index/UI 无 5% 回退。

完整前缀哈希方案因 internal 仅改善约 65% 被否决；所有小 rollout 都存状态的版本
因 history-heavy `+5.20%` 和 cache bytes `+53.83%` 被否决。最终用 1 MiB 门槛和
稀疏 side map 消除常规 cache 成本。Claude/CodeBuddy/Cursor 保留 provider-specific
后续评估：Claude 当前 benchmark 未显示阶段 H 引入的回退，CodeBuddy/Cursor 需先有
changed-large 数据；其余 provider 存储模型不匹配。完整数据见
`docs/tui-startup-performance-baseline.md`。

## 10. 任务终止条件

以下任一情况出现时停止当前生产阶段，不带着不确定性继续扩大修改：

- 第 0.1 节的产品决策尚未确认却准备进入 title 行为变更；
- 无法在 public CLI 复现或锁定本阶段声明的用户行为；
- native fixture 与当前 producer schema 不一致；
- provider 影响分类仍有 unknown 且本次 shared abstraction 会覆盖该 provider；
- before benchmark 缺失、fixture correctness 未验证或 before/after 环境不可比；
- cache migration 可能删除唯一有效 cache，或测试触及真实用户 store；
- focused tests 通过但完整 e2e 显示 session 丢失、排序变化、evidence 缺失或 unsafe
  resume。

停止时记录具体 blocker、已完成证据和恢复入口。困难、耗时或性能收益低于预期本身
不是跳过测试或扩大授权的理由。
