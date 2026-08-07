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
| E：Shared title policy | 待填 | 待填 | 待填 | 待填 |
| F：Cache 快速修复 | 待填 | 待填 | 待填 | 待填 |
| G：Cache 分片 | 待填 | 待填 | 待填 | 待填 |
| H：Codex 增量解析 | 待 benchmark 决策 | 待填 | 待填 | 待填 |
