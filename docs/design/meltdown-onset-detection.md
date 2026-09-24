# Meltdown Onset Detection (Sliding-Window Tool-Sequence Entropy)

## 概述

ggcode 内置的 meltdown onset 检测器监控 agent 工具调用序列的"行为熵"：当最近
16 次工具调用的名称分布 Shannon 熵持续超过阈值时，判定 agent 进入 meltdown
onset（行为崩溃前兆）——特征是无锚点的、不收敛的工具乱切换（即使每次调用都
"成功"）。此时注入有限次 guidance，建议暂停重规划 / 回滚 checkpoint /
compaction 后重新对齐目标。

## 研究依据

- arXiv:2603.29231 *Beyond pass@1: A Reliability Science Framework for
  Long-Horizon LLM Agents*（2026）：行为崩溃（meltdown onset, MOP）通过
  工具调用序列的滑窗熵检测；前沿模型在超长任务中 meltdown 率最高
  （约 19%），MOP 处早期干预可挽回大量本将失败的运行。
- arXiv:2606.08162 *Silent Failure in LLM Agent Systems: The Entropy
  Principle*（2026）：高熵行为签名先于任何单点错误暴露的质量退化。

## 与现有检测器的区别

ggcode 既有行为检测器全部是"锚定"的：

| 检测器 | 锚点 |
|---|---|
| strategy_exhaustion / errStrategyLoop / compounding_failure | 重复错误指纹 / 失败率 |
| attention_fragment / solution_fixation | 目录切换 / 单文件失败编辑 |
| edit_oscillation / diminishing_edit | 文件内容翻转 / 编辑尺寸递减 |

meltdown onset 是唯一**无锚定**检测：不依赖任何错误、路径或编辑信号，
纯粹度量调用序列自身结构的塌缩。strategy_exhaustion 引用的 EEA 框架中
"persistently HIGH entropy with no convergence = flailing" 分支由本检测器实现。

## 判定条件（全部确定性，零 LLM 成本）

窗口 W=16 次工具调用，需同时满足：

1. 名称分布熵 ≥ 2.9 bits（等效多样性 ≈ 7.5 个不同工具）；
2. 窗口内不同工具数 ≥ 6；
3. 持久化门：连续 2 次满窗评估均命中（miss 时命中计数减半而非清零）；
4. 冷却门：两次告警间隔 ≥ 12 次工具调用；
5. 每 run 最多告警 2 次。

参考标定：3 工具循环 ≈ 1.6 bits、6 工具均匀探索 ≈ 2.6 bits（均不触发）；
10 工具均匀乱切 ≈ 3.25 bits（触发）。

## 实现

- `internal/agent/meltdown_onset.go` — 状态与判定逻辑
- `internal/agent/meltdown_onset_test.go` — 熵计算、结构化/健康/乱切序列、
  冷却、重置、衰减语义测试
- 挂载点：`internal/agent/agent.go` 工具结果循环（与 strategy exhaustion
  相邻），run 重置时同步 reset。

guidance 注入沿用既有 contextManager user-message 通道，接受 guidance
budget 统一治理。
