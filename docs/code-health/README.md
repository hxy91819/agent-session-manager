# Code-health baseline artifacts

本目录保存 Agent 代码防腐实验 `2026-08-stage-b-v1` 的阶段 B 证据：

- `baseline.json`：revision `6c139c696984b28c63ec5afd3ab2dbaa53e9096d`
  的完整生产/测试源码快照和 base/head 比较结果；
- `history-40.json`：截至同一 revision，first-parent 历史中最近 40 个包含
  生产 Go 代码变化的提交的聚合快照、增长次数和反作弊趋势；
- `history-trend.md`：对 baseline、历史分布和阈值决定的可读总结。

阶段 C 的新 PR 使用方法见 `pr-workflow.md`。CI 会生成 report-only Summary 和
90 天 JSON artifact，不写 PR 评论、不阻断合并；每个 PR 的人工判断使用
`pr-observation-template.json` 和 `pr-observation.schema.json` 记录。

可复现命令：

```sh
go run ./tools/check-code-health \
  --base 6c139c696984b28c63ec5afd3ab2dbaa53e9096d \
  --head 6c139c696984b28c63ec5afd3ab2dbaa53e9096d \
  --format json

go run ./tools/check-code-health \
  --history 40 \
  --revision 6c139c696984b28c63ec5afd3ab2dbaa53e9096d \
  --format json
```

JSON 不包含生成时间；同一 revision、配置和依赖版本应产生字节一致的输出。
历史选择只沿 first-parent，且只纳入 `cmd/`、`internal/`、`tools/` 中有非测试、
非生成、非 vendor/testdata Go 文件变化的提交。测试文件始终单独统计。

阈值、指标定义、scope、schema series 和分析器版本的可执行 source of truth 是
`tools/check-code-health/config.json`。Go module checksum 锁定分析器实现，工具启动时
校验构建依赖版本与配置一致。默认运行是报告模式并返回 0；`--enforce` 只用于验证
已实现的棘轮契约，发现 hard violation 时返回 1。revision、解析或分析错误返回 2。

产物校验和：

```text
05649060f298f6a29a40409090862596921b64d0b6a2a19b5ec5fcbce8c24e4c  baseline.json
0b10f12e6ddfd7b094e4919135408c5f08f0df4065c59e4c86d64a71581ffdfd  history-40.json
```
