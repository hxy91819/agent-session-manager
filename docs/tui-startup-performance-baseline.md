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
| E：Shared title policy | `1510a18` | oversized title 启动 `-86.32%`、cache `-96.77%`；原六场景最大变化 `+1.54%` | cache/index/UI 无 5% 回退；paired A/B 中 Codex changed-large 无显著变化、Claude `+5.81%` | 保留；Claude 项在 F/G 后复测 |
| F：Cache 快速修复 | 待填 | 待填 | 待填 | 待填 |
| G：Cache 分片 | 待填 | 待填 | 待填 | 待填 |
| H：Codex 增量解析 | 待 benchmark 决策 | 待填 | 待填 | 待填 |

## 阶段 E：Shared title policy

实现与契约：

- 维护者确认最多 512 rune、同时最多 2048 byte，`…` 计入上限，被截断尾部不再
  搜索；普通 title 保持原样；
- 生产实现提交：`42477c8`；避免缓存型 provider 重复归一化后的最终提交：
  `1510a18`；benchmark 补强提交：`68c26ca`；
- `sessioncache.Version` 从 5 更新到 6，旧 cache 安全退化为 miss 并从 native
  store 重建，不删除用户 cache；report evidence 和 preview 没有经过 title 截断。

公共行为证据：

- base `3719b4e` 上执行
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
`3719b4e`（临时 benchmark commit `f4bee01`）和最终实现，fixture correctness 在
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
