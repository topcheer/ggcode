# 不确定性校准（uncertainty calibration loop）

ggcode 的不确定性检测面已有 7 个检测器（trajectory confidence、action hedging、compounded uncertainty、foresight 校准、多检测器共识、ask quality gate、capability boundary）。本节记录 **度量闭环化**：让孤立信号参与决策、让校准记账可反馈。

## P1：logprob 置信接入状态机

**问题**：provider 回报的 `event.Confidence`（avg token logprob）此前只在 `StreamEventDone` 处打一行 debug+低阈值提示（agent.go），未进 `confidenceState`——信号孤岛化。

**修复**：
- `confidenceState.lastLogprob *float64` 字段（reset 清零）
- stream done 事件将其写入状态（nil 守卫：provider 不回报时零扰动）
- `maybeIntervene()` 双证据触发：低 logprob（≤-2.5）叠加弱轨迹分 → 干预理由追加 "recent turns had low language-model confidence"

**依据**：arXiv:2608.27167——包装权威的伪证据使 12 个前沿模型承诺率 6.5%→54.0%；"说得漂亮"与"真有把握"须分离度量。

## P2：会话级校准记分卡

**问题**：`foresight_calibrate` 只数失配（mismatches）供告警封顶，命中从不记账，校准率不可知、不反馈。

**修复**：
- `foresightCalibrateState` 增 session 级 `sessionPreds/sessionHits`（**故意不受 turn 级 reset() 清零**）
- `foresightScoreboardLine`：每次评分预测记账；每 10 次输出记分卡（`[foresight] calibration scoreboard: N scored predictions, M confirmed (X% aligned)` + 分档建议：≥90% well-calibrated / ≥60% moderate / 以下 poor→"read actual state before acting"）

**依据**：arXiv:2610.18614——outcome-grounded 校准使 ECE 0.42→0.12；本实现为零成本近似（启发式计数，非 GRPO 训练）。

## 暂缓项（研究席 P3 判定）

- 多采样语义一致性抽查（HASSUM/CUSP 语义熵）——成本高，待高优场景驱动
- 硬弃权门（不可知问题拒答）——与审批疲劳教训冲突，2608.27167 证明门控上下文脆弱

## 测试

`zz_confidence_logprob_test.go`（双证据触发+reset 清零）、`zz_foresight_scoreboard_test.go`（每 10 次记分+分档建议+session 计数穿越 turn reset）。全量 `-tags goolm`。
