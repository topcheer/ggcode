# Trajectory-Level Confidence Tracking (r75)

## 概念来源

- **Agentic Confidence Calibration** (arXiv:2601.15778, 2026-01, NVIDIA)：提出 Holistic
  Trajectory Calibration (HTC)。核心论点：静态单轮校准无法捕捉 agentic 系统的特有失败
  模式——轨迹上的复利式误差累积（compounding errors along trajectories）；需要过程级
  特征（宏观趋势 macro dynamics + 微观稳定性 micro stability）才能暴露"每一轮单独看都
  还行"的持续退化。
- 与本项目 sa-74（每轮 mean token logprob 置信度遥测，"Logprobs Know Uncertainty",
  KDD 2025）互补：sa-74 是**单轮点检查**，r75 是**轨迹层**消费同一信号。

## Gap

sa-74 的 `StreamEvent.Confidence` 在 `internal/agent` 只有单轮阈值消费（conf < -2.5
提示，无状态、可逐轮重复触发）。逐渐退化的轨迹（如 -0.6 → -1.1 → -1.4 → -1.8）从不
触发任何升级信号——这正是 HTC 指出的静态单轮校准盲区。

## 实现

`internal/agent/confidence_trajectory.go`（无新增 detector）：

- 滚动窗口（12 样本）记录每轮 logprob，两个可解释特征：
  - **micro**：连续 3 轮 < -1.75（持续退化 streak）
  - **macro**：窗口后半段均值比前半段低 ≥ 0.8 且后半段为负（下行趋势）
- **episode 语义**：每个退化 episode 只升级一次；某轮 ≥ -0.75 视为恢复，重新武装。
- 升级文案指向"重新验证当前方法或询问用户"，与 r74 escalation ladder / r73 硬停止
  级联互补（提供校准后的触发信号，不替代其决策逻辑）。

接线点：`agent.go` `StreamEventDone` 处理中单轮点检查之后，惰性初始化。

## 协议兼容

仅追加 `StreamEventSystem` 文本事件（与既有 `[confidence]` 提示同通道），不在
tool_calls / tool_results 之间插入任何消息。

## 测试

`confidence_trajectory_test.go`：episode 触发/静默/恢复再触发、纯趋势触发、
健康震荡不误报、窗口封顶。既有 `TestRunStreamConfidenceNotice`（sa-74 单轮行为）
保持不变且通过。

## 局限

- Anthropic 不暴露 logprobs（Confidence 恒 nil），轨迹层随之静默——与 sa-74 一致。
- logprob 是 token 级流畅度代理，不等于任务级正确性；HTC 论文的完整过程特征抽取
  （跨域校准器 GAC）需要离线训练，本实现取其可本地化的最小子集。
