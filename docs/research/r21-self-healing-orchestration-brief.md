# R21 任务书：自愈编排（Self-Healing Orchestration）——失败分类驱动的恢复闭环

## 前沿概念（本轮方向，主会话在线检索 2026-10-09）

**方向**：tool-augmented agent 的失败处理从"盲目重试/放弃重规划"升级为"分类 → 按成本序选恢复动作 → 验证 → 预算熔断"的自愈闭环。

### 在线检索事实（非训练记忆）

1. **Self-Healing Agentic Orchestrators**（Suresh Babu & Agrawal, arXiv:2606.01416, 2026-05）
   - 故障注入基准：0.1-0.5 故障/工具调用 × 100 任务 × 9000 次执行
   - 无恢复静态工作流仅 **70.1%** 任务成功率；retry-only 基线在中等故障强度下平台在 **94.5%**
   - 分类恢复 + 验证闭环达 **98.8%**，且**静默失败（wrong-but-plausible）从 13.2-17.6% 降到 0**
2. **A Self-Healing Framework for Reliable LLM-Based Autonomous Agents**（Jeong & Shin, arXiv:2605.06737, 2026-05）：幻觉/执行错误/不一致推理在无 mid-flight 检测纠正机制时跨步骤传播
3. **七类失败分类**（F1-F7）：F1 工具超时/不可用；F2 schema/参数失败；F3 输出畸形/不完整；F4 工具选错；F5 上下文过期/不足；F6 证据矛盾；F7 控制环失败（重试风暴/无进展）
4. **八个恢复动作按成本序**：Retry → Argument Repair → Tool Substitution → Context Refresh → Replan → Graceful Degradation → Escalate to Human → Terminate；每次恢复后过 **Verifier**（schema 有效性/完整性/证据一致性）
5. **Codex CLI hook 体系对比**（codex.danielvaughan.com 2026-07 分析 + openai/codex issue #24907）：Codex CLI 暴露 4 个缺口——无失败分支 hook 事件（PostToolUseFailure，PostToolUse 只在成功时触发）、hook 工具覆盖不全（只有 shell/apply_patch/MCP 发事件）、无原生 recovery budget（重试/替换/升级计数熔断）、无专门 verifier 事件

### 概念要点

关键不是重试策略本身，而是**失败分类与恢复动作的映射 + 恢复预算**：malformed 参数重试 100 次也是同错（该走 Argument Repair）；两个工具输出矛盾（F6）该走 Context Refresh 而非 Retry；重试风暴（F7）该触发预算熔断升级人类。以及**失败分支的可扩展性**：用户/hook 应能在工具失败时刻介入注入恢复指导，而不仅是成功后。

## ggcode 已覆盖清单（对标时先排除，避免重复实施）

| 失败类/机制 | ggcode 已有实现（凭记忆，需代码验证） |
|---|---|
| F1 超时/限流 | adaptive-timeout、http-timeout-detection、rate-limit-awareness、transient-retry、circuit-breaker |
| F2 参数失败 | required-param-validation、tool-call-repair（r118） |
| F3 截断/畸形 | truncation-recovery、output-line-compression |
| F4 工具选错 | tool-target-mismatch |
| F5 过期读 | stale-read-detection、expired-read、file-freshness-sentinel |
| F6 矛盾证据 | memory-contradiction、delayed-observation-contradict |
| F7 重试风暴 | error-strategy-loop、futile-cycle、toolstorm/bgorphan |
| 错误分类 | error-classifier（ErrorCategory）、tool-error-fallback-hints |
| 验证 | race-verify-hint、verify-coverage-gap、builder-validator（r384） |
| 预算 | budget-guard |

**上表是记忆索引，不是代码事实——你的第一项工作是核实哪些真实存在、哪些是碎片。**

## 对标任务

### 需要回答的问题

1. **hook 系统失败分支**：internal/hooks 的 HookType 枚举有哪些事件？是否存在"工具执行失败"分支的 hook 事件（PostToolUseFailure 等价物）？工具失败时 hook 用户能否介入？Dispatch 在失败路径上是否触发？
2. **恢复动作映射**：error-classifier / tool-error-fallback 的输出是否驱动**差异化恢复动作**（分类→不同恢复路径），还是统一"注入提示文本"？F6（证据矛盾）与 F7（风暴）是否有专门的恢复分叉？
3. **恢复预算**：是否存在按恢复类型的计数熔断（如同一工具 N 次失败后降级/升级），还是只有全局 budget-guard？
4. **Gap verdict**：结合上表核实结果，判定：哪些子 gap 真实存在且用户可感知？优先级？是否存在"失败分支 hook 事件"这个 Codex CLI 同款缺口？

### 可实施草案（供验证/修正，不是定论）

若失败分支 hook 缺失：在 HookType 增加失败分支事件（如 PostToolUseFailure），在 agent loop 工具执行 error 路径上 Dispatch，payload 带工具名/参数摘要/错误分类，hook 返回恢复指导注入上下文。范围窄、用户可感知（hook 用户可写自定义恢复策略）。

### 硬性要求

- **工具白名单绝对优先**：你只有只读工具（read_file/grep/glob/code_search/code_execution/lsp_*）。禁止写文件、web、git 操作
- 不实施代码——产出方案（文件/函数级 diff 指引）+ 测试建议，主会话负责落地
- 结论必须明确 verdict：每个子问题 gap 成立/不成立（若已有等价实现指认文件:行号）
- 报告全文在最终消息返回（落盘由主会话代做）
