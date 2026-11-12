# Agent 自我模型（self-model，持久化）

## 问题（#feat-self-model P1）

ggcode 的"自知"此前是**反应式、会话级、不可查询**：capability_boundary 只在会话内 pivot+失败时发散文告警，tool_effectiveness 只在内存记成功率（`toolEffTracker` 无持久化）。跨会话冷启动重复交学费；delegation 派单无接收方胜任度依据。对标：SARSI（arXiv:2607.12254）持久 self-model 驱动 scope routing/结构化 handoff；SEAA（arXiv:2609.17331）自模型须随经验**持续更新**而非静态 prompt，否则 personality drift；既有 `capability_boundary.go:119` 已引用同谱系 ICML 2025（arXiv:2506.05109）但只做了最薄一层。

## 机制

`internal/agent/self_model.go`：

- **AgentSelfModel**：`Skills map[string]*SkillStat{Success,Fail,LastFail}` + `DeclaredLimits[]`（20 环形上限）+ UpdatedAt
- **持久化**：`.ggcode/self_model.json`（0600）；**load 即 merge**（跨会话累积而非重置——SEAA 批评点）；每次记录即保存（小 JSON 防崩损，无终钩依赖）
- **双钩接线**（复用既有站，零新探测）：
  - `recordCall`（agent.go 工具结果处理处）→ 成功/失败 upsert（失败留 80 字 LastFail 摘录）
  - capability_boundary 告警触发处 → `addLimit("approach pivots xN + M fails")`
- **DelegationContext()**：派单/handoff 卡片——Top-3 胜任（≥3 次才计入）+ 最近 3 条 DeclaredLimits；agent 派单时可据此"派/不派/加约束"

## Backlog（P2）

- **delegation prompt 注入**：`DelegationContext()` 已就绪；真正派单 prompt 构建站在 agentruntime/tool 层，接线列为后续 PR
- **skill 级依赖/工具组合声明**（SARSI Dependencies 字段填充）

## 测试

`zz_self_model_test.go`（4 段，-race 通过）：PersistReloadMerge（记录→存→新会话载→merge 累积）/LimitCap（20 环形淘汰）/DelegationContext（空卡/强度排序/limits 携带/弱项浮现）/FileContract（.ggcode/self_model.json 落盘）。agent 全套件 `-tags goolm` 绿。
