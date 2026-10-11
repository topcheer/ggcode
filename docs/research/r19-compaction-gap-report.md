# r19 Context Compaction 对标报告（Gap Report）

> 研究轮 r19 产出。趋势素材见任务书 `r19-context-compaction-brief.md`（2026-10 主会话预取）。执行：cron-runner (sa-5)，证据均为实读的 文件:行号。

## 五个对标问题的回答

### Q1 类型感知度（对标 A1 Compaction Cliff）— 部分类型感知，但确定性保真只覆盖 2 类

- payload 构建是类型感知的：`buildSummaryPayload`（manager.go:2570-2625）对 tool_result 头部截断 200/500 字符、tool_use 输入截 300、用户消息设 verbatim 专区（manager.go:2580-2589）、图片显式标注"不保留"（manager.go:2617-2619）；trigger 消息 verbatim 嵌入（manager.go:2344-2346，#625）。
- 但"压缩后确定性兜底"只有 2 类：`applyFactRetention`（fact_retention.go:125-159）只回填**约束行**（constraintMarkers 关键词，fact_retention.go:23-26）和**高频文件路径**（≥3 次出现，fact_retention.go:34）。工具产出的事实（测试结果/commit hash/失败原因）、搜索结果、推理块没有确定性保留——全靠 LLM 遵守 prompt 章节（manager.go:2352-2393）。A1 指出的正是"prompt 命令≠保证"。
- PCC 分块（summarize_pcc.go:45-85）纯按 token 打包 + 消息边界切分，块内类型混合，无类型配额——但每块 prompt 保留了 9 类契约（summarize_pcc.go:91-107），风险可控。

### Q2 触发策略（对标 A3 SELFCOMPACT / A5 缓存权衡）— 纯预算阈值 + 用户命令，无模型自决、无缓存代价考量

- 主触发：`autoCompactThresholdLocked`（manager.go:2478-2497）+ agent 侧 `maybeAutoCompact`（agent_compact.go:232）/ `tryReactiveCompact`（agent_compact.go:121，provider 报错后被动压缩）。
- 非预算入口只有一个：`/compact` 命令 → `RequestAutoCompact`（agentruntime/compact_context.go:29 → agent_compact_request.go:33）。**没有暴露给模型的 self-compact 工具**——A3 SELFCOMPACT 的"触发判断器+压缩计划"完全缺失。
- KV 缓存：`RecordUsage` 精细解析 CacheRead 语义（manager.go:1222-1241），但压缩触发完全不消费该信息——A5 的"缓存失效代价 vs 压缩收益"权衡未进入决策。

### Q3 压缩后防重做（对标 B1 Claude Code v2.1.293 post-compaction redo）— 三层机制，但 Done 抽取是 prompt-only

- 三层：① 摘要 prompt "## Done … ✅ This prevents the agent from redoing finished work"（manager.go:2361-2362）+ Dead Ends 反知识节（manager.go:2376-2377）；② `buildPostCompactState` 压缩后重建 recent files + todo 板（manager.go:2805-2818，注入于 1447/1497-1507 与 1105-1107）；③ `postCompactNoteFn` 任务板 re-materialize（manager.go:263-323）。
- 缺口："Done" 抽取无确定性后验——与 fact_retention 修掉的同一类问题（prompt 命令非保证）：若摘要漏写某项已完成工作，无机制从 transcript 确定性回填（如成功 commit hash、通过的测试名）。

### Q4 入口治理分工（对标 B3 Gemini CLI safer tool output handling）— 已有，边界清晰

- `tool_output_guard.go` 在 agent 侧截断超大单次输出（internal/agent/error_propagate.go:30、search_param_guard.go:19、mcp/adapter.go:367 注释互相印证分工）；出口侧另有确定性 in-place 压缩：`CompactSupersededReads`（manager.go:1766）、`CompactSupersededCommands`（manager.go:1881，引 AgentDiet arXiv:2509.23586）、`CompactOldReasoningBlocks`（manager.go:1672，签名感知）。入口治理→出口压缩分工成立。

### Q5 拒绝路径语义（对标 A2 Compaction Theory 算子分类）— 5 种拒绝码 + 冷却归因；缺"结构化抽取"算子

- `CompactRejectReason`：None/NoChange/Empty/UserReset/BenignTrim（manager.go:91-104），拒绝语义用于 precompact 冷却退款的正确归因（manager.go:172-180 注释 + agent_precompact.go:328-330）。被拒后无强丢：TOCTOU 非尾变更守卫直接弃置快照等下次触发（manager.go:1459-1465）；PTL 错误走 `truncateGroupsForPTLRetry` 降级（manager.go:2418-2427）。
- 对照 A2 算子分类：已有删除（superseded）、替换（summary）、豁免（pinned.go）；缺"确定性抽取/结构化状态机"类算子（如把 tool_use 成功调用表提炼为紧凑表格）。

## Gap 清单

| # | Gap | 分级 | 差距 | 实施 |
|---|-----|------|------|------|
| G1 | fact_retention 类型覆盖窄（仅约束行+路径，无工具事实/Done 锚点/失败原因） | 缺陷级边缘 | A1/B1 | 未实施：确定性"已完成工作回填"需定义抽取契约（哪些 tool 调用算 done、防误判），边界不清，进 backlog |
| G2 | 无模型自决压缩入口（SELFCOMPACT） | 愿景级 | A3 | 否——需新增模型侧工具 + precompact 状态机扩展（agent_precompact.go:104 StartPreCompact 可复用），建议单独立项 |
| G3 | 压缩触发不考虑 KV 缓存失效代价 | 体验级 | A5 | 否——RecordUsage 已有 CacheRead 数据（manager.go:1232），可在 maybeAutoCompact 加"缓存命中率高时推迟到更晚阈值"启发式，~80 行，进 backlog |
| G4 | 摘要图片内容显式丢失（manager.go:2617-2619） | 体验级 | B2 | 否——多模态摘要成本高，属产品决策 |
| G5 | 缺确定性"结构化抽取"压缩算子 | 愿景级 | A2 | 否——研究向 |
| G6 | fact_retention.go:158 日志计数 bug（%d/%d 两处都传 len(facts)） | 缺陷级（trivial） | — | **已实施**（主会话 r19: 提取 written 计数） |

## 结论

ggcode 压缩执行策略在**入口治理、确定性 in-place 算子、压缩后状态重建、拒绝归因**上已对齐或超过 B1/B3 工业实践；核心前沿差距集中在 **A1（类型盲的确定性兜底）** 与 **A3/A5（自决触发与缓存代价）** 三个方向。下一轮优先级建议：G3（缓存感知触发，最小可行）→ G1（Done 锚点确定性回填，需先定义契约）→ G2（SELFCOMPACT 自决入口，单独立项）。
