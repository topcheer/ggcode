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
- **子代理委派**：spawn_agent 等委派在聊天流中以单行 `Spawn <目标>` 条目呈现，点击展开运行报告（子代理实时流规划中）
- **命令面板**：`⌘/Ctrl+K`（切视图/切主题/新建会话/切换最近会话）；`⌘/Ctrl+N` 新会话；TUI 内 `/webui` 打印地址并唤起浏览器（服务器未运行时友好提示）
- **设置页**：配置（JSON 编辑）、供应商、MCP、IM、A2A、会话、Knight、定时任务（只读）、检查点（只读时间线）九域
- **双主题**：暗色/亮色/跟随系统（主题选择先于首帧渲染，无闪白）；侧栏字号循环按钮（12/13/14/16px，localStorage 持久化）
- **只读 API**：`GET /api/workspace/files?q=`（文件搜索）、`GET /api/cron/jobs`（当前 workspace 定时任务）、`GET /api/checkpoints`（最近编辑检查点）

## 安全

- WS 升级执行同源校验（跨 Origin 拒绝，CSWSH 防护）+ Bearer/query token 鉴权
- 设计文档（token 体系/信息架构/API 契约）：`docs/design/webui-redesign-zcode-inspired.md`
