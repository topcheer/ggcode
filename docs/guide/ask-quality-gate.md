# Ask Quality Gate (r27)

## 概念来源

- **arXiv 2606.03135**（ICML 2026）"Uncertainty-Aware Clarification in LLM Agents with Information Gain"：用 Information Gain Reward（澄清交换对目标信念的贝叶斯更新量）训练 clarifier；τ-Bench 五 backbone 交叉评估：成功率比无澄清 +3.7%，平均交互步仅 +0.3——**高信息增益的问题既提成功率又几乎不打断用户**。
- **SAGE-Agent**（ACL 2026 Findings, 2026.acl.2028）：结构化不确定性做推理时问题选择，模糊任务覆盖 +7-39%，澄清问题数减 1.5-2.7x。

共同结论：澄清的价值是**选择问题**，不是数量问题。零信息增益的问题白花一次用户打断。

## Gap 判定（sa-57/59 轮）

ggcode 已实现"该问时问"侧：`ambiguity_point`、`input_underspec`（引用同一 IGR 框架）、`user_sentiment`、`loop_detect` 全部是促问方向。**"不该问时别问"侧无任何确定性门**——`internal/tool/ask_user.go` 只有 schema 校验与单请求内 question-ID 去重。

注意与 r24 Ask gate（#3368，permission 层审批节流+审计）完全不同轴：r24 管的是**权限问询的频率与审计**，r27 管的是**问题内容的信息增益**。

## 实现

`internal/agent/ask_quality_gate.go`（agent 工具循环 pre-exec 区，`tc.Name == "ask_user"` 分支，checkRedactedInWrite 之前）：

| 检查 | 行为 | 依据 |
|---|---|---|
| (a) 可自答拦截 | 问题文本命中确定性正则（which/what version、file exists、signature of、which branch、go.mod 依赖类）→ 阻断（error tool_result），引导用 read_file/grep/git 自查 | 答案是环境可查事实，信念更新可自服务，IGR=0 |
| (b) 同 run 重复问 | title+prompt+无序 choice 集 归一化后 FNV 指纹去重 → 第二次同问阻断 | 重复问零信念更新 |
| (c) 安全默认 advisory | choice label 含 default/recommended/safe/preserve/skip/current/keep → 不拦截，记 debug 日志提示"用户未回应时按默认继续+声明假设" | 有可逆默认时间问的期望收益低于按默认继续（与 assumption-track 互补） |

- 阻断粒度：批内任一问题命中 (a)/(b) 即阻断整个 ask（半坏的 ask 就是坏的 ask，模型应重组问题批次）
- per-run reset：每个用户 turn 重开问询窗口（新 turn 重新允许问）
- nil gate / 畸形 JSON：直通（schema 校验是工具自己的职责）
- 确定性、零 LLM 成本

## 测试

`internal/agent/ask_quality_gate_test.go`：4 case（自答拦截 / 归一化重复+新问放行 / 安全默认 advisory 不拦截 / reset 与直通），`go test -tags goolm -race -run 'AskQualityGate|AskGate'` 与 r24 既有 8 测试同跑全绿。
