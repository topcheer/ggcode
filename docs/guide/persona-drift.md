# 跨会话约束锚点（persona drift / behavioral anchors）

## 问题（#persona-drift P0）

ggcode 的 8 类会话内 drift 检测（goal/scope/criteria/plan/env/doc + constraint_amnesia）**全部每 run 清零**（goal_drift_ctx.go:36-44 reset、constraint_amnesia.go:37 reset+仅 1 警），无任何跨会话约束存续。用户可感知失效：会话 A 明确否决的方案/指定约束（"禁止 X"、"必须走 worktree"）在会话 B/C 被静默遗忘或违反——Nautilus Compass（arXiv:2605.09863）实测的生产级 agent 最贵静默失效模式（遗忘用户约束/滑回已纠正错误/虚构先前约定），黑盒锚点法（索引期零 LLM）ROC AUC 0.83。

## 机制

`internal/agent/persona_drift.go`（与既有 constraint_amnesia **正交**：那是会话内提醒，本文件是跨会话持久层；EN 提取复用其 `extractConstraints`，CN 模式本文件新增）：

- **ConstraintAnchor**：`{ID, Text, Source, CreatedAt, Violations}`；提取自用户消息（EN `constraint_amnesia` 模式 + CN `不要/禁止/必须/铁律` 附加模式），按归一化文本去重，50 条环形上限
- **持久化**：`.ggcode/anchors.json`（0600），跨会话累积
- **肯定面注入**：`maybeInjectDynamicSystemPrompt` 的 dynamicParts 注入 `RenderAnchorsSection()`（最多最近 8 条+违规计数）——system prompt 每轮提醒
- **违反检测**：工具调用处（recordToolCall 群）`CheckViolation`——禁令锚点词级关键词（削情态词 never/must/only...）命中工具目标 → `[anchor-violation]` 告警（引用锚点原文+累计违规数）+计数持久化

## Backlog（P1/P2，研究席判定）

- **P1 漂移评分 EWMA**：近 N 次工具目标 vs 活跃锚点重叠率 EWMA，低于阈值状态行告警（模式同 teammate_trust 规划）
- **P2 锚点审计**：变更 Merkle 链日志（对标论文防篡改设计）；handoff（#5472）锚点包随席传递

## 测试

`zz_persona_drift_test.go`（3 段，-race 通过）：ExtractPersist（EN+CN 提取→持久化→重载→去重）/Violation（词级命中告警引用锚点原文+计数跨会话持久+良性目标静默）/PromptSection（空库零渲染+锚文本列出）。agent 全套件 `-tags goolm` 绿（46.7s）。

> 部署注记（#5500）：锚点/self-model 存储按 **workspace** 路径解析（SetWorkingDir 惰性绑定），非 process cwd——ACP/daemon 形态（cwd=/）下文件仍落在项目 `.ggcode/` 下。
