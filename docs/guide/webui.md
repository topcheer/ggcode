# WebUI（内置 Web 界面）

ggcode 内置一个可在任意浏览器访问的 Web 界面。前端自 v1.3.278 起由单文件内联实现迁移为 **React 19 + TypeScript + Vite + Tailwind v4** 工程化构建（设计基准：z-ai/ZCode 设计语言，聊天界面为核心），后端 HTTP/WS API 与 `go:embed` 嵌入机制保持不变。

## 构建与开发

```bash
make webui-build   # 前端构建 → internal/webui/dist（go:embed 嵌入，随二进制分发）
make build-all     # 一站式：webui-build + 制品入库同步 + Go 二进制
make webui-dev     # 开发模式：Vite 热更新 :5199，/api 反代到本地 Go 服务
make webui-sync-artifacts   # dist/assets 被 gitignore 挡住，入库需显式同步（前端变更后执行）
go build -tags goolm ./cmd/ggcode   # 重新编译使新 dist 生效
```

- 前端源码位于 `web/`，构建产物输出到 `internal/webui/dist/`（该目录为构建产物，勿手改）
- 开发模式代理目标可用 `GGCODE_WEBUI_TARGET` 覆盖（默认 `http://127.0.0.1:8090`）

## 功能与交互

- **聊天为核心**：首屏 WelcomeScreen + 大输入框；流式 markdown 渲染、思考过程折叠块（旁注形态：无外框、一条细竖线引导、中性弱色文字，比正文与工具卡更轻；流式期间展开贴底，完成后收起为一行并显示行数）、乐观 UI（离线消息队列补发）；截图/图片直接粘贴进输入框（缩略图预览，随消息发送）
- **工具卡片（tool call / tool result）**：头部始终给结论——状态灯 → 类别图标 → **展示名**（服务端 `tool.DescribeTool`，与 TUI/IM/desktop/移动端同源，永不显示 `read_file` 这类裸名；MCP 工具显示 `Server › Tool`）→ 一行目标 → 结果徽章（`tool.DescribeToolResult` 摘要或按类别推导：行数 / 匹配数 / 失败原因 / 退出码）→ 耗时。正文按**类别**渲染而非 JSON 原文：命令 → shell 块（description 标题 + 完整命令）+ 终端输出（stdout / `STDERR:` / `Command failed:` 分段）；读文件 → `cat -n` 行号代码视图 + 语言标签 + 范围；编辑 → `old_text → new_text` 内联 diff（红/绿、`−n +m` 计数），结果里的 diff 同样着色；写文件 → 内容预览；grep/glob → `path:line: text` 按文件分组、计数；网页 → markdown 渲染；todo → 清单；task/cron/lanchat → 键值表 + 摘要载荷；其他/MCP → 参数键值表 + 文本/JSON 结果。大输出默认截断（200 行 / 20k 字符，可"显示全部"），每卡可切到"原始 JSON"。默认展开规则：错误、编辑、todo、小结果直接展开，大输出折叠由徽章给结论。结果按 `id` 精确关联（并行同名调用不串），历史回填时 tool_result 跨消息按 `tool_id` 关联，并剥离面向模型的 `<untrusted_tool_output>` 围栏。前端渲染冒烟测试：`cd web && npm run test:render`
- **左侧栏可折叠（沉浸模式）**：桌面宽度下 `⌘/Ctrl+B`、聊天页左上角的侧栏开关、或命令面板「收起/展开左侧栏」三处均可切换，收起后对话区占满整宽；偏好写入 `localStorage`（`ggcode-sidebar-collapsed`），刷新后保持。窄屏仍是抽屉（`☰`），不存在折叠态。导航项（会话 / 设置 / 共享 / 隧道）使用随主题取色的线性 SVG 图标（`web/src/components/NavIcon.tsx`），底栏显示 `WebUI · 已连接/连接中/未连接` 状态点与当前工作区名；右侧活动栏仅在有内容（Spawn 详情 / 任务清单 / 子代理流 / Cron 事件）时才在桌面占位，空栏不再吃掉对话宽度；欢迎页快捷入口的说明文案面向用户，不显示研发阶段编号
- **插入消息队列镜像**：agent 忙碌时从页面发送的消息进入 TUI 队列，输入框上方显示居中的窄条「排队中 N 条 · 最近一条（单行截断）· 本轮结束后依次发送」。服务端每个队列事件都附带全量快照与递增 `seq`（`internal/webui/server_queue.go`），前端以快照为准、丢弃乱序旧快照——带图片的消息出队后横幅必定消失，事件丢失也会被下一个事件/重连的快照自愈
- **Composer**：`⌘/Ctrl+Enter` 发送、`Shift+Enter` 换行、输入 `@` 唤起文件（实时搜索 workspace 真实路径）/技能/Agent 彩色引用弹层、活跃发光态
- **子代理委派**：spawn_agent 等委派在聊天流中以单行 `Spawn <目标>` 条目呈现；右栏自动显示**子代理实时流监视器**（推理/文本增量，完结转状态色，空闲自动隐藏）——TUI/WebUI/桌面共用同一 `StreamEventSubagent*` 事件契约；swarm 队友（tm-*）生命周期事件同样经此面板流式呈现（`internal/tui/repl.go` 将 `teammate_text`→SubagentDelta、`teammate_idle`→SubagentDone 复用既有通道，#5881）
- **命令面板**：`⌘/Ctrl+K`（切视图/切主题/新建会话/切换最近会话）；`⌘/Ctrl+N` 新会话；TUI 内 `/webui` 打印地址并唤起浏览器（含 `#token=`，服务器未运行时友好提示）
- **公网隧道**：`/et on` 经 etunnel（服务端由 SDK 内置默认 `wss://t.etun.uk/tunnel` 决定）把本 WebUI 映射为公网 https 域名并打印完整 URL（含 `#token=` 鉴权，免费计划 4h/1Mbps）；`/et off` 关闭；`/et show` 查看状态。付费定制域名：`/et setup <子域名> <token>` 持久化到 config `etunnel` 节（token 在 /api/config 面自动脱敏）。Go SDK 以已发布模块 `github.com/topcheer/etunnel`（v1.2.0）引入，无需本地 replace（官方 SDK 另有 pub.dev 等 12 语言发行）。**服务端客户端版本门禁**：etunnel 服务端（v1.7.40+）会拒绝不上报版本或落后超过 5 个 patch 的 SDK，握手返回 `client_outdated`（错误文本含升级 URL）；ggcode 原样透出服务端文本并追加"升级 ggcode"提示（`describeTunnelError`）。内嵌 SDK 版本落后于服务端要求时 `/et on` 会直接失败——升级 ggcode 即修复，这是依赖必须跟随 SDK 最新版的原因
- **侧栏双隧道入口**：①📱 共享——点按即发起 `/share`，弹层显示连接二维码（PNG）+ Connect URL，mobile app 扫码接入后弹层自动隐藏（轮询 `GET /api/tunnel/status` 的 `share.connected`），可显式停止；②⇄ 隧道——点按即 `/et on`，弹层显示公网 URL 可直接点击（关弹层不关隧道，可显式停止）。启停均走 `POST /api/chat/commands` slash 全链，状态真相源为活 scheduler 快照
- **审批与问卷（#5865）**：supervised 模式下工具调用的 ask 审批、以及 `ask_user` 问卷，和 TUI/IM/移动端一样出现在页面顶部横幅——审批显示工具名与输入（可展开）并提供「允许 / 总是允许 / 拒绝」，问卷按题型渲染单选/多选/自由输入；任一前端（TUI 键盘、IM 回复、移动端、本页）先应答即生效，其余前端随即清除（`interaction` / `interaction_resolved` 事件，连接时回放仍在等待的请求；应答走 `interaction_response`）。TUI 宿主与 daemon 宿主的 WebUI 行为一致
- **聊天控制**：顶部三下拉——切换模型（列表来自配置）、选择推理力度（复用 `/effort` 循环语义）、切换权限模式（supervised/bypass）；全部经 `POST /api/chat/commands` 走 TUI slash 命令全链（校验/busy 语义/反馈消息零复制），变更反馈以系统消息回流聊天流
- **会话切换**：左侧栏列示本 workspace 全量会话；当前会话高亮锁定，其余点击即切——实际切换由后端 TUI resume 全链执行（`POST /api/sessions/switch`），agent 运行中（busy）会话返回 409 锁定不可切；被他实例持有 flock 的会话显示 🔒 并禁点（`GET /api/sessions` 的 `locked_by_pid` 字段，tooltip 显示持有者 PID；`locked_by_self`=本实例自己打开的会话，不计入锁定态），真实锁竞争拒绝由切换时 `TryAcquireSessionLock` 兜底
- **设置页**：配置（JSON 编辑）、常规（主题/字号/模型/effort/mode/端点配置）、供应商、MCP、IM、A2A、会话、定时任务（只读）八域
- **双主题**：暗色/亮色/跟随系统（主题选择先于首帧渲染，无闪白）；侧栏字号循环按钮（12/13/14/16px，localStorage 持久化）
- **移动端适配**：右活动面板 lg 以下改为顶栏“☰ 面板”唤起的全屏抽屉（遮罩点击关闭）；设置页流式 padding + 网格小屏收单列；命令面板 92vw 封顶；触屏设备（pointer: coarse）输入框回车直发、提示文案同步切换，顶栏 ⌘ 钮等价唤起命令面板（无组合键场景）
- **右栏可隐藏（桌面）**：lg+ 顶栏 pin 钮收起/展开右侧活动面板（状态持久化 localStorage `ggcode-pane-pinned`，收起后主聊天区占满全宽）；移动端 overlay 语义不受影响
- **品牌标识**：侧栏品牌位、顶栏与 favicon 使用与 mobile app 同款 icon（`web/public/icon.png`）
- **只读 API**：`GET /api/workspace/files?q=`（文件搜索）、`GET /api/cron/jobs`（当前 session 定时任务,真相源为活 scheduler 随会话切换;独立 webui 模式回落磁盘聚合）、`GET /api/checkpoints`（最近编辑检查点）

## 安全

- WS 升级执行同源校验（跨 Origin 拒绝，CSWSH 防护）+ Bearer/query token 鉴权
- 设计文档（token 体系/信息架构/API 契约）：`docs/design/webui-redesign-zcode-inspired.md`
