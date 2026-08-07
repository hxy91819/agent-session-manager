# 阶段 C：新 PR 怎么使用 code-health 报告

阶段 C 的目标是校准规则，不是提前阻断开发。至少收集 10 个已合并 PR 后，才决定
是否进入阶段 D 硬门禁。

最简单的使用方式是在仓库会话中调用：

```text
$code-health-review 分析当前 PR 的代码健康报告并给出建议
```

该 skill 会解析 CI artifact 或重新运行明确的 base/head 比较，区分本 PR 退化与
存量债务，并给出 `fix before merge`、`consider` 或 `accept during Stage C` 建议；
默认只分析，不修改代码或阈值。

## PR 打开或更新后

CI 会出现绿色的 `Code health report` check。进入该 check 的 Summary，可以直接看
文本结论；Artifacts 中的 `code-health-pr-<PR号>` 保存：

- `code-health.txt`：人读摘要；
- `code-health.json`：完整、稳定的 base/head 测量结果；
- `code-health-run.json`：PR、revision、耗时和计数元数据。

CI 明确比较 PR 的 base SHA 和 GitHub 实际测试的合并 SHA，并在元数据中另存 PR
head SHA。这样落后于 base 的分支不会把无关上游变化误判为本 PR 退化。报告中的
`would-fail` 不会让 check 失败；只有 revision 不可达、源码无法解析或分析器运行
失败才会失败。

## 维护者只需要做的判断

### `verdict: pass`

不需要额外动作，按普通测试和 review 结果决定是否合并。

### `verdict: would-fail (report only)`

查看 `VIOLATION` 项：

1. 如果是本 PR 引入的真实复杂化或重复，让 Agent 优先减少分支、状态共享、依赖
   范围或重复规则；不要只为过指标机械拆函数。
2. 重新 push 后看下一次报告。目标是在两轮内得到结构性修正。
3. 如果当前实现合理，阶段 C 仍允许合并，但必须记录为 `accepted-report`、
   `false-positive` 或 `exception`，不能添加整文件 `nolint` 来隐藏样本。

测试代码告警只用于了解分布，不要求修复。

## 每个 PR 留一份观察记录

让处理 PR 的 Agent 复制 `pr-observation-template.json` 为：

```text
docs/code-health/observations/pr-<PR号>.json
```

填写第一次和最终报告、修复轮数、CI 秒数、误报、潜在指标作弊和维护者结论。
字段契约由 `pr-observation.schema.json` 定义。

分类含义：

- `pass`：从未产生 would-fail；
- `fixed`：Agent 根据报告完成了结构性修正；
- `accepted-report`：报告真实，但阶段 C 决定暂时接受；
- `false-positive`：测量方法与实际可维护性不一致；
- `exception`：进入硬门禁后才使用，必须填写有期限的批准信息。

阶段 C 不要求你手工抄完整指标；从 artifact 的 `code-health-run.json` 复制计数和
耗时即可。完整证据仍保留在对应 workflow artifact 中 90 天。

## 10 个 PR 后做什么

汇总观察文件并回答：触发率、误报率、两轮内修复率、例外率、CI P95 耗时，以及
函数/KLOC、微型函数比例、package 文件数和修改文件数是否出现异常增长。只有这些
数据可接受，才把 `--enforce` 接入阶段 D；否则先调整或删除相应规则并开启新 series。
