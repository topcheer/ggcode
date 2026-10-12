# r19 研究轮任务书：上下文压缩执行策略（Context Compaction Policy）

> 本任务书由主会话预取并内嵌了全部在线趋势素材。**执行者不需要也不得使用任何网络搜索工具**（web_search / web_fetch / mcp web-reader / browser 均被白名单排除）。所有链接仅作溯源引用。

## 目标

对标 2025-2026 上下文压缩（context compaction）前沿方法论与工业实践，逐条审查 ggcode `internal/context/` 的实际实现，产出 **gap 对标报告**（ggcode 现状 vs 前沿行为，逐条差异 + 建议实现路径）。若发现小而确定的缺陷级 gap（改动 <200 行、边界清晰、可独立测试），可直接实施并提交；大 gap 只出 backlog 建议，不实施。

## 一、已内嵌的趋势发现（主会话 web 搜索预取，2026-10）

### A. 学术前沿（本方向的核心方法论）

| # | 概念 | 来源 | 一句话行为描述 | 与 TUI coding agent 的相关性 |
|---|------|------|----------------|------------------------------|
| A1 | **Compaction Cliff（类型盲压缩的结构性失败）** | arXiv:2608.22752 https://arxiv.org/html/2608.22752v1 | 四个 LLM compactor 家族均复现：type-blind（不区分内容类型）压缩在长程会话中触发断崖式性能崩塌；结论是压缩必须按内容类型区分策略 | 直接命中：coding agent 会话里有工具输出/代码片段/用户指令/推理块等异质内容，一刀切 summarize 就是 type-blind |
| A2 | **Context Compaction Theory** | arXiv:2608.01326 https://arxiv.org/abs/2608.01326 | 把 compaction 形式化为状态拟合问题，给出 LLM-summarization 之外的确定性压缩算子分类 | ggcode 的 CompactResult/CompactRejectReason 体系可对照该分类自查覆盖面 |
| A3 | **SELFCOMPACT（模型自决压缩）** | arXiv:2606.23525 https://arxiv.org/pdf/2606.23525 | 不用固定阈值：模型在推理时自己决定何时压缩、压什么，配两个推理时元素（触发判断器 + 压缩计划） | ggcode 目前是预算阈值触发（budget.go）；"agent 自主发起压缩"是产品级差异点 |
| A4 | **CompactionRL** | arXiv:2607.05378 https://arxiv.org/html/2607.05378v2 | 用 RL 联合训练长程 agent 的压缩策略（压缩动作进入策略空间） | 研究向，本轮只作背景，不建议实施 |
| A5 | **Compaction vs prompt caching 成本权衡** | https://www.louisbouchard.ai/context-engineering-2026 | 2026 年讨论：缓存命中成本下降后，"全保留 + 缓存"有时比"压缩 + 失缓存"更便宜 | ggcode 有 kv-cache 记忆（research-r105）；压缩触发时机应考虑缓存失效代价 |

### B. 工业实践信号

| # | 竞品行为 | 来源 | 与 ggcode 的对照点 |
|---|---------|------|--------------------|
| B1 | Claude Code v2.1.293 修复 **post-compaction redo** 问题（压缩后重做已完成工作）——压缩丢事实导致 agent 重复劳动，官方持续投入修 | https://dev.classmethod.jp/en/articles/20261008-cc-updates-v2-1-293 | ggcode 已有 fact_retention.go / post_compact_note_test.go，需对标"压缩后防重做"语义是否等价 |
| B2 | Claude Code cloud sessions 压缩后 artifact 读写被拒的修复（压缩与会话状态一致性） | claudelog.com changelog v2.1.28x | 压缩与 checkpoint/pinned 状态的交互一致性 |
| B3 | Gemini CLI v0.60+：**safer tool output handling**（工具输出先清洗再入上下文，从源头减压缩压力） | https://releasebot.io/updates/google/gemini-cli | 入口治理 vs 出口压缩：ggcode tool-output guard（tool_output_guard.go）与 compaction 的分工 |

## 二、概念分析与对标范围

ggcode 对应实现（必须逐文件审查，`internal/context/`）：

- `manager.go`（ContextManager 主循环、CompactResult、CompactRejectReason）
- `budget.go`（预算触发、BudgetBreakdown、AnalyzeBudget）
- `fact_retention.go`（压缩时的事实保留——对标 A1/B1 的关键文件）
- `summarize_pcc.go`（PCC 压缩路径）
- `pinned.go`（钉住项豁免压缩）
- `token_calibrator.go` / `tokenizer.go`（token 估算）
- 关联消费方：`internal/agent/` 中调用 compaction 的位置（grep `Compact` / `ContextManager` 找调用点，含 reasoning-block 压缩与 superseded-reads 系列已知实现）

**对标问题清单（报告必须逐条回答，附代码证据：文件:行号）**：

1. **类型感知度**（对标 A1）：当前压缩路径是否区分内容类型（工具输出 / 代码 / 用户指令 / 推理块 / 搜索结果）？fact_retention 的"事实"抽取覆盖哪些类型、漏哪些？是否存在一刀切 summarize 的路径？
2. **触发策略**（对标 A3/A5）：压缩完全由预算阈值驱动吗？是否存在"agent/模型自主决定压缩"的入口？压缩触发是否考虑 KV 缓存失效代价？
3. **压缩后保真验证**（对标 B1）：压缩完成后有无机制防止"重做已完成工作"（如已完成工具调用的结果标记、task 状态锚点）？post_compact_note 的语义是什么，够不够？
4. **入口治理分工**（对标 B3）：工具输出入上下文前有无体量治理，与压缩的职责边界是否清晰？
5. **拒绝路径语义**（对标 A2）：CompactRejectReason 有哪些拒绝原因，被拒后会话如何继续（降级？强丢？），与前沿"确定性压缩算子"分类相比缺什么？

## 三、产出要求

1. **gap 对标报告**（写到 `docs/research/r19-compaction-gap-report.md`）：每个 gap 给出
   - 前沿行为描述（引用上表编号）
   - ggcode 现状（文件:行号证据）
   - 差距定性（缺陷级 / 体验级 / 愿景级）
   - 建议实现路径（包/文件/接口草案，不写代码）
2. 缺陷级 gap 才允许实施（<200 行 + 有测试 + `go test ./internal/context/` 全绿），实施后 git commit（信息含 `r19:` 前缀）
3. 结束时 save_memory 一条 `research-r19-compaction-audit`（结论 + gap 清单 + 是否实施）

## 四、硬性约束

- **工具白名单（绝对优先）**：read_file, multi_file_read, grep, glob, search_files, code_search, list_directory, lsp_symbols, lsp_definition, lsp_references, lsp_hover, edit_file, multi_edit_file, write_file, run_command（仅构建/测试）, git_status, git_diff, git_add, git_commit, save_memory, knowledge_graph
- **禁止**：web_search, web_fetch, mcp web 工具, browser, spawn_agent, delegate, 任何网络访问——趋势素材已全部内嵌上文
- 不做预算门控方向（Context Budget Awareness Gate 已被拒，与 budget guard 重复）；本轮只谈**压缩执行策略**
- 不重复已研究方向：写入时代码质量检查（60+ AST 检查）、Tool Effectiveness Tracker、Context Budget Awareness Gate
- 主仓工作树可能共享他人 WIP：只 stage 自己创建/修改的文件，逐个 git add，禁 git add -A
- 跳过一切 rewind/checkpoint 对标（刚交付 PR #3605）

## 五、验收标准

- [ ] 报告文件存在且五个对标问题全部有代码证据级回答
- [ ] 每个 gap 有定性分级 + 实现路径建议
- [ ] 若实施了缺陷级修复：测试全绿 + commit 落地；否则明确说明为何无缺陷级 gap
- [ ] save_memory 完成
