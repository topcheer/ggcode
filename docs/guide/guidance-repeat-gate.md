# Guidance Repeat Gate

每个非关键（non-critical）hint 标签在一次运行内最多投递 `guidanceTagMaxDeliveries`（当前为 3）
次。达到上限后，该标签在本次运行的剩余部分被降级（demoted）：

- 后续同标签 hint 在进入每轮 budget 之前即被丢弃——不再占用
  `guidanceBudgetPerTurn` 的两个 advisory 槽位，也不计入 byte 池；
- 第一次被丢弃的 hint 会替换为一条一次性的一行通知
  `[guidance-paused] ... the earlier hint still stands.`，让模型知道此前的
  指导仍然有效；
- 之后的重复投递静默丢弃（仅 debug 日志）。

关键标签（`criticalHintTags`，如 `hardcoded-secret`、`git-destructive`）与无
标签 hint 永不降级：安全指导不受此门限制。

## 为什么需要它

- `guidance_budget.go` 只做**轮内**去重与限额；一个持续复燃的检测器可以在
  每一轮都占掉一个 advisory 槽位，把更新鲜的信号挤出去。
- 各检测器的 per-run 配额是手工调的，且只覆盖部分家族；repeat gate 是在
  budget 收口处的**通用**兜底：任何标签（包括未来新增检测器）都不会被无限
  重复投递。
- 研究依据：harness 工程质量决定 agent 成本与质量（arXiv:2607.08938）、
  alert fatigue（Google SRE Ch.6）、overthinking/重复 meta-prompt 削减
  （2026）。

## 压缩（compaction）语义

账本在 context 压缩后重置（`guidanceCounterResets` 注册项）：被压缩掉的
指导文本模型已不可见，"先前 hint 仍然有效"的前提不再成立，投递上限随之
重新计数——与 B 类检测器 once-per-run 配额的压缩契约一致。每轮的
`guidanceBudget.reset()` 不会触碰该账本（它是 run 作用域的）。

## 协议安全

所有通知都走既有的 `appendGuidance`（追加进 tool result 内容）或
`injectGuidance`（受 budget 约束的 user 消息）通道，不会在 tool_calls 与
tool_results 之间插入新消息。
