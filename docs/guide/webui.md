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

- **聊天为核心**：首屏 WelcomeScreen + 大输入框；流式 markdown 渲染、思考过程折叠块、工具调用状态卡片（spinner→绿/红+耗时；diff 类结果逐行红绿着色）、乐观 UI（离线消息队列补发）；截图/图片直接粘贴进输入框（缩略图预览，随消息发送）
- **Composer**：`⌘/Ctrl+Enter` 发送、`Shift+Enter` 换行、输入 `@` 唤起文件（实时搜索 workspace 真实路径）/技能/Agent 彩色引用弹层、活跃发光态
- **子代理委派**：spawn_agent 等委派在聊天流中以单行 `Spawn <目标>` 条目呈现；右栏自动显示**子代理实时流监视器**（推理/文本增量，完结转状态色，空闲自动隐藏）——TUI/WebUI/桌面共用同一 `StreamEventSubagent*` 事件契约
- **命令面板**：`⌘/Ctrl+K`（切视图/切主题/新建会话/切换最近会话）；`⌘/Ctrl+N` 新会话；TUI 内 `/webui` 打印地址并唤起浏览器（含 `#token=`，服务器未运行时友好提示）
- **公网隧道**：`/et on` 经 etunnel（服务端由 SDK 内置默认 `wss://t.etun.uk/tunnel` 决定）把本 WebUI 映射为公网 https 域名并打印完整 URL（含 `#token=` 鉴权，免费计划 4h/1Mbps）；`/et off` 关闭；`/et show` 查看状态。付费定制域名：`/et setup <子域名> <token>` 持久化到 config `etunnel` 节（token 在 /api/config 面自动脱敏）。Go SDK 以已发布模块 `github.com/topcheer/etunnel`（v1.2.0）引入，无需本地 replace（官方 SDK 另有 pub.dev 等 12 语言发行）
- **聊天控制**：顶部三下拉——切换模型（列表来自配置）、选择推理力度（复用 `/effort` 循环语义）、切换权限模式（supervised/bypass）；全部经 `POST /api/chat/commands` 走 TUI slash 命令全链（校验/busy 语义/反馈消息零复制），变更反馈以系统消息回流聊天流
- **会话切换**：左侧栏列示本 workspace 全量会话；当前会话高亮锁定，其余点击即切——实际切换由后端 TUI resume 全链执行（`POST /api/sessions/switch`），agent 运行中（busy）会话返回 409 锁定不可切；被他实例持有 flock 的会话显示 🔒 并禁点（`GET /api/sessions` 的 `locked_by_pid` 字段，tooltip 显示持有者 PID），真实锁竞争拒绝由切换时 `TryAcquireSessionLock` 兜底
- **设置页**：配置（JSON 编辑）、常规（主题/字号/模型/effort/mode/端点配置）、供应商、MCP、IM、A2A、会话、定时任务（只读）八域
- **双主题**：暗色/亮色/跟随系统（主题选择先于首帧渲染，无闪白）；侧栏字号循环按钮（12/13/14/16px，localStorage 持久化）
- **只读 API**：`GET /api/workspace/files?q=`（文件搜索）、`GET /api/cron/jobs`（当前 session 定时任务,真相源为活 scheduler 随会话切换;独立 webui 模式回落磁盘聚合）、`GET /api/checkpoints`（最近编辑检查点）

## 安全

- WS 升级执行同源校验（跨 Origin 拒绝，CSWSH 防护）+ Bearer/query token 鉴权
- 设计文档（token 体系/信息架构/API 契约）：`docs/design/webui-redesign-zcode-inspired.md`
