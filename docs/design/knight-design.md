# Knight 自进化精灵 — 深度设计文档

**核心理念：** Knight 是一个在后台持续运行的精灵，它通过观察用户行为、分析代码模式、
记录成功/失败经验，在同一个项目上变得越来越聪明。长期运行后，它对项目的理解深度
将远超人类工程师。

## 当前状态

### 已实现
- ✅ 5分钟 tick 循环 + idle time 检测
- ✅ Session 分析（最近10个会话）→ 发现可复用模式
- ✅ Skill candidate queue — 候选技能队列
- ✅ Skill promotion — staging → production 晋升
- ✅ Skill validation — 6小时一次的自动验证
- ✅ Budget 管理 — 每日 token 预算控制
- ✅ Usage tracker — 技能使用频率追踪
- ✅ Skill index — 全局/项目技能索引
- ✅ IM 通知 — 通过 DingTalk/Slack 等推送报告

### 缺失 — 必须补齐的能力

---

## P0: 项目知识图谱（Project Knowledge Graph）

**目标：** Knight 维护一个对项目的深度结构化理解，包括：

```
.kg/
├── architecture.json      # 架构概览：包依赖图、数据流
├── patterns.json          # 代码模式：常用 idiom、anti-pattern
├── hotspots.json          # 热点文件：频繁修改的文件及其原因
├── conventions.json       # 项目约定：命名、错误处理、测试风格
├── dependencies.json      # 外部依赖及版本约束
├── api-contracts.json     # 包间 API 契约（输入/输出类型）
├── pain-points.json       # 痛点：频繁出 bug 的区域
└── evolution.json         # 项目演化：每周架构变化摘要
```

**实现路径：**
1. `Analyzer.BuildKnowledgeGraph()` — 每次分析时增量更新
2. 用 AST 解析 Go 源码，提取类型关系和函数调用图
3. 结合 git log 分析变更频率和 co-change patterns
4. 知识图谱持久化到 `.ggcode/knowledge-graph/`

**关键指标：**
- 知识图谱覆盖的包/文件比例
- 图谱 freshness（最后更新时间）
- 每次 tick 的增量更新大小

---

## P0: 跨会话记忆（Cross-Session Memory）

**灵感来源：** Hermes 的 "Agent-curated memory with periodic nudges"

**目标：** Knight 维护三类持久记忆：

### 1. Episodic Memory（情景记忆）
- 每次 agent session 结束后，Knight 提取关键决策和结果
- 存储：`{session_id, task_summary, decisions_made, tools_used, outcome, lessons_learned}`
- 用途：下次遇到类似任务时自动参考历史

### 2. Semantic Memory（语义记忆）
- 项目特定的知识点："这个项目的数据库用 SQLite 不是 Postgres"
- 自动从代码和对话中提取
- 类似 CLAUDE.md 但自动维护
- 存储：`.ggcode/project-memory.jsonl`

### 3. Procedural Memory（程序记忆）
- 成功的操作序列作为 skill 候选
- 失败的操作序列作为 anti-pattern
- 存储：与现有 skill 系统集成

**定期 Nudge 机制（借鉴 Hermes）：**
- Knight 每小时检查记忆 freshness
- 过期的记忆被标记，在下次 idle 时用 LLM 重新验证
- 高价值的记忆被 promoted 到 `CLAUDE.md` / `AGENTS.md`

---

## P1: 自我反思循环（Self-Reflection Loop）

**灵感来源：** Hermes 的 "Skills self-improve during use"

**目标：** Knight 不只是创建 skill，还能持续评估和改进已有 skill：

```
每个 skill 有：
- quality_score: 0-100（基于使用频率、成功率、用户反馈）
- last_validated: 最后验证时间
- version: 版本号（每次改进递增）
- test_coverage: 覆盖的测试场景
```

**Skill 质量衰退检测：**
1. 如果 skill 的 success rate 下降 → 触发重新验证
2. 如果项目结构变化导致 skill 失效 → 自动修复或标记 deprecated
3. 如果更好的 pattern 被发现 → 自动更新 skill 内容

**Skill 变体测试：**
- Knight 可以创建 skill 的多个变体
- 在后台用真实场景 A/B 测试
- 最优变体自动 promoted

---

## P1: 用户模型（User Modeling）

**灵感来源：** Hermes 的 "Honcho dialectic user modeling"

**目标：** Knight 学习用户的偏好和工作风格：

```json
{
  "preferred_testing_style": "table-driven tests",
  "commit_message_format": "conventional commits",
  "code_review_preferences": ["small PRs", "descriptive comments"],
  "common_mistakes": ["forgets to close response body", "missing error handling in goroutines"],
  "project_priorities": ["reliability", "performance", "simplicity"],
  "working_hours": "9am-6pm CST",
  "notification_preferences": "slack only for critical findings"
}
```

**实现路径：**
1. 从 session 历史中提取用户反馈（"不，用另一种方式" = 偏好信号）
2. 从 git history 中提取编码风格
3. 从 code review 评论中学习项目标准
4. 模型持久化到 `~/.ggcode/user-model.json`

---

## P2: 主动学习（Proactive Learning）

**目标：** Knight 在 idle 时间主动学习而不只是被动分析：

### 1. 代码库探索
- 随机深度阅读代码，构建更丰富的知识图谱
- 发现未测试的代码路径 → 创建测试建议
- 发现潜在的 bug 或性能问题 → 加入待办

### 2. 依赖监控
- 检查 go.sum 变化 → 分析依赖更新
- 监控已知 CVE → 主动报告安全风险

### 3. Pattern Mining
- 分析 commit history 的 co-change patterns
- "修改 A 总是需要同时修改 B" → 记录为隐性依赖
- 预测用户接下来要改的文件

### 4. 竞品分析
- 关注 claude-code、opencode、crush 的更新
- 提取可借鉴的新功能/模式
- 自动评估是否适用于当前项目

---

## P2: Skill 生态（Skill Ecosystem）

**灵感来源：** Hermes 的 agentskills.io 兼容 + Skill Guard

### Skill 安全扫描（借鉴 Hermes skills_guard.py）
- 每个 Knight 生成的 skill 必须通过安全扫描
- 检测：数据泄露、注入、破坏性命令、持久化、网络访问
- 信任级别：builtin / trusted / community / agent-created
- agent-created 的 skill 发现 caution 级问题需要用户确认

### Skill 共享
- 项目级 skill 可以 exported 到全局
- 全局 skill 可以 imported 到特定项目
- 未来：skill marketplace（类似 agentskills.io）

---

## 架构改进建议

### 当前问题
1. **分析粒度太粗** — 只看最近10个 session，不考虑内容质量
2. **没有增量学习** — 每次分析从零开始，不利用已有知识
3. **Skill 验证太被动** — 只在固定时间验证，不根据代码变化触发
4. **没有反馈闭环** — 用户不告诉 Knight skill 是否有用
5. **Budget 管理太死板** — 只看 token 数，不看学习价值

### 建议改进
1. **增量分析** — 只分析自上次以来的新 session
2. **事件驱动验证** — git push 或文件变化时触发 skill 验证
3. **反馈收集** — 在 tool 输出中嵌入 "was this helpful?" 的隐性追踪
4. **智能预算** — 高价值分析（发现 bug）增加预算，低价值（重复模式）减少预算
5. **层级存储** — 热数据在内存，温数据在本地文件，冷数据压缩归档
