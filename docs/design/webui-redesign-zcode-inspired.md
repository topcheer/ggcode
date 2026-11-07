# WebUI 视觉/体验重构方案 — 对标 ZCode（聊天为核心）

> 参考：zai-org/ZCode `DESIGN.md` + `packages/ui/src/styles.css`（Tailwind v4 + shadcn 组件源码模式 + Lexical prompt editor + streamdown 流式 markdown）
> 现状：`internal/webui/dist/index.html` 单文件 2003 行（内联 CSS/JS、零组件化），25 个 REST 端点 + `/api/chat/ws`（gorilla/websocket），`go:embed all:dist` 嵌入。

## 1. ZCode 设计精髓提炼（我们要抄什么）

| 精髓 | ZCode 做法 | 对 ggcode 的意义 |
|---|---|---|
| 产品性格 | calm / dense / operational：桌面优先、高信息密度、键盘驱动、长会话可读；禁止营销式大间距与花哨渐变 | WebUI 是工作台不是官网，一切间距/字号向密度与可读性倾斜 |
| Token 化主题 | 全套语义 CSS variables（background/surface/card/popover/menu/input 分层），`color-mix` 派生 hover/选中态，主题=换变量 | 双主题（亮/暗）一次到位，杜绝现在的硬编码 GitHub-dark |
| 可调字阶 | `text-ui-*` 全部派生自 `--ui-font-size`（默认 14px）；代码/终端独立字号；禁止任意 px 字号 | 用户可调界面字号而不破坏布局 |
| 聊天输入=富文本 | Lexical prompt editor + mention 节点（@file=sky / @skill=violet / @command=slate，各配 bg/hover/fg 三 token） | ggcode 的 @file @skill @agent 体系直接对应 |
| Composer 活跃态 | Highspeed 发光边框+粒子层（仅活跃输入框，不外溢普通输入） | "丝滑感"的关键情绪表达，可做简化版（发光边框+呼吸动画） |
| 工具调用块 | ToolCallBlocks 折叠块 + 状态灯；轨迹五色（user/assistant/reasoning/tool-call/tool-result） | agent 执行过程可视化是 coding agent UI 的灵魂 |
| 桌面级细节 | `data-document-hidden` 禁动画防内存泄漏；`text-wrap-phrase` 中文短语断行；全局 focus outline 清理；磨砂 toast（blur+rounded-2xl） | 这些细节合起来才是"桌面应用的丝滑" |
| 流式渲染 | streamdown：流式 markdown 无闪烁渲染 | 现在聊天是纯文本追加，必须换 |

## 2. 信息架构（聊天即主角）

```
┌────────┬──────────────────────────────┬──────────┐
│ 会话栏  │  聊天流（max-w 860 居中）        │ 侧栏 Pane │
│ 232px  │                              │ (可折叠)  │
│ TaskList│  user 气泡(极淡 surface)       │ 预览/diff │
│ 搜索/新建│  assistant 全宽 markdown       │ /终端     │
│        │  tool 块(折叠+状态灯)           │          │
│        │ ┌──────────────────────────┐ │          │
│ 底部:  │ │ composer（发光活跃态）        │ │          │
│ 状态点  │ │ @mention · 模型 · 发送/停止   │ │          │
└────────┴─┴──────────────────────────┴─┴──────────┘
```
- **首屏 = WelcomeScreen**：居中大 composer + 快捷入口（现有 7 个设置域收进右上齿轮，不占主导航）
- 七大域（config/vendors/mcp/im/a2a/sessions/knight）保留为设置页子路由，视觉降权
- 侧栏 Pane 按需弹出（diff 预览/文件树），移动端降级为底部抽屉
- 键盘映射表（全流程键盘可达）

| 快捷键 | 动作 | 作用面 |
|---|---|---|
| `⌘K` / `Ctrl+K` | 命令面板（切会话/跳设置/切主题） | 全局 |
| `⌘N` | 新建会话 | 全局 |
| `⌘⏎` | 发送消息（Shift+⏎ 换行） | Composer |
| `Esc` | 中断生成 / 关闭弹层（两级） | Composer/弹层 |
| `J` / `K` | 会话列表下/上导航 | 会话栏聚焦时 |
| `⌘B` | 折叠/展开侧栏 Pane | 全局 |
| `⌘/` | 快捷键速查浮层 | 全局 |

## 3. 设计 Token（ggcode 主题变量表·直接采用）

```css
:root {
  --ui-font-size: 14px;                 /* 可调基准，text-ui-* 全部派生 */
  /* 三层背景分离（Zai Dark 参考值） */
  --color-background: #101014;          /* neutral-900 级 */
  --color-background-alt: #16161c;
  --color-sidebar: #14141a;
  --color-card: #1b1b22;
  --color-popover: #1e1e26;
  --color-input: #20202a;
  /* 文本三级 */
  --color-foreground: #e8e8ee;
  --color-foreground-subtle: rgba(232,232,238,.62);
  --color-foreground-subtlest: rgba(232,232,238,.4);
  /* 品牌 + 交互 */
  --color-brand: #38bdf8;               /* sky-500，延续现有 accent 蓝 */
  --color-primary: var(--color-brand);
  --color-selected: rgba(255,255,255,.1);
  /* 语义反馈（surface 族成对出现，禁止借用语义色提升音量） */
  --color-success: #16a34a;  --color-success-fg: #f0fdf4;
  --color-success-surface: rgba(22,163,74,.12);  --color-success-surface-fg: #4ade80;
  --color-destructive: #dc2626;  --color-destructive-fg: #fef2f2;
  --color-destructive-surface: rgba(220,38,38,.10);  --color-destructive-surface-fg: #f87171;
  --color-warning: #ca8a04;  --color-warning-fg: #fefce8;
  --color-warning-surface: rgba(202,138,4,.12);  --color-warning-surface-fg: #facc15;
  /* mention 节点色族（@file/@skill/@agent） */
  --color-file-node: rgba(2,132,199,.16);      --color-file-node-fg: #38bdf8;
  --color-skill-node: rgba(124,58,237,.16);    --color-skill-node-fg: #a78bfa;
  --color-agent-node: rgba(217,119,6,.16);     --color-agent-node-fg: #fbbf24;
  /* 轨迹五色（会话详情/回放用） */
  --color-trajectory-user: #2563eb;  --color-trajectory-assistant: #0f766e;
  --color-trajectory-reasoning: #7c3aed;  --color-trajectory-tool-call: #d97706;
  --color-trajectory-tool-result: #0284c7;
  /* composer 活跃态 */
  --shadow-composer-active: 0 0 0 1px rgba(56,189,248,.4), 0 0 24px rgba(56,189,248,.12);
}
.light {
  --color-background: #fafafa;  --color-background-alt: #f4f4f5;
  --color-sidebar: #f4f4f5;  --color-card: #ffffff;  --color-popover: #ffffff;
  --color-input: #ffffff;
  --color-foreground: #3f3f46;  /* neutral-700 级，ZCode 同款 */
  --color-foreground-subtle: rgba(63,63,70,.62);  --color-foreground-subtlest: rgba(63,63,70,.4);
  --color-brand: #0ea5e9;  /* sky-500 */
  --color-selected: rgba(0,0,0,.08);
  /* hover/选中态用 color-mix 派生，不写死：
     --color-hover: color-mix(in oklab, var(--color-foreground) 6%, transparent);
     --color-surface-hover: color-mix(in oklab, var(--color-foreground) 4%, transparent); */
}
```
规则：组件只准用语义 token，禁止 `text-white/60` 式 ad hoc 值；文本层级优先于加边框。

## 4. 丝滑感清单（体验重构的核心交付）

1. **流式 markdown 无闪烁**：streamdown 同类实现（增量块渲染+光标块动画），代码块高亮在流结束后 freeze
2. **工具调用块**：折叠卡片 + 状态灯（spinner→success 绿/destructive 红）+ 耗时徽章；diff 内嵌绿/红 token 着色
3. **Composer**：多行自适应高度、@mention 弹层（文件/技能/agent 彩色节点）、⌘⏎ 发送、Esc 中断、活跃发光边框、底部模型选择；活跃态动画规格：`--shadow-composer-active` 外发光 + 2.4s 呼吸循环（opacity .12↔.20 ease-in-out），**不做粒子层**（ZCode Highspeed 粒子依赖其桌面 runtime，Web 简化为纯 CSS 发光+呼吸，效果保留 90%、成本 10%）
4. **乐观 UI**：消息立即上屏（待确认态描边），WS 断线自动重连+队列补发，失败红边+重试按钮
5. **键盘全流程**：⌘K 命令面板（切会话/跳设置）、⌘N 新会话、J/K 会话列表导航——桌面感的一半来自键盘
6. **微动效**：消息入场 fade+slide 120ms、侧栏悬停 80ms、按钮按压 scale(.98)；页面切换无跳转（SPA 视图切换 + 视线锚定在 chat 主体）
7. **中文排版**：`text-wrap-phrase`（word-break: auto-phrase 渐进增强）+ CJK mono 字体栈（ZCode 修复过的 Consolas 无中文字形问题直接规避）
8. **性能卫生**：`document.hidden` 时禁动画（防后台流式内存累积）；虚拟滚动长会话列表；图片懒加载
9. **磨砂 toast**：`bg-toast/60 + backdrop-blur + rounded-2xl`，统一由 toast 组件定位

## 4.5 ggcode 消息与工具渲染全景（深度挖掘版）

**方法论：不是罗列用户举例的几个，而是对齐仓库现成分类学。** 两个源码事实：①TUI 渲染层（`internal/chat/tools.go`）已有成熟的 10 类工具渲染分类+17 个特殊工具特判——WebUI 渲染组件直接对齐这套分类学，不发明新类目；②工具注册面（`agentruntime/subsystems.go`+`interactive_core.go`）共 **50+ 工具**，按渲染形态天然分三组。

### 4.5.1 同步工具（一次性调用→结果）：现有 ToolCallChunk/Done/Result 事件足够，只需前端卡片组件

对齐 TUI 分类学的卡片组件矩阵：

| 卡片组件 | 覆盖工具 | 特化渲染 |
|---|---|---|
| FileCard | read_file/write_file/edit_file/multi_edit_file/notebook_edit/apply_patch | **editdiff 特判**：绿红 diff 内嵌视图（TUI 已有同判） |
| SearchCard | search_files/grep/glob/code_search | **searchcount 特判**：结果数徽章+首条命中预览 |
| ListCard | list_directory/glob | 树形/列表演染 |
| WebCard | web_fetch/web_search | 引用卡（favicon+标题+URL） |
| GitCard | git_status/git_diff/git_log/git_add/commit/… | **gitstatus/gitdiff/gitlog 特判**：彩色状态行/diff/log 时间线 |
| BashCard / CmdCard | run_command/start_command + interactive 系 | 终端风 mono 块+退出码徽章+后台 job 交互（读输出/停实验） |
| LspCard | lsp_* 全系 | 符号+位置引用卡 |
| TodoCard | todo_write/task_create/update/list/stop | **实时 checkbox 面板**（ZCode TodoPanel 同构，ggcode 已有 TodoTask 结构） |
| SkillCard | skill/skill 导出导入 | skill 名+参数徽章 |
| WorktreeCard | enter_worktree/exit_worktree/merge_worktree | worktree 名+分支+merge 状态徽章 |
| MacroCard | macro/define_tool | 宏名+步骤链可视化 |
| MemoryCard | save_memory/list_memory/recall_experience/recall_toolflow | 记忆键+召回命中卡 |
| ConfigCard | config | 键+脱敏值（掩码纪律沿用现有 im_masking 思路） |
| A2ACard | a2a_discover/a2a_remote/send_task | **TUI 已有 a2a_discover 特判**：目标+skill 徽章 |
| GenericCard | 其余全部 | 名称+参数折叠+结果折叠（兕底） |

折叠/列表交互语义直接继承 TUI 已有实现（internal/chat/disclosure.go + list.go，已核）：①**折叠态状态机**——BaseToolItem.ToggleExpanded/Expanded + 截断计数提示（"…N more lines"），WebUI 卡片默认折叠+按需展开+截断行数徽章与 TUI 语义一致；②**列表跟随/裁剪语义**——List.Follow 仅在 AtBottom 时跟随新输出 + SetMaxItems 上限裁剪（长会话内存上限），WebUI 虚拟滚动需继承同语义（贴底自动滚、离底不打扰、上限裁剪防内存累积）。

### 4.5.2 后台生命周期工具（状态跨多次调用持续演化）：需要专用 WS 事件

| 概念 | 工具面（已核） | 专用渲染 |
|---|---|---|
| **Subagent**（spawn_agent/wait_agent/list_agents） | 4 工具 | 嵌套子时间线卡：agent_id+model+worktree 徽章、进度/结果拉取态、可展开子轨迹 |
| **Team/Swarm**（team_create/delete、teammate_spawn/list/shutdown/results、swarm_task_create/list/claim/complete、review_digest/decide） | 12 工具 | 团队看板卡：成员列表+任务板列+评审 digest 折叠——ggcode 特色，ZCode 无对应物 |
| **Named Agent 模板**（create/delete/list/use_namedagent） | 4 工具 | 模板管理卡：名称+model+工具白名单徽章；use 时同 subagent 卡 |
| **Cron**（create/delete/list/update/pause/resume/get） | 7 工具 | 系统事件卡：cron 表达式 mono+周期/一次性徽章+触发史；**TUI 已有 cronbody 特判**，WebUI 直接对应 |
| **研究编排**（delegate/llm_call/best_of_n/workflow_run） | 4 工具 | 编排进度卡：workflow 节点图/best_of_n 候选并列/delegate 目标徽章 |
| **MCP**（capabilities/prompt/resource+elicitation） | 3+工具 | MCP 能力卡+elicitation 表单（见 4.5.3） |

### 4.5.3 交互阻塞型（需要用户输入才能继续）：需要 WS 往返协议，ZCode 有对应物但形态不同

| 概念 | 现有面 | 渲染形态 |
|---|---|---|
| **ask_user 结构化问卷** | AskUser 工具（single/multi/text+choices+freeform） | **问卷卡**：选项卡式问题组+radio/checkbox+自由输入+提交通道——ZCode AskUserQuestion 是单选对话式，ggcode 多 tab 问卷是特色 UI，独立设计 |
| **permission/approval** | ApprovalFunc/DiffConfirmFunc | 权限请求卡：操作摘要+风险色+allow/deny 按键；diff 确认内嵌 editdiff 视图 |
| **Plan mode**（enter/exit_plan_mode） | TUI 已有 exit_plan_mode 特判 | 计划卡：plan 正文块+批准/修改按钮，退出时高亮验收标准 |
| **checkpoint/rewind** | checkpoint 包+undo_edit | 会话头时间轴徽章+回滚点标记（点击回滚）——桌面级特色，需 API 面 |
| **steering**（中途中断/改道） | midrun steering 面 | 输入框上方"steering 待注入"提示条 |

## 5. 后端 API 缺口评估（按三形态分组，Provider 契约不动）

**总判断：同步组零后端改动；交互组需 WS 往返协议（新增）；后台组需生命周期事件+2 REST 端点；桌面状态组需 REST 面。**

1. **同步组**：零改动——现有 ToolCallChunk/ToolCallDone/ToolResult 已携带全部所需；前端按 4.5.1 分类做卡片
2. **后台生命周期组**：WS 新增事件（System 事件 payload 扩展，向后兼容）：
   - `subagent_spawn/progress/done`：{agent_id, model, worktree, status, result_summary}——**已核实：agent 层已有 `SubagentCompletion` 事件结构（internal/agent/subagent_notify.go:11），缺的是 webui 透出桥接；spawn/progress 需新增**
   - `teammate_update`：{team_id, member, task_board_state}（同时覆盖 swarm_task 系事件）
   - `cron_fired`：{job_id, schedule, one_shot, result_summary}
   - `workflow_progress`：{run_id, step_id, layer, status}（覆盖 best_of_n 候选态）
3. **交互阻塞组**：WS 往返协议（新增，这是最大的真缺口）：
   - `interaction_request`：{kind: ask_user/permission/plan/diff_confirm, payload} → 前端渲染对应卡 → 回发 `interaction_response` {id, answer}。ZCode 用 PermissionDialog/ElicitationDialog（桌面进程内），WebUI 必须走 WS 往返——**全新协议面，P1 核心工作**
4. **REST 只读新增**：`GET /api/todos`（会话 todo 态恢复）、`GET /api/cron`（任务列表+触发史）、`GET /api/checkpoints`（回滚点列表）、`GET /api/teams`（团队/任务板）
5. **历史回放**：`/api/chat/history` 已含 reasoning；建议附 `agent_tree` 侧车（subagent 父子树）与 `interactions` 侧车（历史问卷结果只读展示）
6. **顺手修**：WS `CheckOrigin: true`（server_websocket.go:19）收紧为 Origin 白名单/同源校验+鉴权 token 下发（CSWSH）

## 6. 技术栈与实现路径

**栈**（维持上轮结论，落到视觉层）：React 18 + TS + Vite + Tailwind v4；组件走 shadcn 源码自持模式（抄 radix 风格组件进 repo，不引大组件库）；Lexical 二期再上（一期 textarea+mention 弹层）；markdown 用 streamdown 同类方案。

**后端零契约改动**：25 个 REST 端点 + chat WS 协议原样；产物仍 build 进 `internal/webui/dist` 走 `go:embed`。顺手修：WS `CheckOrigin` 收紧 + 鉴权下发。

**阶段**（每阶段独立可验收，文件级改动清单）：
- **P0 骨架**（验收：双主题目检+路由骨架可导航）：新建 `web/`（Vite 脚手架：`package.json`/`vite.config.ts`（dev proxy→:8090 或现 webui 端口）/`tsconfig.json`）；`web/src/styles/tokens.css`（§3 变量表落码）；`web/src/App.tsx`+三栏骨架组件；`Makefile` 加 `webui-build` target（pnpm build→拷贝进 `internal/webui/dist`）；`internal/webui/dist/index.html` 原样保留为回退开关
- **P1 聊天页**（验收：流式 60fps+断网重连≤3s 不丢消息+快捷键全流程）：`web/src/pages/Chat.tsx`；`web/src/chat/MessageList.tsx`（虚拟滚动）/`MessageBubble.tsx`/`ToolCallBlock.tsx`（状态机：running→success/destructive+耗时徽章）/`Markdown.tsx`（流式增量渲染）；`web/src/chat/Composer.tsx`（mention 弹层+发光活跃态）；`web/src/ws/client.ts`（重连+队列补发）；`web/src/store/`（Zustand：会话/消息/连接态）
- **P2 设置域七页**（验收：25 端点全量表单读写回归）：`web/src/pages/settings/{Config,Vendors,Mcp,Im,A2a,Sessions,Knight}.tsx` + `web/src/api/`（按域分模块类型化客户端）
- **P3 收尾**（验收：e2e 聊天流回归绿）：删 `internal/webui/dist/index.html` 旧件；`server_e2e_test.go` 扩聊天流用例；README/docs 同步

## 7. 验收标准
- 滚动/流式全程 60fps；首屏（embed 本地）< 1s；双主题正文对比度 ≥ 4.5:1
- 流式输出零闪烁零跳版；断网重连 ≤ 3s 自动恢复且不丢消息
- 键盘可完成：新建会话→发送→中断→切会话→开设置 全流程
- 390px/1200px 两档宽度 + 亮暗双主题四矩阵目检通过
