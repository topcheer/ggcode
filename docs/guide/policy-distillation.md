# Policy Distillation（策略蒸馏）

> sa-149 · GEPA 式提示策略自进化 · arXiv:2507.19457（Agrawal et al., Jul 2025）

## 问题

`traj_intel`（Post-Run Trajectory Intelligence）已实现提取→注入→因果记账闭环（r457-462）：学习条目带 `InjectedRuns/AfterSuccess/AfterFail` 计数和 holdout 对照组。但即使某条 learning 被因果数据证明 10/10 有效，它仍以**提取时的统计模板原句**反复注入（"runs of type X took N iterations..."）——学到的经验从未被编译成更好的**策略文本**。

GEPA 的核心主张：对执行反馈做反思性改写、直接编辑 prompt policy 文本，在 SWE-bench 上超过 DSPy MIPROv2 达 10%，且比 RL 少 31 倍样本。缺的正是这一"反思性文本进化"环节。

## 方案

`internal/agent/policy_distill.go`：

1. **成熟度门**：仅当 r461 计数器证明条目有效（注入 ≥3 次、成功率 ≥75%、未被 effectiveness gate 退休）才可蒸馏
2. **一次性蒸馏**：每批 ≤6 条，经 `aux` 辅助模型通道一次调用，把统计句改写为 ≤2 句祈使式策略，写回条目的 `Refined` 字段（JSONL，向后兼容）
3. **注入升级**：`RenderPromptSection` 优先注入 `Refined`，未蒸馏条目回退原句
4. **预算与安全**：每小时每工作区最多一批（stamp 节流）；15s 超时；fail-open；蒸馏文本经 sanitize（280 rune/3 行/控制字符剥离/注入模式黑名单）才可进入系统提示

## 与 traj_intel 分工

| 组件 | 职责 |
|---|---|
| `traj_intel.go` | 提取 learnings、注入、r461 因果计数、holdout 对照 |
| `policy_distill.go` | 消费**被证明有效**的条目，把模板句进化为策略文本 |

## 相关

- [traj_intel](../design/) — arXiv:2603.10600 Trajectory-Informed Memory
- [configuration.md](configuration.md) — `aux_model` 辅助模型通道
