# 计划监控与修复（plan monitoring → repair verdict）

## 问题（#plan-repair-verdict P1）

计划监控此前是 **fire-once 散文**：`plan_drift.checkPlanDrift` 首查即置 `fired=true`（且置位在判空返回**之前**——首查无漂移也会烧掉全 run 唯一告警），此后漂移持续/加深均静默；`subgoal_track` 同为 `sgMaxWarns=1`。对标前沿：ContextFlow（arXiv:2605.19314）scoped updates、PhyAgentOS（arXiv:2607.16636）success/failure/**replan** 三值裁决、RoboBRIDGE（2607.27881）deviation-triggered replanning。

## 修复：三值裁决状态机

`internal/agent/plan_repair.go`：

| streak（连续漂移查次） | 裁决 | 行为 |
|---|---|---|
| 1 | `verdictContinue` | 既有 nudge 文本（未收敛项清单） |
| 2 | `verdictRepair` | 结构化要求：**显式输出修订计划**（`todo_write` 保留完成项/删卡死项）+一行阻塞归因+禁止继续原路径 |
| ≥3 | `verdictAbandon` | 交给用户：重定目标/接受部分完成/换策略（`pdMaxVerdicts=3` 封顶防刷屏） |

`plan_drift.go` 重写：
- **无漂移查次清零 streak**（`stallStreak=0`）——修复烧机会 bug（判空前置位）
- 每次漂移 streak++ → `planVerdictFor(streak)` 升级裁决
- 既有 `fired` 字段保留（终局裁决后置位，兼容 adherence 联动）
- 未收敛判定语义不变（≥2 项或 ≥50%）

`subgoal_track.go`：`sgMaxWarns` 1→3（重复 nudge 低成本，升级职责已移交裁决机）。

## 暂缓项（研究席 P2/P3 判定）

- **P2 证据包接地**：planItem 增 evidence[]（工具名+file:line）替代纯关键词匹配，实现"构建失败→责任计划项"回溯——中期
- **P3 重规划预算**：plan-repair 触发计推理成本，超阈值降级 ask 用户（对标 arXiv:2610.31376）——低优

## 测试

`zz_plan_repair_test.go`：Escalation（1→continue/2→repair+todo_write/3→abandon+cap 静默）/StreakReset（清零检查不烧 verdict+干净查重置 streak+重置后再漂移为全新 continue）/VerdictFor（映射含溢出）。既有 6 项 plan/subgoal 测试零回归；全量 `-tags goolm`。
