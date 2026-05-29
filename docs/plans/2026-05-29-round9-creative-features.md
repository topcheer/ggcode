# Round 9 创意新功能建议（Creative Feature Proposals）

**Date**: 2026-05-29
**Author**: Round 9 review synthesis
**关联评审**: `docs/reviews/round9-summary.md`

本文档为 ggcode 提出 18 个创意新功能建议，结合现有 Round 8 / Round 9 review、现有设计文档（Knight、Mobile sub-agent tabs、Background pre-compaction、Live Stream 等）以及多端的实际能力缺口。每个建议给出：

- **创意点 & 用户价值**
- **可行性 & 风险**
- **实施草案**（涉及哪些包/文件 / 协议变更 / 测试要点）
- **依赖项**

排序按 **价值/工作量比** + **解锁后续能力** 综合给出。已有详细设计的功能（如 Knight 主体）不重复，仅指向。

---

## P0 — 解锁能力 / 长期影响最大

### F1. Cross-Instance Federated Memory（跨实例共享 Knight 记忆）

**创意点**：同一个项目可能由多人/多机器/多个 ggcode 实例协作。当前 Knight (`docs/design/knight-design.md`, `docs/design/knight-auto-evolution.md`) 是单实例记忆。引入一个轻量的**分布式只追加日志**（基于现有 relay 或一个独立的 sync endpoint），让所有实例的"项目理解"逐渐汇聚。

**用户价值**：
- 团队 A 的资深成员训练出的 Knight 直觉，自动惠及团队 B 的初级成员
- 跨机器接力工作时，"我昨天在公司机器上让 Knight 学到的东西"今天家里继续生效
- 大型 monorepo 的多个工作目录共享同一份代码理解

**可行性**：中等。需要：
- 一个 `knowledge_sync` 协议（基于 CRDT 或简单的 LWW 时间戳合并）
- 端到端加密（团队私钥），避免被中继看到
- 冲突解决策略（同一 fact 不同实例有不同表达）

**实施草案**：
- `internal/knight/sync.go`：新增 sync 客户端
- `ggcode-relay/`：可选新增 `/knowledge/{team_id}/append` 和 `/knowledge/{team_id}/since/{cursor}` 端点（或换用现有共享存储）
- 配置：`.ggcode/knight-sync.yaml`（team_id + 加密密钥 + 同步频率）
- 协议：每个 fact 携带 `(team_id, project_id, fact_id, content_enc, version, author_id, timestamp)`
- 测试：模拟两个实例并发学习不同 fact，合并后两边都能看到

**依赖**：现有 Knight 实施（仍是设计阶段，所以可以一起做）；relay 认证（解决 C-1 后才能上生产）

---

### F2. Multi-Repo Orchestration（跨仓库编排）

**创意点**：当前一个 ggcode 会话绑定一个 workspace。许多真实任务横跨多个仓库（backend repo + frontend repo + infra repo + docs repo）。引入 **WorkspaceSet** 概念：一个会话可同时挂载多个工作目录，所有工具（grep/view/edit/run_command）都能在配置允许的多个根下操作；agent 的 system prompt 自动注入"本会话挂载的仓库列表"。

**用户价值**：
- "把 backend repo 的新 API 同步到 frontend 客户端" 一次完成
- "在所有相关仓库都加上版权头" 一次完成
- 微服务 / Federation 架构原生支持

**可行性**：中等。需要：
- `config.WorkspaceSet`：新结构
- 所有 file 工具（fs / lsp / harness）按"工作目录前缀解析"，仍隔离不同根
- TUI 顶部状态栏增加 "1/3" 当前活跃 workspace 指示
- 移动端 chat 列表上每个消息打上 workspace 标签
- 权限模型扩展：每个工作目录可独立配置 `permission.PermissionMode`

**实施草案**：
- `internal/config/workspace_set.go`
- `internal/tool/builtin.go`：注入活跃 workspace 列表
- 工具协议：路径必须以已注册的 workspace 根为前缀，否则拒绝（兼现有沙箱）
- 新斜杠命令：`/workspace add <path>` / `/workspace remove <path>` / `/workspace list`
- 测试：覆盖跨仓库 grep、跨仓库 edit、权限隔离

**依赖**：无关键依赖；与 instance-config 设计协同

---

### F3. Time-Travel Debugging（会话时光机）

**创意点**：每个 agent 主循环 turn 完成时自动保存一个 mini checkpoint（已经有 session JSONL）。新增**双向时光导航**：用户可以 `Ctrl+Z` 回到任意一个 turn 之前的全状态（消息、文件、todo、harness），并在那里"如果当时我说了 X 会怎样"地分叉。

**用户价值**：
- 调错路径不需要重启会话
- 试探性 prompt 不再昂贵（"我先试试 A 路线，不行就回这里换 B"）
- 教学场景天然支持

**可行性**：中等偏高。现有 session JSONL + harness checkpoint 提供了大部分基础。难点是 **filesystem rewind**——可以用 harness 工作树 / git stash / 双向 diff 来实现。

**实施草案**：
- `internal/session`: 新增 `Branch(sessionID, fromTurn) -> newSessionID`
- `internal/harness`: 暴露按 turn 索引回滚工作树（已有 dirty checkpoint）
- TUI 斜杠命令：`/rewind <turn>` `/branches` `/checkout <branch>`
- 桌面端：会话列表显示分叉树
- 移动端：长按消息 → "fork from here"

**依赖**：harness 已有的 checkpoint 能力；新的 rewind UI

---

### F4. Plan Diff Visualizer（计划差异可视化）

**创意点**：当前 plan mode 退出时只给一段文字。引入**结构化计划**：plan 工具输出 JSON（changes[]、each with file+intent+touchedRanges），TUI/桌面/移动端用三栏 diff 视图渲染——左侧文件树、中间 file 内容、右侧 intent 标注，让用户在执行前一眼看清"这次到底要动哪些文件、哪些范围、为什么"。

**用户价值**：
- 真正可信任的 plan mode（不再依赖"它说会做 X"，而是看到 X 实际是什么）
- 复杂重构前的安全网
- 桌面/移动端原生体验

**实施草案**：
- `internal/tool/plan.go`：新增 `plan_propose` 工具输出结构化计划
- TUI：`internal/tui/plan_view.go` 新分屏面板
- 桌面：复用 markdownx 渲染 diff
- 移动：plan 卡片用 `expand` 进入分屏视图
- 协议：新事件 `plan_proposal` 通过 tunnel 推送给移动端

**依赖**：现有 plan mode 状态机；harness diff 工具

---

## P1 — 高用户感知 / 中等成本

### F5. Agent Telemetry Overlay（运行时遥测条）

**创意点**：opt-in 的浮动状态条（TUI 底部、桌面右下角、移动端折叠面板），实时显示当前 turn 的 LLM 首 token 延迟、tokens/s、cache hit %、context 占用百分比、并发 sub-agent 数。已有 stats panel 是离线统计；这个是实时遥测。

**用户价值**：
- 让用户 *看见* prompt caching 实际生效情况（v1.3.41 已支持）
- context 接近上限时提前主动 compact
- 给 power user 一种"驾驶舱"感

**实施草案**：
- 复用 `internal/metrics` 现有的事件流
- `internal/tui/telemetry_bar.go`：新组件，订阅 metrics 流
- 桌面：复用 metrics window，新增"live"模式
- 移动：聊天顶部一个可下拉的 dial 条
- 开关：`/telemetry on|off`

**依赖**：metrics goroutine 修好（Round 9 H 项）

---

### F6. Smart Cron Suggestions（Knight 推荐自动化）

**创意点**：Knight 在观察用户行为时记录"重复出现的工作流"（如：每周一手动跑 `npm audit` 并贴报告到飞书）。当置信度足够，TUI 在 idle 时主动提示："要不要把它做成 cron？" 一键采纳就写入 `.ggcode/cron.yaml`。

**用户价值**：
- 让自动化从"用户主动想出来"变成"agent 主动建议"
- 现有 cron 系统使用率大涨

**实施草案**：
- `internal/knight/pattern_detector.go`：滑动窗口找重复操作
- 提示触发：`m.handleAgentDoneMsg` 后；mobile/desktop 同步通过 tunnel
- 新斜杠命令：`/cron accept <suggestion_id>`
- 数据：Knight memory 持久化候选模式

**依赖**：Knight 主体实施

---

### F7. Inline Diff Intent Capture（用户编辑作为学习信号）

**创意点**：当 ggcode 提出一个 file edit 但用户在 review 时手动修改了 diff（或者事后又改了被 ggcode 改过的文件）——把这个 delta 作为**负反馈学习信号**输入 Knight。"用户更喜欢这种写法"。

**用户价值**：
- 长期使用越用越懂你的代码风格
- 不需要显式 RLHF

**实施草案**：
- `internal/diff/intent_capture.go`：钩进现有 diff 工具
- TUI：edit 完成时 watch 文件 1 小时，若被用户改动且未涉及业务逻辑（启发式），记入 Knight
- Knight 学习层接收 `(original_intent, ggcode_output, user_revision)`

**依赖**：Knight 主体

---

### F8. Voice Mode for Mobile（移动端语音）

**创意点**：移动端按住发声键 → 本地 STT → 作为用户输入推到 relay → desktop/TUI 处理 → 回复可选 TTS 朗读。骑车/做饭/通勤时让 ggcode 继续干活。

**用户价值**：
- 解锁"碎片时间也能用 ggcode"
- 与 mobile-subagent-teammate-tabs 设计天然互补

**实施草案**：
- 移动端：用 platform STT（iOS Speech / Android SpeechRecognizer）避免引入大模型
- 协议：tunnel 增加 `voice_input` 事件类型（含 text 转写 + 可选 audio blob）
- TUI/桌面：可选显示"语音输入"标记
- TTS：用平台 TTS；可选用 LLM 提供商的语音 API

**依赖**：移动端 reconnect 稳定（Round 9 mobile-relay 项）

---

### F9. Tab-Aware TUI（终端内多会话标签）

**创意点**：tmux-like 在同一个 ggcode TUI 中开多个 conversation tab，每个 tab 一个 session、一个 workspace。状态栏每个 tab 都有 busy/idle 徽章。一边 backend 等 LLM 回复时，前台切到另一个 tab 继续 frontend。

**用户价值**：
- 单终端高密度并行
- 与终端原生 tab（itab/iTerm tab）形成正交补充
- 主输入框消息可路由到指定 tab

**实施草案**：
- `internal/tui/tabs.go`：标签管理（每个 tab 独立 `Model`）
- 共享：agent、provider、policy、memory pool；session/chat 隔离
- 快捷键：`Ctrl+T` 新 tab、`Ctrl+W` 关、`Ctrl+1..9` 切换
- 终端 title 显示 `[backend*] frontend api-rewrite`
- 移动端：天然映射到现有 mobile-subagent-teammate-tabs 设计

**依赖**：终端 title 已上线；可借鉴现有 mobile sub-agent tab 设计

---

### F10. MCP Marketplace（社区 MCP 仓库）

**创意点**：内置一个 `/mcp browse` 命令，从一个可信源（如 GitHub repo `topcheer/ggcode-mcp-registry` 或现有的 `acp-registry/`）拉取社区 MCP 服务器目录，带签名、权限范围、用户评分。一键安装 + 沙箱限制。

**用户价值**：
- MCP 生态降低发现门槛
- 与 ggcode 品牌联动建立网络效应

**实施草案**：
- `acp-registry/` 已有原型，可扩展为 MCP registry
- `internal/mcp/registry.go`：客户端
- 签名：每个 MCP 包用 maintainer 私钥签；本地验证
- 沙箱：默认拒绝 read 系统目录、network egress；安装时让用户挑权限
- TUI：`/mcp browse` 进入交互式 list；`/mcp install <name>`

**依赖**：现有 MCP manager

---

## P2 — 高创意 / 探索性

### F11. Pair-Session Live Coding（多人共享 ggcode）

**创意点**：开发者 A 启动 ggcode → 生成"协作链接"→ 同事 B 通过浏览器/桌面/移动端加入。两人都能看 chat、看 diff、可选 lock 谁可发 prompt。冲突解决：发 prompt 排队（与现有 pending queue 兼容）。

**用户价值**：
- 远程 pair programming 不再需要 zoom 共享屏幕
- 教学/审计/onboarding 场景

**实施草案**：
- 复用 relay 协议（与 mobile 共享）
- 新事件 `peer_join`/`peer_leave`、`prompt_request`、`prompt_lock`
- WebUI 增加观察者模式
- 权限：admin/编辑者/只读三档

**依赖**：relay auth（C-1）；mobile-relay 稳定化

---

### F12. Workspace Snapshot Diff Between Sessions（会话级项目差异）

**创意点**：在会话开始时自动快照（git ref 即可）。会话结束时 `/snapshot diff` 显示"本次会话相对昨晚的我做了什么"——按文件、按 intent 分组，可一键转成 PR description / 提交说明 / 周报。

**用户价值**：
- 自动化的"我今天/本周做了啥"
- 杀手级 stand-up 工具

**实施草案**：
- `internal/session/snapshot.go`：会话开始/结束时存 git ref + 已编辑文件清单
- 新斜杠命令：`/snapshot summary` 让 agent 用 LLM 生成总结
- 输出格式：markdown 报告 + 可选直接 push 到飞书/Notion（复用现有 IM）

**依赖**：现有 session + IM

---

### F13. Compliance Audit Log with Cryptographic Signing（合规审计日志）

**创意点**：企业部署时强需求。每个工具调用、文件改动、命令执行都写一条结构化日志，链式 hash + 私钥签名。第三方可验证"这段时间内的所有动作"不可篡改。

**用户价值**：
- 解锁企业销售场景（金融/医疗/政府）
- 与现有 session JSONL 互补

**实施草案**：
- `internal/audit/logger.go`：仿 Git/Bitcoin 块链结构（previous_hash + current_payload → next_hash）
- 配置：`.ggcode/audit.yaml`（启用 + 公钥 + 输出目录）
- 输出：`audit/<session>/chain.jsonl`
- 验证工具：`ggcode audit verify <session>`

**依赖**：无

---

### F14. Differential Context Window with Cache Markers（增量上下文）

**创意点**：当 provider 支持 prompt caching（OpenAI / Anthropic 都已支持），ggcode 只发送 context 的 *差异*（已 cache 部分 + 新增部分），而非全 re-send。需要每个 provider 适配器实现"cache point"插入。

**用户价值**：
- token 费用大幅降低（已经能看到 cache hit % 但没有主动管理）
- 首字延迟减小

**实施草案**：
- `internal/provider`：新增 `CacheStrategy` 接口
- `openai.go` / `anthropic.go`：插入 cache point markers
- `context.Manager`：标记"稳定 prefix"（系统 prompt + 项目 memory + 已 compact 历史）
- 测试：模拟连续 3 个 turn，验证后两个 turn 的 cache hit % > 70%

**依赖**：现有 TokenUsage cache % 报告

---

### F15. Mobile-Driven Approval-Only Mode（移动端审批 HUD）

**创意点**：桌面 ggcode 以 `--mobile-approve-only` 启动 → autopilot 跑，但所有 dangerous 工具调用都暂停，推送通知到移动端→用户在锁屏直接 Allow/Deny。

**用户价值**：
- 真正的"启动一下午让 ggcode 自己干活"，但保留 safety net
- 与 daemon 模式正交（daemon 是无审批；这个是审批换平台）

**实施草案**：
- 复用现有 IM approval 流（dingtalk/feishu/qq 已支持），抽象出 mobile 路径
- 移动端：iOS Push Notification + Notification Action ("Allow"/"Deny")；Android 同
- 新 CLI flag：`--approval-via mobile`

**依赖**：移动端推送基础设施（可外包给 OneSignal / FCM）

---

### F16. AI-Assisted Workspace Diagnostics on Startup（启动健康检查）

**创意点**：TUI/桌面启动时（gate 后）后台跑一组健康检查（broken imports via LSP、TODO 数、failing tests、依赖过期、git 未推送、有 secrets 泄漏检查），结果以一行 "🟡 3 things might need attention — type /diag to see" 提示。

**用户价值**：
- 把 ggcode 从"等 prompt 的工具"变成"主动开口的助手"
- 与 Knight 形成时间互补（Knight 是慢学习；diagnostics 是即时反应）

**实施草案**：
- `internal/diagnostics/runner.go`：插件化 checker
- 内置 checker：lsp_errors、test_failures、stale_deps、git_status、secret_scan（用 trufflehog 风格 regex）
- TUI：`/diag` 命令展开详细报告；启动横幅显示概要
- 桌面：右上角图标气泡

**依赖**：现有 LSP 集成

---

### F17. Conversation → Runbook Export（会话转运维手册）

**创意点**：用户在 ggcode 中诊断了一个生产问题。结束后 `/runbook export <name>`——agent 把整个会话总结成"症状→排查步骤→根因→修复→预防"的标准运维手册（markdown），落地到 `docs/runbooks/`。

**用户价值**：
- 隐性知识显性化
- 与团队复用 / on-call 训练直接挂钩

**实施草案**：
- `internal/commands/runbook.go`：斜杠命令
- 用现有 LLM + 模板提示（"把以下会话整理成 SRE runbook"）
- 输出：markdown + 自动 PR 草稿（可选）

**依赖**：无

---

### F18. Battery-Aware / Connection-Quality-Aware Mobile（自适应移动端）

**创意点**：移动端检测网络质量 + 电量，动态调整：
- 4G 弱信号时：减少 token 流式频率（batch deliver）
- 电量 < 20% 且非充电：暂停 wakelock + 减少 ping 到 2 分钟
- 后台 + 锁屏：减少状态 UI 渲染
- 完全离线：把 prompt 排队、连上后再 flush

**用户价值**：
- 全天候挂着不烫不耗电
- 弱网环境（地铁、机场）依然可用

**实施草案**：
- `mobile/flutter/lib/core/adaptive.dart`：检测层
- 适配 `connectivity_plus` + `battery_plus`
- 协议：客户端可声明 `delivery_preference: batch|stream`
- relay 端按 client 偏好聚合

**依赖**：Round 9 mobile-relay 稳定化

---

## 实施优先级建议

| 阶段 | 包含 | 估算 |
|------|------|------|
| **Sprint 1（2 周）** | F1（仅 fact-sync 协议层 + 加密）、F5（telemetry overlay）、F16（diagnostics）、F4（plan visualizer） | 高用户感知，复用已有基础设施 |
| **Sprint 2（2 周）** | F2（multi-repo workspace set）、F9（TUI tabs） | 解锁后续工作流 |
| **Sprint 3（3 周）** | F3（time-travel）、F12（snapshot diff）、F17（runbook export） | 会话级生产力 |
| **Sprint 4（3 周）** | F6（smart cron）、F7（inline diff capture） — 都依赖 Knight 主体 | 等 Knight 落地后集中实施 |
| **Sprint 5（4 周）** | F11（pair session）、F15（mobile approval）、F18（adaptive mobile） | 多端联动场景 |
| **Sprint 6（4 周）** | F8（voice）、F10（MCP marketplace）、F13（audit signing）、F14（diff context） | 探索性 / 企业向 |

每个 sprint 的工作量假设 1-2 名开发者。具体落地建议以独立设计 doc 形式进入 `docs/design/`，再以 plan 形式进入 `docs/plans/`。

---

## 与已有设计的关系

| 现有设计 | 本文档新建议如何叠加 |
|----------|---------------------|
| `docs/design/knight-design.md` + `knight-auto-evolution.md` | F1 (federated)、F6 (smart cron)、F7 (diff capture) 都依赖 Knight 落地 |
| `docs/design/mobile-subagent-teammate-tabs.md` | F9 (TUI tabs) 是其桌面/TUI 对应方案；F11 (pair) 复用 tab 协议 |
| `docs/design/copilot-style-tui.md` | F5 (telemetry overlay)、F4 (plan visualizer) 是其下游 UI 模块 |
| `docs/design/background-precompact-design.md` | F14 (diff context) 是其优化的下一步 |
| `docs/design/live-stream.md` | F11 (pair session) 是其交互式版本 |
| `docs/design/instance-config.md` | F2 (multi-repo) 是其多 workspace 自然延伸 |
| `docs/plans/2026-05-21-mobile-gateway-resume-design.md` | F18 (adaptive mobile) 在其稳定后实施 |

---

## 备注

- 上述建议均不依赖额外的付费服务或基础设施（除可选 push notification provider 外）
- 涉及 relay 的功能（F1、F11、F15、F18）需要先解决 Round 8/9 的 relay auth + TLS 关键安全项
- 涉及 Knight 的功能（F6、F7）需要 Knight 主体先落地（已有完整设计文档）

后续 agent 可以独立挑选一个建议，创建相应的 `docs/design/<feature>-design.md` 进入详细设计阶段，再创建 `docs/plans/<date>-<feature>-implementation.md` 进入实施阶段。
