# Error Budget（SLO 错误预算）

agent 运行时维护一个滑动窗口（默认 20 次工具调用）的成功率统计，SLO 默认 85%（即失败预算 15%）：

- **纯统计，不再注入任何提示。** 早期版本在“快烧”（窗口内失败率 > 30%）时注入一次 `[error-budget] fast burn ...` 告警，在“预算耗尽”（失败率 > 15% 且失败 ≥ 6）时注入 `[error-budget] BUDGET EXHAUSTED ... ask the user` 并要求模型停止重试、请求人工裁决。这两段按错误计数触发的转向注入已按用户决定移除——**由 AI 自行判断是否停止重试**，而不是由计数器替它决定“该停下来问人”。
- 滑动窗口与 burn rate 仍被记录（`errorBudget.burnRate()`），供快照 / 内省使用；每个 run 重置。
- 同一批清理掉的还有两处早已没有调用方、同样属于“按计数转向”的死代码：`[trajectory confidence: N/100] ... N consecutive errors detected`（`confidenceState.maybeIntervene`）和 `[Systemic Failure Detected / Cross-Detector Consensus] ... STOP incremental patching`（`consensusState.check`）。`confidence.score()` 与 `consensusState.recordFiring` 的统计保留。

仍在工作、且是刻意保留的：`[trajectory-health]` 复合告警——错误负载和工具失败率只是其中两个维度，必须综合分 ≥ 5/10、≥ 5 轮才触发，每个 run 最多 2 次，且走 `injectGuidance`（受每轮指引预算约束）。

回归测试：`TestErrorBudgetNeverSteersOrStops`、`TestErrorStormRunIsNeverSteeredByBudget`（25 次连续工具失败的真实 run：零注入、不被截断）。

与显式上限的关系：`max_iterations` / `tool_call_budget` / `max_tool_calls` / `session_*_budget` 这些用户显式配置的硬上限照常生效；它们是用户设定的边界，不是默认注入的转向。
