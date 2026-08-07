# asm TUI 启动性能基线

## 用途

本文档是阶段 D 之后所有启动性能优化的持久化对比入口。机器相关的 raw output
不提交；每个生产阶段必须在同一机器用相同命令生成 after output，并把统计摘要、
commit 和结论追加到本文档。

## 重构前基线

- 采集日期：2026-08-07；
- 生产 base commit：`53dd1e7`；
- benchmark suite commit：`99522cc`；
- 计时外 fixture correctness 补强后的采集 commit：`8734292`；
- Go：`go1.26.5 linux/amd64`；
- OS/arch：Linux `x86_64`；
- CPU：AMD EPYC 7K62 48-Core Processor，当前环境可见 32 CPU；
- benchstat：`golang.org/x/perf/cmd/benchstat@v0.0.0-20260709024250-82a0b07e230d`；
- 样本数：每个 benchmark 10；`benchtime=1s`。

CLI fixture：

| 场景 | session 数 | cache 大小 | 大文件 |
|---|---:|---:|---:|
| EmptyStoresEmptyCache | 0 | 0 | 0 |
| EmptyStoresHistoricalCache | 500 个历史 entry，0 个 native session | 约 238.3 KiB | 0 |
| WarmRecentWindow | 120（六 provider 各 20） | 约 67.94 KiB | 0 |
| WarmHistoryHeavy | 2000（10 个 recent） | 约 894.6 KiB | 0 |
| ColdPopulatedStores | 120（六 provider 各 20） | 约 69.06 KiB | 0 |
| ChangedLargeCodexSession | 1 | 约 569 bytes | 16 MiB rollout |

CLI wall time（benchstat 中位数与区间）：

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

原始结果生成命令：

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

## 基线交付物修复记录

- 2026-08-07：PR #39 合并后的 `origin/master`（`6e7060f`）包含 benchmark
  suite 和 `docs/tui-startup-optimization-plan.md` 内的完整摘要，但遗漏了计划指定的
  本文件。开始阶段 E 前核对到该缺口；本机 `/tmp/asm-before-cli.txt`、
  `/tmp/asm-before-internal.txt` 和 `/tmp/asm-before-fixtures.txt` 仍存在，环境、
  commit、样本数和摘要与计划记录一致，因此从已验证记录恢复持久化入口。
- 该修复不修改 benchmark、fixture 或生产代码，也不重新解释历史结果。后续阶段
  以本文件中的 base 数据为统一 before，并保留各阶段新 raw output 的生成命令。
- 2026-08-07：在 PR #39 的合并提交 `6e7060f` 上额外执行 3 轮 CLI 冒烟复验：

  ```sh
  go test -run '^$' -bench '^BenchmarkCLIStartup' \
    -count=3 -benchtime=1s ./tests \
    > /tmp/asm-tui-startup-eh/phase-d-recheck-cli.txt
  go run golang.org/x/perf/cmd/benchstat@v0.0.0-20260709024250-82a0b07e230d \
    /tmp/asm-before-cli.txt \
    /tmp/asm-tui-startup-eh/phase-d-recheck-cli.txt
  ```

  六个场景的 fixture session 数和 cache bytes 与历史基线一致；五个 wall-time
  场景无显著差异。`ColdPopulatedStores` 显示约 `+3.37%`，但 after 只有 3 个样本，
  benchstat 明确提示无法计算 95% 置信区间，因此该结果只证明 benchmark 和 fixture
  在合并提交上可复现，不作为性能回退或阶段验收结论。正式 E-H 对比仍使用每项
  10 个样本。

## 阶段对比记录

| 阶段 | after commit | CLI 结论 | internal 结论 | 决策 |
|---|---|---|---|---|
| E：Shared title policy | `0c7f283` | oversized title 启动 `-86.32%`、cache `-96.77%`；原六场景最大变化 `+1.54%` | cache/index/UI 无 5% 回退；paired A/B 中 Codex changed-large 无显著变化、Claude `+5.81%` | 保留；Claude 项在 F/G 后复测 |
| F：Cache 快速修复 | `110bbb0` | EmptyStoresHistoricalCache 相对 D `-41.87%`、相对 E `-42.35%`；历史 cache 增量开销约 `-98.6%` | 相对 E 无 5% 回退，allocations 稳定 | 保留，进入 G |
| G：Cache 分片 | `cabf966` | WarmHistoryHeavy 相对 F `-42.55%`、相对 D `-42.33%`；其他 CLI wall time 无显著回退 | HistoryHeavyLoad `-99.92%`、SingleEntryUpdateSave `-87.52%`；index/UI 无回退 | 保留 64-shard 大型布局，进入 H |
| H：Codex 增量解析 | `eeeee26` | ChangedLargeCodexSession 相对 G `-89.77%`；其他 CLI 最大变化 `+2.40%` | Codex changed-large `-97.64%`、bytes `-99.33%`；其他 provider/index/UI 无 5% 回退 | 保留，阶段 E-H 完成 |
| P2：Codex metadata 快路径 | `a433aab` | 真实冷启动 `-28.68%`；交替热测无显著变化 | 混合 rollout `-94.70%`、B/op `-96.54%`；实际输入 bytes 不变 | 保留；report 继续完整解析，P3 未开始 |

## 阶段 E：Shared title policy

实现与契约：

- 维护者确认最多 512 rune、同时最多 2048 byte，`…` 计入上限，被截断尾部不再
  搜索；普通 title 保持原样；
- 生产实现提交：`a1f7fa8`；避免缓存型 provider 重复归一化后的最终提交：
  `0c7f283`；benchmark 补强提交：`50a5c3a`；
- `sessioncache.Version` 从 5 更新到 6，旧 cache 安全退化为 miss 并从 native
  store 重建，不删除用户 cache；report evidence 和 preview 没有经过 title 截断。

公共行为证据：

- base `b27198c` 上执行
  `go test ./tests -run '^TestCLITitleNormalizationAcrossProviders$' -count=1`，
  9 个 provider 的长 title 均以 827–832 rune、约 2.8 KiB 原样返回，尾部 token
  仍可搜索，测试因产品断言失败；
- 最终实现上同一测试通过；普通 title、`title_source`、合法 UTF-8、rune/byte
  上限和尾部不可搜索均由真实 provider fixture 锁定；
- `TestCLIReportKeepsEvidenceIndependentFromNormalizedTitle` 通过，证明 title 截断
  不改变 report evidence；focused provider、index、UI、report、cmd 和 CLI tests
  以及完整仓库检查全部通过。

Provider 影响分类：

| Provider | title 路径 | cache | 结论 |
|---|---|---:|---|
| Codex | rollout，动态 session index/history | 是 | 两条路径均归一化 |
| Claude | project JSONL user/summary | 是 | cache 写入前归一化 |
| Kimi | state title/last prompt | 否 | 返回前归一化 |
| Kiro | metadata JSON，动态 prompt fallback | 是 | 两条路径均归一化 |
| opencode | session JSON，动态 message fallback | 是 | 两条路径均归一化 |
| CodeBuddy | project JSONL title/summary/user | 是 | cache 写入前归一化 |
| Cursor | transcript first user message | 是 | cache 写入前归一化 |
| OpenClaw | compact sessions index | 否 | 返回前归一化 |
| ZCode | SQLite title/first input | 否 | 返回前归一化 |

### E.1 基线缺口与修正

阶段 D 的 history-heavy fixture 只含短 title，无法量化真实 cache 中 43–624 KiB
title 的放大。阶段 E 新增公开 CLI `WarmOversizedTitles`：120 个 Claude session，
每个 native title 约 68.4 KiB。相同 benchmark 代码分别应用到生产 base
`b27198c`（临时 benchmark commit `f4bee01`）和最终实现，fixture correctness 在
计时外验证 120 个 session。该场景是 D 基线遗漏后的补测，不回填或改写原始 D
数字。

补测命令：

```sh
go test -run '^$' \
  -bench '^BenchmarkCLIStartup/WarmOversizedTitles$' \
  -count=10 -benchtime=1s ./tests
```

| 指标 | before | E after | 变化 |
|---|---:|---:|---:|
| wall time | 68.238 ms ±1% | 9.338 ms ±3% | -86.32% |
| cache bytes | 8266.8 KiB | 267.0 KiB | -96.77% |
| fixture sessions | 120 | 120 | 无变化 |

### E.2 原始 suite 对比与异常复核

最终 raw output：

- CLI：`/tmp/asm-tui-startup-eh/phase-e-final2-cli.txt`；
- internal：`/tmp/asm-tui-startup-eh/phase-e-final2-internal.txt`；
- oversized before：`/tmp/asm-tui-startup-eh/phase-e-oversized-before2.txt`；
- changed-large paired A/B：`/tmp/asm-tui-startup-eh/phase-e-ab3-base.txt` 和
  `/tmp/asm-tui-startup-eh/phase-e-ab3-after.txt`。

原六个 CLI 场景中，五项无显著变化；`ChangedLargeCodexSession` 为 `+1.54%`，
仍低于 5%。sessioncache 的关键 wall time 最大变化 `+1.64%`，allocations 无变化；
index 为 `+1.31%`，UI 无显著变化。整套 internal 串行结果的 Codex/Claude
changed-large 分别出现 `+18.95%`/`+9.99%`，且多轮整套采样漂移较大，因此又将
base/after test binary 交替执行 10 次：Codex 无显著差异，Claude 为 `+5.81%`，
两者 B/op 和 allocs/op 均无显著回退。Claude 参与本阶段 title 路径，暂不作为
“未参与 provider 回退”否决阶段 E；保留该观测，在 F/G 完成后用 paired A/B
复测，再决定是否需要 Claude incremental parsing follow-up。

正式 after 命令与阶段 D 相同，仅输出路径改为上述 `phase-e-final2-*`；统计继续
使用固定 benchstat 版本。阶段 E 的主要性能收益由新增 oversized 场景证明，原有
短 title fixture 保持基本稳定。

## 阶段 F：Cache 快速修复

实现提交 `110bbb0`。六个缓存型 provider 在 bounded discovery 没有选中任何
native 文件时，直接返回空结果，不再读取历史 cache；unbounded discovery 仍加载
cache 并执行 prune。无 dirty 时不写盘是阶段 D 已锁定的现有行为，title 写 cache
前归一化已由阶段 E 完成，因此 F 没有重复实现这两项。

正确性证据：

- `sessioncache.SkipLoadForEmptyDiscovery` 覆盖 since/limit bounded empty、
  unbounded empty 和 bounded non-empty；
- `EmptyStoresHistoricalCache` 在计时外继续验证 0 session；cache cold/warm、
  bounded/unbounded、load-more 和 missing-cwd resume e2e 全部通过；
- 六个 provider focused tests、完整仓库检查全部通过。

正式 raw output：

- CLI：`/tmp/asm-tui-startup-eh/phase-f-after-cli.txt`；
- internal：`/tmp/asm-tui-startup-eh/phase-f-after-internal.txt`；
- 相对 D：`/tmp/asm-tui-startup-eh/phase-f-vs-d-cli.txt`；
- 相对 E：`/tmp/asm-tui-startup-eh/phase-f-vs-e-cli.txt` 和
  `/tmp/asm-tui-startup-eh/phase-f-vs-e-internal.txt`。

CLI 10 样本结果：

| 场景 | D before | E | F | F vs D | F vs E |
|---|---:|---:|---:|---:|---:|
| EmptyStoresHistoricalCache | 9.489 ms | 9.566 ms | 5.515 ms | -41.87% | -42.35% |
| EmptyStoresEmptyCache | 5.384 ms | 5.424 ms | 5.458 ms | 无显著变化 | 无显著变化 |
| WarmRecentWindow | 9.188 ms | 9.225 ms | 9.287 ms | 无显著变化 | 无显著变化 |
| WarmHistoryHeavy | 31.26 ms | 30.97 ms | 31.38 ms | 无显著变化 | 无显著变化 |
| ChangedLargeCodexSession | 63.14 ms | 64.11 ms | 63.58 ms | 无显著变化 | 无显著变化 |

绝对 wall time 包含约 5.4 ms 的空进程/provider 固定成本；按历史 cache 增量开销
计算，D 为 `9.489-5.384=4.105 ms`，F 为 `5.515-5.458=0.057 ms`，降低约
`98.6%`，超过计划的 70% 目标。相对 E 的其余 CLI 场景均无显著变化。

internal 相对 E：Claude changed-large 无显著变化，说明阶段 E 的观测项没有在 F
继续恶化；Codex changed-large 为 `-17.25%`，反映该 benchmark 跨整套运行的漂移，
不归因于只触发 empty discovery 的 F。sessioncache 关键 load/save、provider
history-heavy、index 和 UI 均无 5% 回退，B/op 与 allocs/op 稳定。正式命令、环境、
样本数和 benchstat 版本与阶段 E/D 相同。

## 阶段 G：Cache 分片

最终生产提交 `cabf966`。`sessioncache` 按 identity/path hash 延迟加载所需 shard，
只保存 dirty shard，并在 unbounded prune 时跨 shard 清理。单 shard 损坏只造成局部
miss；legacy 单文件 cache 保持只读兼容，迁移失败时继续保留最后有效 cache，成功
迁移则最后原子写入 manifest 启用新布局。Provider 仍只依赖 cache 接口，不知道
分片布局。

布局选择经过三轮验证：

- 固定 32 shard 会让小 cache 的 `WarmRecentWindow`、`WarmOversizedTitles` 和
  `ColdPopulatedStores` 分别回退 `+6.01%`、`+8.18%`、`+15.10%`，因此否决；
- 改为小 cache（不超过 128 entry）内联 manifest、中型 16 shard、大型 32 shard
  后消除了上述 CLI 回退，但公开 `WarmHistoryHeavy` 仅改善 `-34.57%`，未达到
  40% 门槛；
- 最终大型 cache 使用 64 shard。相同 2000-entry 样本中，16/32/64 个 active
  shard 的 load 分别为 11.05/5.511/2.764 ms，single-entry update 分别为
  1.854/1.244/1.008 ms，因此选择 64。

公共行为与容错证据：

- cold/warm、primary/dynamic invalidation、bounded/unbounded、limit/order、cwd
  refresh 和 resume safety E2E 均保持不变；CLI fixture session 数全部不变；
- 模块测试覆盖 lazy load、dirty save、跨 shard prune、局部 corruption、legacy
  迁移幂等性、迁移/rename 失败回滚和不同 provider 隔离；
- 完整仓库检查全部通过。

最终 raw output：

- CLI：`/tmp/asm-tui-startup-eh/phase-g-final64-cli.txt`；
- internal：`/tmp/asm-tui-startup-eh/phase-g-final64-internal.txt`；
- 相对 F：`/tmp/asm-tui-startup-eh/phase-g-final64-vs-f-cli.txt`；
- 相对 D：`/tmp/asm-tui-startup-eh/phase-g-final64-vs-d-cli.txt` 和
  `/tmp/asm-tui-startup-eh/phase-g-final64-vs-d-internal.txt`。

CLI 10 样本中，`WarmHistoryHeavy` 从 F 的 31.38 ms 降至 18.03 ms
（`-42.55%`），相对 D 为 `-42.33%`；`EmptyStoresHistoricalCache` 相对 D 为
`-43.67%`，其余场景无显著 wall-time 回退。Codex changed-large 为 63.19 ms，
与 D 的 63.14 ms 无显著差异，说明整文件解析仍是阶段 H 的主要瓶颈。

相对 D 的关键 internal 结果：

| Benchmark | D | G | wall time | bytes | allocs |
|---|---:|---:|---:|---:|---:|
| HistoryHeavyLoad | 16.87 ms | 12.72 us | -99.92% | -99.95% | -99.96% |
| SingleEntryUpdateSave | 7.669 ms | 957.3 us | -87.52% | -96.94% | -96.81% |

`ActiveIdentityLookup` 增加约 1.06 us，属于 hash/shard 定位固定成本；部分 provider
hot-cache microbenchmark 因读取小 shard/manifest 出现 5% 以上相对变化，但公开 CLI
场景未出现对应回退，history-heavy 和 cold 路径整体显著改善。Codex/Claude
changed-large 分别无显著变化和 `+2.02%`，index/UI 无显著变化。阶段 H 只处理
Codex；Claude 未达到跟进门槛，CodeBuddy/Cursor 也暂不扩大范围。

## 阶段 H：Codex append-only 增量解析

最终生产提交 `eeeee26`。仅对不小于 1 MiB 的 Codex rollout 保存 provider-owned
解析状态：完整 record offset，以及旧前缀首尾各 64 KiB 的 SHA-256 指纹。文件增长
且两端指纹匹配时只解析 tail；truncate、edge-altering replace、prefix/boundary
rewrite、partial JSONL 或 parent/child inherited-history 边界均回退 full parse。
状态通过 `sessioncache` 的稀疏 opaque side map 持久化，其他 provider 不理解
Codex parser state，普通小 cache entry 也不承担固定状态体积。

正确性证据：

- 公共 CLI 先建立 cache，再追加 turn context 和 user message；第二次 JSON 输出保持
  native title/title_source，同时刷新 cwd/model，report 输出保留追加前后两条 evidence；
- provider tests 覆盖 append tail、truncate、atomic replace、prefix mismatch、partial
  record 完成后的 full parse、parent/child inherited history，以及 native title、preview
  和 report evidence 语义；
- 这是行为保持型性能优化，base 的公开输出本来正确，因此没有伪造产品级 base
  failure；性能 base 由阶段 G changed-large benchmark 提供；
- focused tests 和完整仓库检查全部通过。

实现中否决了两版方案：

- 每次增量解析重新哈希完整 16 MiB 前缀，internal 约 13 ms，只改善约 65%，未达到
  70% 门槛；改用首尾边界指纹后才消除 O(file size) 校验；
- 为所有 rollout 持久化状态曾使 `WarmHistoryHeavy` 回退 `+5.20%`、cache bytes
  增加 `+53.83%`；最终增加 1 MiB 门槛并将状态从每-entry 字段改为稀疏 side map，
  普通 cache bytes 恢复到阶段 G 水平。

最终 raw output：

- CLI：`/tmp/asm-tui-startup-eh/phase-h-final-cli.txt`；
- internal：`/tmp/asm-tui-startup-eh/phase-h-final-internal.txt`；
- 相对 G：`/tmp/asm-tui-startup-eh/phase-h-vs-g-cli.txt` 和
  `/tmp/asm-tui-startup-eh/phase-h-vs-g-internal.txt`；
- 相对 D：`/tmp/asm-tui-startup-eh/phase-h-vs-d-cli.txt`。

最终 10 样本结果：

| 场景 | G | H | 变化 |
|---|---:|---:|---:|
| CLI ChangedLargeCodexSession | 63.194 ms | 6.467 ms | -89.77% |
| Codex DiscoverChangedLargeSession | 38.159 ms | 0.899 ms | -97.64% |
| Codex changed-large bytes | 40.25 MiB | 276.8 KiB | -99.33% |
| Codex changed-large allocs | 1069.5 | 199 | -81.39% |

其余 CLI wall time 相对 G 最大变化为 `WarmHistoryHeavy +2.40%`，cache bytes 无显著
变化，fixture session 数全部相同；相对 D 的 `WarmHistoryHeavy` 仍为约 `-41%`。
Codex cold/hot 分别 `-2.26%`/`-2.02%`。Claude changed-large、CodeBuddy/Cursor
history/cold/hot、index 和 UI 均无 5% 回退。sessioncache 核心 history load 和
single-entry save 无显著回退；部分 shard-count update microbenchmark wall time 为
`+5.88%`/`+6.65%`，但 bytes/allocs 不变，公开 CLI 无对应回退。

Sibling provider 决策：Claude、CodeBuddy、Cursor 也读取 appendable JSONL 并在文件
identity 改变后 full parse，但 parser state 和继承/preview 规则各不相同。Claude
changed-large 相对 G 无显著变化，本阶段不扩范围；CodeBuddy/Cursor 没有 changed-large
证据证明达到实施门槛，后续应先补 benchmark。Kimi、Kiro、opencode、OpenClaw、
ZCode 分别使用 compact state/index、小 metadata/消息文件或 SQLite，不具备相同的
大型 primary JSONL 启动瓶颈。阶段 H 不创建共享 incremental parser abstraction。

## 真实环境安装验证

2026-08-07 在同一台开发机的真实 provider stores 上，先测量已安装的阶段 D 生产
基线 `53dd1e7`，再从干净工作树构建并安装 PR commit `249b50f`。验证使用 Linux
`6.6.92-34.1.tl4.x86_64`、32 CPU、Go 1.26.5 和默认最近 30 天窗口。

为覆盖完整 discovery 而不进入交互 TUI，每次调用公共 CLI
`asm --resume __asm_real_startup_probe_missing__`，预期以“session 不存在”退出。冷启动
测量 10 次，每轮先把默认 cache 移到任务隔离目录，从空 cache 开始；热启动先预热
2 次，再测量 20 次。使用 `date +%s%N` 包围真实安装二进制调用，并以固定版本
`benchstat@v0.0.0-20260709024250-82a0b07e230d` 比较。原始计时和可恢复 cache 备份仅
保存在任务隔离临时目录，不提交真实 title、cwd、session ID 或 transcript。

聚合正确性前后完全一致：均发现 730 个 session、86 个 project、0 个 provider
error；排序后的 `{provider,id}` 集合和 `{cwd,count}` project 集合哈希分别一致，
provider 数量也逐项一致：Codex 286、Claude 183、ZCode 68、CodeBuddy 58、Kiro 58、
Cursor 55、Kimi 22；opencode 和 OpenClaw 在本次默认窗口内均为 0。该验证只公开聚合
值和不可逆哈希，不公开会话内容。

| 场景 | Before | After | 变化 |
|---|---:|---:|---:|
| 冷启动（每轮空 cache，n=10） | 32.07 s ±2% | 30.70 s ±1% | -4.26%（p=0.000） |
| 热启动（预热后，n=20） | 2.746 s ±2% | 2.710 s ±1% | -1.31%（p=0.000） |
| 默认 cache 大小 | 5,455,003 bytes | 1,084,420 bytes | -80.12% |

冷启动中位数由 32.065 s 降至 30.698 s，均值由 32.184 s 降至 30.701 s；热启动
中位数由 2.746 s 降至 2.710 s，均值由 2.767 s 降至 2.708 s。两项 wall time
变化均达到统计显著。完整分布摘要如下：

| 场景 | 版本 | min | median | mean | p95 |
|---|---|---:|---:|---:|---:|
| 冷启动 | Before | 31.373 s | 32.065 s | 32.184 s | 34.310 s |
| 冷启动 | After | 30.327 s | 30.698 s | 30.701 s | 31.001 s |
| 热启动 | Before | 2.711 s | 2.746 s | 2.767 s | 2.825 s |
| 热启动 | After | 2.679 s | 2.710 s | 2.708 s | 2.737 s |

真实整体收益小于 history-heavy、oversized-title 和 changed-large fixture 的专项
收益。当前真实启动同时包含多个 provider 的 native store 枚举、文件筛选和解析，
因此不能把整体改善归因于单一 provider；结果表明 cache 体积和 cache 内热点已明显
改善，但真实端到端启动仍主要受 cache 外成本支配。专项 fixture 与真实环境验证测量
不同工作负载，结论互相补充而不互相替代。真实环境的较小百分比不否定阶段 E-H：
专项 benchmark 已证明目标瓶颈被消除，真实验证又证明安装后正确性不变、冷/热启动
均有统计显著改善；它同时为下一轮继续降低端到端冷/热启动时间提供了基线。

后续性能 PR 应复用本节协议，但不要直接把未来绝对时间与本次不断变化的真实 stores
做纵向比较。应在同一次验证中，用当时的 `main` 和候选版本对同一份 stores 交替测量：

- 保持默认 30 天窗口和相同公共 CLI 探针；
- 冷启动每轮从空 cache 开始，至少 10 个样本；热启动预热 2 次，至少 20 个样本；
- 同时报告 benchstat 变化、median、mean、p95 和 cache bytes；
- 对比 session/project/provider 聚合计数与不可逆集合哈希，确保结果语义不变；
- 增加 provider/file-discovery 分解数据定位耗时，但仍以公共 CLI 冷/热 wall time 作为
  最终用户可见验收指标。

仓库脚本可自动执行单版本协议并生成聚合报告：

```sh
go build -o /tmp/asm-startup ./cmd/asm
python3 scripts/benchmark-real-startup.py \
  --asm-bin /tmp/asm-startup \
  --revision "$(git rev-parse HEAD)"
```

默认参数就是冷启动 10 次、预热 2 次、热启动 20 次和最近 30 天。脚本使用临时
`XDG_CACHE_HOME`，不会移动或读取用户现有 asm cache；原始 session JSON 和任务 cache
在进程结束时自动清理，只保留计时、cache bytes、聚合数量和不可逆哈希。诊断构建若
支持 `ASM_STARTUP_DIAG_FILE` 聚合事件，可额外传 `--diagnostics` 生成 provider/stage
摘要；该模式包含计时开销，不能替代无诊断的公共 CLI wall-time 验收。

## 2026-08-07 当前 master 真实分解诊断

本轮评测对象为 `origin/master@8cc4af8`，继续使用默认最近 30 天、公共 missing-session
resume 探针和隔离 `XDG_CACHE_HOME`。冷启动每轮使用全新空 cache，共 10 个样本；
热启动预热 2 次后采集 20 个样本。无诊断的公共 CLI wall time 如下：

| 场景 | n | min | median | mean | p95 | max | stdev | CV |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| 冷启动 | 10 | 30.905 s | 31.265 s | 31.684 s | 33.879 s | 33.879 s | 0.923 s | 2.91% |
| 热启动 | 20 | 2.670 s | 2.717 s | 2.718 s | 2.749 s | 2.765 s | 0.025 s | 0.91% |

固定版本 benchstat 摘要分别为 `31.27 s ±5%` 和 `2.717 s ±1%`。冷 cache 中位数
1,088,368 bytes，范围 1,087,752–1,088,368 bytes；热 cache 为 1,088,417 bytes。

真实 store 在评测过程中继续增长，因此只比较同一时点连续执行的冷/热正确性快照。
两次结果均为 737 个 session、86 个 project、0 个 provider error；provider 数量逐项
一致：Codex 293、Claude 183、ZCode 68、CodeBuddy 58、Kiro 58、Cursor 55、Kimi 22，
opencode 和 OpenClaw 为 0。不可逆集合哈希也一致：

- session `{provider,id}` SHA-256：
  `23cc5ec82234f652768d7de41c126a606985eb50fd348f61d18bb915c13f28b6`；
- project `{cwd,count}` SHA-256：
  `4f8b3311b1e94043825028c8732b2ee19f3263c3da0634d9cefe3bd48b7dd83a`。

临时诊断构建只记录 `{provider,stage,nanos,count}` 聚合事件。下表为 provider 总耗时
中位数/p95；阶段单元格为“中位耗时 / provider 总耗时占比”。Cache R 包含 manifest
和 lazy shard lookup，Cache W 包含更新与保存。

### 冷启动 provider/stage

| Provider | 总计 / p95 | 枚举筛选 | Primary parse | Dynamic | Cache R | Cache W | CWD |
|---|---:|---:|---:|---:|---:|---:|---:|
| Codex | 30998 / 31560 ms | 26.4 / 0.08% | 30901 / 99.69% | 54.6 / 0.18% | 1.4 / <0.01% | 8.9 / 0.03% | 0.57 / <0.01% |
| Claude | 2945 / 2982 ms | 6.4 / 0.22% | 2926 / 99.40% | 0 | 0.30 / 0.01% | 8.8 / 0.30% | 0.54 / 0.02% |
| Cursor | 238.7 / 247.3 ms | 29.7 / 12.54% | 208.1 / 86.76% | 0 | 0.05 | 1.07 / 0.46% | 0.09 |
| CodeBuddy | 190.9 / 195.6 ms | 17.2 / 9.04% | 169.7 / 88.82% | 0 | 0.06 | 3.18 / 1.65% | 0.02 |
| Kiro | 190.0 / 198.2 ms | 0.59 / 0.31% | 186.8 / 98.25% | 0.12 | 0.06 | 1.60 / 0.83% | 0.05 |
| ZCode | 8.19 / 11.46 ms | 0.01 | 7.39 / 89.33% | 0 | 0 | 0 | 0.04 |
| Kimi | 1.59 / 3.84 ms | 0.84 / 53.96% | 0.55 / 35.06% | 0 | 0 | 0 | 0.02 |
| opencode | 0.16 / 0.29 ms | 0.13 / 78.48% | 0 | 0 | 0 | 0 | 0 |
| OpenClaw | 0.07 / 0.12 ms | 0.02 / 22.88% | 0 | 0 | 0 | 0 | 0 |

### 热启动 provider/stage

| Provider | 总计 / p95 | 枚举筛选 | Primary parse | Dynamic | Cache R | Cache W | CWD |
|---|---:|---:|---:|---:|---:|---:|---:|
| Codex | 2689.5 / 2731.2 ms | 23.7 / 0.89% | 2614.5 / 97.27% | 36.4 / 1.37% | 9.34 / 0.35% | 0.49 / 0.02% | 0.36 |
| Cursor | 27.15 / 28.29 ms | 25.81 / 96.27% | 0 | 0 | 0.77 / 2.87% | <0.01 | 0.04 |
| Claude | 20.53 / 21.92 ms | 5.41 / 26.55% | 5.98 / 28.72% | 0 | 8.23 / 40.42% | <0.01 | 0.24 |
| CodeBuddy | 18.16 / 19.17 ms | 16.23 / 89.57% | 0.10 | 0 | 1.59 / 8.85% | <0.01 | 0.01 |
| ZCode | 6.79 / 7.25 ms | 0.01 | 6.13 / 90.33% | 0 | 0 | 0 | 0.03 |
| Kiro | 2.11 / 3.49 ms | 0.59 / 28.25% | 0 | 0.10 / 4.84% | 1.17 / 57.20% | <0.01 | 0.02 |
| Kimi | 1.57 / 1.77 ms | 0.84 / 52.27% | 0.56 / 35.85% | 0 | 0 | 0 | 0.02 |
| opencode | 0.19 / 0.59 ms | 0.12 / 65.29% | 0 | 0 | 0 | 0 | 0 |
| OpenClaw | 0.08 / 0.14 ms | 0.02 / 22.53% | 0 | 0 | 0 | 0 | 0 |

### 已验证瓶颈

热启动中，Codex 每轮约有 780 个候选文件、522 个 cache hit 和 258 个 miss。进一步
只读聚合确认，其中 257 个文件对应 257 个唯一 session，与已成功发现的 ID 零重叠；
首条 `session_meta.source` 是 producer 持久化的对象形态 `{"subagent": ...}`，而当前
解析器要求字符串，导致整条 metadata 反序列化失败。这 257 个文件合计 158,313,074
bytes，每次热启动重复解析约 2.61 秒，同时造成 session 漏发现。不能用负缓存或默认
隐藏固化当前错误行为，应先修复 schema 兼容并缓存成功结果。

冷启动顺序解析约 1.96 GB Codex rollout。304 个不小于 1 MiB 的有效文件合计约
1.69 GB，贡献约 24.81/29.21 秒，即约 85% 的 parse 时间。只读单样本 worker 上限
实验保持 valid/missing/error 数量逐项一致：

| Worker | Parse 时间 | 相对 1 worker |
|---:|---:|---:|
| 1 | 32.02 s | 基线 |
| 2 | 18.21 s | -43.1% |
| 4 | 13.17 s | -58.9% |
| 8 | 10.27 s | -67.9% |
| 16 | 7.26 s | -77.3% |
| 32 | 6.12 s | -80.9% |

该实验只量化 parser 并行上限，不是正式 after 验收；首个实现候选应从有界 8 worker
开始，并用内存、race、顺序语义和公共 CLI 真实 A/B 决定是否保留。当前可见 Codex
title 中，216 个来自 `session_index`、73 个来自 `history`、4 个来自 rollout；98.6%
不依赖 rollout title，这为更晚的 metadata/turn-context 快路径提供依据，但其正确性
风险高于有界并行解析。

## P0：兼容 Codex subagent source schema

### 契约与实现

- 测试提交：`cd804de`；生产实现：`dd8642b`；A/B 的生产 base 与测试提交相同，
  相对其父提交只增加测试和 benchmark；
- `session_meta.source` 的历史字符串继续原样映射到 `entrypoint`；含 `subagent` tag 的
  对象统一映射为 `entrypoint=subagent`；未知对象不设置 entrypoint，但不能使 `id`、
  `cwd`、timestamp、parent 或其他 metadata 丢失；
- subagent 本身不作为隐藏条件。非交互分类仍只使用字符串 `source=exec` 或稳定的
  `originator=codex_exec`；
- `sessioncache.Version` 从 6 升至 7。旧解析器可能把 child rollout 中后续继承的 parent
  metadata 缓存为该文件的 session，因此不能继续复用 version 6；旧 cache 安全退化为
  native store 重建，不删除用户 cache；
- provider fixture 覆盖字符串、`subagent=review`、`thread_spawn`、`other`、未知对象、
  无后续 child metadata、parent boundary、exec 分类、cold/warm parity、primary
  invalidation，以及 title/cwd/model/parent；公共 CLI 同时锁定 parent/child JSON、
  resume command 和 report 只计 parent user work。

base 上的失败是产品断言失败：provider cold discovery 对对象 source 返回 0；公共 CLI
漏掉 child，并把 child 文件中继承的 parent metadata 误归属为该文件的会话。修复后相同
测试通过。完整 focused provider/cache/index/UI/report/cmd/CLI 矩阵以及仓库 performance
contract、lint、全量测试、build、pre-commit 均通过。

### Focused benchmark

`BenchmarkDiscoverObjectSourceWarmCache` 使用 producer-compatible 的对象 source 和
16 MiB assistant payload；先 discovery 一次，再重复相同 warm discovery。相同 benchmark
在 base/after 各运行 10 个样本、`benchtime=1s`：

| 指标 | Base `cd804de` | After `dd8642b` | 变化 |
|---|---:|---:|---:|
| wall time | 256.28 ms | 86.69 us | -99.97% |
| B/op | 147330.8 KiB | 9.21 KiB | -99.99% |
| allocs/op | 5496 | 111 | -97.98% |
| sessions/op | 0 | 1 | 修复漏发现并命中 cache |

raw output：

- `/tmp/asm-tui-startup-p0-20260807/object-source-before.txt`；
- `/tmp/asm-tui-startup-p0-20260807/object-source-after.txt`；
- `/tmp/asm-tui-startup-p0-20260807/object-source-benchstat.txt`。

### 真实冷/热 A/B

环境为 Go 1.26.5、Linux `6.6.92-34.1.tl4.x86_64`、AMD EPYC 7K62、32 CPU；
base/after 均用 `-buildvcs=false` 构建。公共 runner 使用默认最近 30 天和 missing-session
resume 探针；每个版本冷启动 10 次、热预热 2 次后采样 20 次，cache 均位于隔离的
临时 `XDG_CACHE_HOME`。固定
`benchstat@v0.0.0-20260709024250-82a0b07e230d` 的结论为：

| 场景 | 版本 | min | median | mean | p95 | max |
|---|---|---:|---:|---:|---:|---:|
| 冷启动 | Base | 31.366 s | 31.684 s | 31.755 s | 32.518 s | 32.518 s |
| 冷启动 | After | 22.937 s | 23.278 s | 23.234 s | 23.348 s | 23.348 s |
| 热启动 | Base | 2.654 s | 2.694 s | 2.696 s | 2.730 s | 2.762 s |
| 热启动 | After | 0.080 s | 0.082 s | 0.083 s | 0.086 s | 0.088 s |

benchstat：冷启动 `-26.53%`（p=0.000，n=10），热启动 `-96.94%`
（p=0.000，n=20）。base 冷 cache 中位数 1,115,549 bytes，after 为
1,287,145 bytes（`+15.38%`）；热 cache 从 1,119,714 增至 1,287,145 bytes
（`+14.95%`）。增长来自 447 个原本漏发现、现已正确持久化的目标 session，不能以
维持较小 cache 为由回退修复。

runner 内部正确性在两个版本各自 cold/warm 一致且 provider error 为 0：base 为
753 sessions/87 projects，Codex 295；after 为 1200 sessions/89 projects，Codex 742；
其他 provider 数量逐项不变。完整集合哈希为：

| 版本 | `{provider,id}` SHA-256 | `{cwd,count}` SHA-256 |
|---|---|---|
| Base | `5f55928023e26a810d804a1e41d51b38508a9f56663f37510f275feaca40e526` | `92b59806f83799d07a1a0e8900c7b7713e78395b255a58785c1dd6c001a40bc4` |
| After | `70cd3aab51f23801a09bf3f37843e86de12ba6d06b0757caff4ebb3b02e67ead` | `e666dbca3c5302a4958f5bccdb0d7c254499416d7b8503cf0067de2f6420d25e` |

由于本项有意修复漏发现，另用紧邻 base/after JSON 快照验证集合差异：新增 447、移除
0、意外新增 0；全部新增 session 都是 `entrypoint=subagent` 且带 parent。排除对象
source 目标后，两边均为 752 个 session，非目标 session 哈希同为
`966cf74a0a88413bb4f3c0ccd4b585227954e9ee70351ef310e73e9716bfde04`，非目标 project
哈希同为 `92b59806f83799d07a1a0e8900c7b7713e78395b255a58785c1dd6c001a40bc4`。
真实 store 在 runner 与紧邻审计间新增了一个非目标 session，因此两组绝对计数相差 1，
但各自 base/after 的目标增量和非目标哈希都闭合。额外审计的原始 JSON 和 cache 已覆盖
删除，只保留聚合值和不可逆哈希。

真实 raw output：

- `/tmp/asm-tui-startup-p0-20260807/real-base/`；
- `/tmp/asm-tui-startup-p0-20260807/real-after/`；
- `/tmp/asm-tui-startup-p0-20260807/real-benchstat.txt`；
- `/tmp/asm-tui-startup-p0-20260807/correctness-audit.txt`；
- `/tmp/asm-tui-startup-p0-20260807/added-metadata-audit.json`。

### Sibling provider 影响分类

| Provider | 实际读取路径 | 结论 |
|---|---|---|
| Codex | rollout `session_meta.source` | affected；字符串类型假设使整个 metadata 失效，本项修复 |
| Claude | project JSONL 顶层 `entrypoint`/`promptSource` 字符串 | not affected；没有 Codex source enum |
| CodeBuddy | project JSONL 的 session/message/providerData | not affected；没有 session_meta/source |
| Cursor | transcript role/message/content | not affected；ID/CWD 来自路径解析，没有 source 字段 |
| Kimi | compact `session_index.jsonl` + `state.json` | not affected；存储模型不同 |
| Kiro | metadata JSON + Prompt JSONL | not affected；parent/reason 为独立字段 |
| opencode | session/project/message/part JSON | not affected；存储模型不同 |
| OpenClaw | compact sessions index，`origin` 已用 `json.RawMessage` | not affected；可变对象不会使主记录失效 |
| ZCode | SQLite session/message/part 查询 | not affected；无 JSON source enum |

cache version 是共享的，因此 Codex、Claude、Kiro、opencode、CodeBuddy、Cursor 会发生
一次性 cold rebuild；它们的 focused cold/warm、invalidation 和公共 CLI 测试均通过。
P0 决定保留。P1 不在本项中实施，必须在 P0 合并后的主线上重新采集独立 base/after。

## P1：Codex cache miss 有界并行解析

### 契约与实现

- P1 base 为最新 `origin/master@b6b2a23`，测试/benchmark 提交为 `99e1ab4`，生产实现
  提交为 `3e1e154`；本节没有复用 P0 after；
- 公共行为本来正确，因此 base 上新增的 CLI 合同通过，不把编译失败或人为断言当成产品
  failure。实现前先提交该公共合同和混合 rollout benchmark，再写串行/并行等价测试；
- 默认最多 8 个 worker。主 goroutine 继续按 newest-first 文件顺序串行执行 cache
  `Get`/`GetLatest`，worker 只解析彼此独立的 primary file；结果按原文件 index 汇总后，
  再串行执行 cache `Put`/`Keep`/`Save`、重复 ID 去重、dynamic title、cwd 和 preview；
- 等价测试覆盖 1/8 worker cold、warm、cache parity、解析失败、重复 ID、parent/child
  inherited-history boundary、native dynamic title、增量 state、newest-first 和 limit；
- 公共 CLI fixture 使用超过默认 worker 数量的 producer-compatible 对象 source rollout，
  锁定 cold/warm JSON、session ordering、project grouping、provider error、missing-cwd 和
  resume safety；
- cache 格式和 version 没有变化，其他 provider 不承担 worker 或持久化兼容成本。

### Focused benchmark、worker sweep 与内存

`BenchmarkDiscoverColdCacheMixedRollouts` 使用 24 个 64 KiB、256 KiB、1 MiB、4 MiB
混合 rollout，包含对象 source、parent metadata、turn context 和 assistant payload；每档
10 个样本、`benchtime=1x`。相同 benchmark 在 P1 base 和默认 8-worker after 上结果为：

| 指标 | Base `99e1ab4` | After `3e1e154` | 变化 |
|---|---:|---:|---:|
| wall time | 577.5 ms ±2% | 103.6 ms ±14% | -82.07%（p=0.000） |
| B/op | 230.1 MiB | 230.1 MiB | +0.01% |
| allocs/op | 3.651k | 3.688k | +1.00% |

同一 after 的 worker sweep：

| Worker | sec/op | 相对 1 worker | B/op | allocs/op | 峰值 RSS |
|---:|---:|---:|---:|---:|---:|
| 1 | 551.6 ms ±7% | 基线 | 230.1 MiB | 3.658k | 26.8 MiB |
| 2 | 291.9 ms ±4% | -47.08% | 230.1 MiB | 3.692k | 29.9 MiB |
| 4 | 168.5 ms ±6% | -69.45% | 230.1 MiB | 3.690k | 50.6 MiB |
| 8 | 97.30 ms ±6% | -82.36% | 230.1 MiB | 3.690k | 77.7 MiB |
| 16 | 87.75 ms ±19% | -84.09% | 230.1 MiB | 3.702k | 112.0 MiB |

8 到 16 worker 只再改善约 9.8%，峰值 RSS 却增加约 44.2%，且 16-worker 分布更不稳定，
因此保留 8。B/op 是完成同一解析工作的累计分配量，worker 数不会减少它；真实冷启动
单样本峰值 RSS 从 base 49.9 MiB 增至 after 71.1 MiB（+21.2 MiB），绝对值可接受，
并与下节 p95 收益一并作为保留依据。

raw output：

- `/tmp/asm-tui-startup-p1-20260807/mixed-base.txt`、`mixed-after.txt` 和
  `mixed-benchstat.txt`；
- `/tmp/asm-tui-startup-p1-20260807/worker-sweep-final.txt`、
  `worker-sweep-summary.txt`、`worker-rss.txt`；
- `/tmp/asm-tui-startup-p1-20260807/real-rss.txt`。

### 独立真实冷/热 A/B

环境为 Go 1.26.5、Linux `6.6.92-34.1.tl4.x86_64`、AMD EPYC 7K62、32 CPU；base
`b6b2a23` 和 after `3e1e154` 均用 `-buildvcs=false` 构建。公共 runner 使用默认最近
30 天和 missing-session resume 探针；每个版本冷启动 10 次，热态预热 2 次后采集
20 次，并使用隔离临时 `XDG_CACHE_HOME`。这是 P1 独立 base/after：

| 场景 | 版本 | min | median | mean | p95 | max |
|---|---|---:|---:|---:|---:|---:|
| 冷启动 | Base | 23.004 s | 23.246 s | 23.296 s | 23.620 s | 23.620 s |
| 冷启动 | After | 5.383 s | 5.476 s | 5.499 s | 5.684 s | 5.684 s |
| 热启动 | Base | 0.079 s | 0.080 s | 0.090 s | 0.157 s | 0.163 s |
| 热启动 | After | 0.080 s | 0.082 s | 0.086 s | 0.086 s | 0.166 s |

固定 `benchstat@v0.0.0-20260709024250-82a0b07e230d`：冷启动 `-76.44%`
（p=0.000，n=10）；热启动无显著变化（p=0.121，n=20）。冷 cache 中位数从
1,291,054 bytes 到 1,291,414 bytes，热 cache 均为 1,291,414 bytes；实现没有 cache
schema 或持久化体积变化。

base/after 的 cold/warm 各自一致，且跨版本也逐项一致：1207 个 session、89 个 project、
0 个 provider error；provider 数量为 Codex 749、Claude 184、CodeBuddy 70、ZCode 68、
Kiro 58、Cursor 55、Kimi 23，opencode/OpenClaw 为 0。不可逆哈希为：

- session `{provider,id}` SHA-256：
  `8f09c5ad967d545ef89156ae0ad172409d770c901c3fb5e05c49c74713010adf`；
- project `{cwd,count}` SHA-256：
  `e2292fdce6324d469339beac4865c70256d8cfaf2e70985881e83ff30f4e8753`。

真实 raw output：

- `/tmp/asm-tui-startup-p1-20260807/real-base/`；
- `/tmp/asm-tui-startup-p1-20260807/real-after/`；
- `/tmp/asm-tui-startup-p1-20260807/real-benchstat.txt`。

### Cross-agent 影响分类

| Provider | Primary store 与共享机制 | 触发可达性与结论 |
|---|---|---|
| Codex | cache miss 解析 per-session rollout JSONL | affected；真实冷启动解析约 1.96 GB，本项修复 |
| Claude | cache miss 解析 per-session project JSONL | 机制可达，但格式/preview 规则独立，当前真实成本远低于 Codex；不扩 P1 |
| CodeBuddy | cache miss 解析 project JSONL | 机制可达，但真实冷成本约百毫秒量级；无实施门槛证据 |
| Cursor | cache miss 解析 agent transcript JSONL | 机制可达，但 cwd/preview 规则独立且真实冷成本约百毫秒量级；不共享 worker |
| Kiro | cache miss 解析小型 metadata JSON | 大型 rollout 触发不可达；动态 Prompt 仍串行重读 |
| opencode | cache miss 解析小型 session JSON | 大型 rollout 触发不可达；project/message 是动态 side input |
| Kimi | compact index + per-session state，无 sessioncache | 不受影响 |
| OpenClaw | compact `sessions.json` index，无 per-session transcript parse | 不受影响 |
| ZCode | SQLite indexed queries | 不受影响 |

当前变更的 contributor scope 只需 Codex 正确性、确定性和资源证据，已完成；没有为其他
provider 建立不安全的共享抽象。维护者 follow-up 继续沿用现有门槛：只有某个 JSONL
provider 在真实关键路径成为主导且补齐 changed-large benchmark 后，才为其独立设计并行
解析。本项不进入 P2 metadata/turn-context 快路径。

全仓 `go test -race ./...` 通过；focused provider/CLI、完整仓库门禁和 autoreview 结果
随本项最终提交一并验收。P1 决定保留，P2 未开始。

## P2：Codex metadata/turn-context 快路径

### 启动门槛与独立 base

- P2 基于 P1 合并后的 `origin/master@07243f0`；测试/benchmark 提交为 `f6c2f9d`，
  生产实现为 `a433aab`，没有复用 P1 after；
- 首次公共 base 为冷启动 10 次、热预热 2 次后 20 次：冷中位数/p95
  `5.533/5.625 s`，热中位数 `81.70 ms`；1209 sessions、90 projects、0 error，
  冷热哈希一致；
- 同一 base 的 Codex-only 空 cache 冷启动 10 次中位数/p95 为 `5.490/5.610 s`，
  约占完整冷启动中位数 99.2%。单次 `strace` 只记录长度、不保留 transcript 内容，
  得到 1,489,266,097 bytes 正向 read 返回；符合 producer schema 的 24-file 大小混合
  benchmark 每次输入约 33.44 MB，却分配约 230.1 MiB。由此确认 P1 后真实关键路径仍由
  Codex 大 rollout 深度解析主导，P2 门槛成立；
- 真实 store 在首次 base 与 after 间新增 1 个 Codex session。为避免把 producer 增长
  当成语义差异，又在当前 1210-session 集合上独立重采 base2；下文正式 A/B 使用
  base2 和 after，二者聚合与哈希完全一致。

门槛 raw output：

- `/tmp/asm-tui-startup-p2-20260807/real-base/`；
- `/tmp/asm-tui-startup-p2-20260807/codex-base-time-rss.txt`；
- `/tmp/asm-tui-startup-p2-20260807/codex-base-read.strace`；
- `/tmp/asm-tui-startup-p2-20260807/mixed-base.txt`。

### 契约与实现

- 仅 `DiscoverOptions.Preview.Enabled()==false` 使用 metadata 快路径。它仍顺序读取完整
  rollout，但只完整解码 `session_meta`、`turn_context` 和 user `response_item`；由
  producer header 明确证明为 assistant/tool/无关类型的记录边读边丢弃，不再保留和
  深度解码大型 payload。header 不完整、字段顺序变化或类型不明确时保守回退完整解码；
- 快路径继续提取最新 cwd/model、对象或字符串 source、parent boundary 和 rollout
  title fallback；随后仍动态应用 `session_index > history > rollout` title 优先级、
  cwd status、去重、newest-first 和 limit；P1 的 8-worker 有界并行保持不变；
- cache 中用 provider-private、输出前删除的 parse-mode 标记区分快路径结果。普通
  discovery 可以复用 full cache；report 若遇到 metadata-only cache 会重做完整 primary
  parse，随后继续走原有 user-preview evidence reader。cache schema/version 不变，旧 cache
  仍被视为完整解析结果；
- 公共 CLI 测试先建立普通 cold/warm cache，再运行 report，锁定最新 turn context、对象
  source、native title、项目聚合和两条 evidence。Provider 测试覆盖 full/metadata
  等价、changed header order、assistant 内容伪装、parent inherited-history boundary、
  append-boundary rewrite，以及既有 append、truncate、atomic replace、prefix rewrite、
  partial/oversized JSONL、cache invalidation、duplicate ID、parse failure、dynamic inputs、
  missing cwd、newest-first 和 limit 矩阵；
- 这是行为保持型性能优化，base 的公共输出本来正确，因此没有伪造产品级失败；新增
  public contract 在 base 和实现上均通过，性能缺口由独立真实 base 与 benchmark 证明。

### Focused 性能与资源

`BenchmarkDiscoverColdCacheMixedRollouts` 使用 24 个 64 KiB、256 KiB、1 MiB、4 MiB
混合 rollout，包含对象 source、parent metadata、turn context 和 assistant payload；
每档 10 个样本、`benchtime=1x`：

| 指标 | Base `f6c2f9d` | After `a433aab` | 变化 |
|---|---:|---:|---:|
| wall time | 103.182 ms | 5.473 ms | -94.70%（p=0.000） |
| rollout input | 31.89 MiB/op | 31.89 MiB/op | 无变化 |
| B/op | 230.105 MiB | 7.955 MiB | -96.54% |
| allocs/op | 3.703k | 7.879k | +112.80% |

allocs/op 增加来自保守 header token 的小对象；累计分配 bytes、真实峰值 RSS 和 wall time
均显著下降，因此保留该权衡。Codex-only 10 次真实冷测中，中位数从 `5.490 s` 降至
`2.495 s`（约 -54.6%）；峰值 RSS 中位数从 67.47 MiB 降至 50.42 MiB（-25.27%），
p95 从 71.29 MiB 降至 53.71 MiB（-24.65%）。after `strace` 为
1,496,262,884 bytes；它比 base 多约 7 MB，来自评测期间真实 store 增长。稳定 fixture
的 31.89 MiB/op 输入完全不变，说明收益不是减少读取、降低 title 上限或 evidence。

focused raw output：

- `/tmp/asm-tui-startup-p2-20260807/mixed-after.txt`；
- `/tmp/asm-tui-startup-p2-20260807/mixed-benchstat.txt`；
- `/tmp/asm-tui-startup-p2-20260807/codex-after-time-rss.txt`；
- `/tmp/asm-tui-startup-p2-20260807/codex-after-read.strace`。

### 正式真实冷/热 A/B 与 report 哈希

环境为 Go 1.26.5、Linux `6.6.92-34.1.tl4.x86_64`、AMD EPYC 7K62、32 CPU；base
`07243f0` 与 after `a433aab` 均用 `-buildvcs=false` 构建。公共 runner 使用默认最近
30 天和 missing-session resume 探针；每个版本冷启动 10 次，热态预热 2 次后采集
20 次，并使用隔离临时 `XDG_CACHE_HOME`：

| 场景 | 版本 | min | median | mean | p95 | max |
|---|---|---:|---:|---:|---:|---:|
| 冷启动 | Base | 5.440 s | 5.540 s | 5.531 s | 5.601 s | 5.601 s |
| 冷启动 | After | 3.931 s | 3.951 s | 4.067 s | 4.567 s | 4.567 s |
| 热启动 | Base | 77.87 ms | 80.50 ms | 80.94 ms | 84.93 ms | 85.58 ms |
| 热启动 | After | 80.02 ms | 81.73 ms | 83.45 ms | 93.97 ms | 99.13 ms |

固定 benchstat：冷启动 `-28.68%`（p=0.000，n=10），冷 p95 `-18.45%`；原始
20-sample 热测为 `+1.53%`（低于 5% 门槛）。为复核 after 热样本尾部噪声，又在分别
预热的独立 cache 上交替执行 base/after 各 30 次，结果 `81.55/82.37 ms`，p=0.150，
无显著变化。冷 cache 中位数从 1,295,718 增至 1,322,936 bytes（+2.10%），来自
provider-private parse-mode 标记；该标记不进入公共 JSON。

base/after 的 cold/warm 各自一致，跨版本也逐项一致：1210 sessions、90 projects、
0 provider error；provider 数量为 Codex 752、Claude 184、CodeBuddy 70、ZCode 68、
Kiro 58、Cursor 55、Kimi 23，opencode/OpenClaw 为 0。不可逆哈希为：

- session `{provider,id}` SHA-256：
  `37ffcc5872d32357534da004342d0118c1c1c216e94df9cc7625ea1a395fcaa9`；
- project `{cwd,count}` SHA-256：
  `933344b4d062212526e38c5d63eb48bb3bf8a3984509c8baf48ff0299ea7c4fd`。

`asm report --period last-week` 在独立 base/after cache 上均为 125 sessions、26 projects、
311 条 evidence、0 provider error；evidence SHA-256 均为
`b507cb6b9ac9dd97103466c40f22f9c2c85154c50217a7a4f9beefa78de72180`，去除
evidence/previews 后的完整聚合 SHA-256 均为
`b5ebdd20a17da2a4ef8035d730d1a72407e4cd0b251652a101f19817169f7a4d`。report wall
time 为 `13.673/13.437 s`，符合 P2 不加速 report、但必须保持 evidence 的边界。

正式 raw output：

- `/tmp/asm-tui-startup-p2-20260807/real-base2/`；
- `/tmp/asm-tui-startup-p2-20260807/real-after/`；
- `/tmp/asm-tui-startup-p2-20260807/real-benchstat.txt`；
- `/tmp/asm-tui-startup-p2-20260807/warm-paired/`；
- `/tmp/asm-tui-startup-p2-20260807/report-base-hashes.json`；
- `/tmp/asm-tui-startup-p2-20260807/report-after-hashes.json`。

### Cross-agent 审查与决策

**Current PR correctness**：Codex 的根因是 primary parser 对每条 rollout 先保留并深度
解码整个 payload；新增 fast/full parity、cache/report E2E 和真实 A/B 证明目标机制已修复，
且 session、title、cwd/model、parent、grouping、resume safety 和 report evidence 不变。

**Cross-agent assessment**：

| Provider | 实际 primary store / parser | 触发可达性与结论 |
|---|---|---|
| Codex | per-session rollout JSONL | affected；真实约 1.5 GB，本项修复 |
| Claude | project JSONL，metadata/message 同记录 | 大型 assistant payload 可达，但字段/title/preview 规则独立；不共享 Codex fast path |
| CodeBuddy | project JSONL，providerData/message 同记录 | 大型 payload 可达，但 title/model/source 规则独立；本项不扩范围 |
| Cursor | agent transcript JSONL，ID/CWD 来自路径 | 大型 payload 可达，但首/末 user title 与 cwd 规则独立；本项不扩范围 |
| Kiro | 小型 primary metadata JSON + Prompt JSONL | Codex assistant/tool payload 触发不可达；Prompt 内容本身是 title/evidence |
| opencode | 小 session/project/message/part JSON | 拆分存储，无大型单条 primary payload |
| Kimi | compact index + `state.json` | 无 per-session transcript primary parse |
| OpenClaw | compact `sessions.json` index | 当前不解析 transcript，触发不可达 |
| ZCode | SQLite indexed queries | 无 JSONL primary parser，触发不可达 |

**Contributor action**：P2 只需保持 Codex 正确性、cache 模式边界和资源证据；已完成，
没有引入共享 parser 或要求其他 provider 承担 cache 兼容成本。

**Maintainer follow-up**：不在本任务实施 sibling 优化。P2 合并后若新的真实分解确认
Claude、CodeBuddy 或 Cursor 成为冷启动主导项，应分别先补 producer-schema 大文件
benchmark 与 public contract，再设计 provider-owned fast path；没有确认瓶颈前不创建
共享抽象。P3 dynamic title index 未开始。P2 决定保留。
