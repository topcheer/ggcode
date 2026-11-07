# WebUI（内置 Web 界面）

ggcode 内置一个可在任意浏览器访问的 Web 界面。前端自 v1.3.278 起由单文件内联实现迁移为 **React 19 + TypeScript + Vite + Tailwind v4** 工程化构建（设计基准：z-ai/ZCode 设计语言，聊天界面为核心），后端 HTTP/WS API 与 `go:embed` 嵌入机制保持不变。

## 构建与开发

```bash
make webui-build   # 前端构建 → internal/webui/dist（go:embed 嵌入，随二进制分发）
make webui-dev     # 开发模式：Vite 热更新 :5199，/api 反代到本地 Go 服务
go build -tags goolm ./cmd/ggcode   # 重新编译使新 dist 生效
```

- 前端源码位于 `web/`，构建产物输出到 `internal/webui/dist/`（该目录为构建产物，勿手改）
- 开发模式代理目标可用 `GGCODE_WEBUI_TARGET` 覆盖（默认 `http://127.0.0.1:8090`）

## 功能与交互

- **聊天为核心**：首屏 WelcomeScreen + 大输入框；流式 markdown 渲染、思考过程折叠块、工具调用状态卡片（spinner→绿/红+耗时）、乐观 UI（离线消息队列补发）
- **Composer**：`⌘/Ctrl+Enter` 发送、`Shift+Enter` 换行、输入 `@` 唤起文件/技能/Agent 彩色引用弹层、活跃发光态
- **命令面板**：`⌘/Ctrl+K`（切视图/切主题/新建会话）；`⌘/Ctrl+N` 新会话
- **设置页**：配置（JSON 编辑）、供应商、MCP、IM、A2A、会话、Knight 七域
- **双主题**：暗色/亮色/跟随系统（主题选择先于首帧渲染，无闪白）；界面字号基于 14px 可调 token 体系

## 安全

- WS 升级执行同源校验（跨 Origin 拒绝，CSWSH 防护）+ Bearer/query token 鉴权
- 设计文档（token 体系/信息架构/API 契约）：`docs/design/webui-redesign-zcode-inspired.md`
