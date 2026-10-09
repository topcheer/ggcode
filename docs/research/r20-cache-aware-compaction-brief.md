# R20 任务书：缓存感知压缩触发（Cache-Aware Compaction Trigger）

## 前沿概念（本轮方向，主会话在线检索 2026-10-09）

**方向**：Compaction 与 Prompt Caching 的成本权衡——压缩时机决策必须消费缓存命中数据。

### 在线检索事实（非训练记忆）

1. **TokenPilot: Cache-Efficient Context Management for LLM Agents**（EMNLP 2026 Findings, arXiv:2606.17016, v2 2026-08-28）
   - 问题定义：现有文本修剪/动态内存驱逐的"无约束序列突变"改变前缀布局 → prefix mismatch → cache invalidation。揭示 **text sparsity 与 prompt cache continuity 的关键权衡**
   - 方法：双粒度——全局 Ingestion-Aware Compaction（稳定 prompt 前缀、入口闸门消除开放世界噪声）+ 局部 Lifecycle-Aware Eviction（保守 batch-turn 调度，仅在任务相关性过期时卸载）
   - 结果：isolated 模式成本降 61%/56%，continuous 模式降 61%/87%（PinchBench / Claw-Eval）
2. **Anthropic 官方 Threshold Compaction 文档**（platform.claude.com/docs/en/build-with-claude/compaction-threshold）
   - `trigger: {"type": "input_tokens", "value": N}`（默认 150k，最低 50k）
   - compaction 后 API 自动丢弃 compaction block 之前的所有内容块；`iterations` usage 数组可观察 compaction 轮与 message 轮各自的 token 消耗
3. **Don't Break the Cache**（arXiv:2601.06007v2）：动态内容放尾部、排除动态 tool results，比 naive 全上下文缓存收益更稳定；naive 全缓存反而可能增加延迟
4. **Context Engineering for Production LLM Agents (2026)**（appscale.blog）：prompt-caching economics ~10x 便宜前缀；**cache-hit telemetry 与 context-precision 遥测列入成熟度阶梯**
5. **Claude Code prompt caching 文档**（code.claude.com/docs/en/prompt-caching）：官方解释 /compact 的缓存代价（压缩破坏缓存前缀 → 慢的无缓存轮）、cache hit rate 可查

### 概念要点

压缩在省 token 的同时**破坏缓存前缀**：压缩后所有后续请求的前缀重算（全价），而未压缩时前缀走缓存读（~10x 便宜）。因此"何时压缩"的最优决策依赖缓存状态：**高缓存命中率的会话里，压缩的净收益 = 省下的全价 token − 失去的缓存折扣 token**，提前压缩可能是净亏。TokenPilot 的"保守 batch-turn 调度、相关性过期才卸载"正是这个思想的落地。

## 上一轮结论（r19 已研究未实施，docs/research/r19-compaction-gap-report.md G3）

> **G3（下轮优先，~80 行启发式）**：压缩触发不消费 RecordUsage 已有的 CacheRead 数据。压缩时机的收益计算只看 token 总量，无视当前会话的缓存命中率——高命中会话提前压缩 = 放弃 10x 折扣的全价重算。

## 对标任务（ggcode 代码库审查）

### 已知线索（主会话预定位）

- `internal/context/manager.go` L1222-1234：`RecordUsage` 已正确处理 CacheRead 的供应商语义差异（Anthropic: CacheRead 与 InputTokens 相加；OpenAI 系: PromptTokensTotal 为准）——**数据已存在，问题在消费端**
- `internal/context/manager.go` L1316 附近：usage 日志输出 CacheRead
- `internal/agent/agent_compact.go`：反应式压缩（isPromptTooLongError → tryReactiveCompact）+ fallbackCheckpoint
- `internal/agent/agent_precompact.go`：precompact 机制
- 主动压缩触发决策点未定位（可能: AnalyzeBudget / agent loop 每轮检查 / budget guard）

### 需要回答的问题

1. **主动压缩的完整触发链**：从 agent loop 到实际执行 Compact 的决策路径（文件:行号），阈值如何计算（baselineTokens? 百分比?）
2. **CacheRead 在触发点的可见性**：触发决策函数能否拿到最近 N 轮的 CacheRead/total 比率？数据流上缺哪一跳？
3. **Gap 确认**：触发逻辑是否 100% 无视缓存状态？是否有任何地方已考虑（避免重复实施——注意已有 budget-guard-impl、adaptive 相关记忆）？
4. **供应商语义**：非 Anthropic 供应商（无增量 CacheRead 语义/无缓存）下方案如何降级（no-op）？

### 可实施方案草案（供验证/修正，不是定论）

在触发决策处加缓存因子：近期 CacheRead 比率 > 阈值（如 0.6）且 token 未达硬上限时，推迟压缩（或调低紧迫度），因为压缩将放弃缓存折扣；比率低（缓存已冷）则按原阈值。保守设计：只推迟不提前，上限保护不变（never exceed window）。

### 硬性要求

- **工具白名单绝对优先**：你只有只读工具（read_file/grep/glob/code_search/code_execution/lsp_*）。不要尝试写文件、web 搜索、git 操作——白名单即边界
- 不实施代码——产出方案（文件/函数级 diff 指引）与测试建议，主会话负责落地
- 结论必须给出明确 verdict：gap 成立 / 不成立（若已有等价实现请指认文件:行号）
- 报告全文在最终消息返回（你无写工具，落盘由主会话代做）

## 排除的重复方向

写入时代码质量检查 / Tool Effectiveness Tracker / Context Budget Awareness Gate（已拒）/ r19 上下文压缩审计本体（G1/G2/G3 之外不复审）。
