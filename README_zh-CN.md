# ggcode

<p align="center">
  <img src="ggcode_cli_banner_1775456774280.png" alt="ggcode" width="600" />
</p>

**[English](README.md)** | **中文**

终端 AI 编程助手。能理解代码仓库、编辑文件、执行命令、提交代码 — 拥有精致 TUI 界面、可恢复会话、多 Agent 支持。

---

## 安装

**macOS / Linux:**
```bash
curl -fsSL https://ggcode.dev/install.sh | bash
```

**Windows:**
```powershell
irm https://ggcode.dev/install.ps1 | iex
```

**其他方式:** [Homebrew](docs/guide/install.md#macos) · [winget](docs/guide/install.md#windows) · [npm](docs/guide/install.md#npm) · [pip](docs/guide/install.md#pip) · [源码编译](docs/guide/install.md#build-from-source)

> 所有安装脚本默认非特权安装（不需要 sudo / 管理员权限）。

## 快速开始

```bash
# 1. 进入你的项目目录运行 ggcode
cd your-project
ggcode

# 2. 首次启动时按提示配置 API Key（交互式）
#    或直接指定:
#    首次启动时通过交互式向导配置

# 3. 开始编程 — 直接输入你的需求
```

第一次用？阅读 **[新手指南](docs/guide/getting-started.md)**。

---

## LAN Chat — 局域网实时协作

<p align="center">
  <strong>零配置 P2P · 同一局域网内 ggcode 实例自动发现、自动连接</strong>
</p>

每个 ggcode 实例通过 mDNS 自动发现同一局域网内的其他实例，
无需中继服务器、无需注册、无需任何配置。

| 特性 | 说明 |
|---------|-------------|
| **自动发现** | mDNS 自动发现对端 — 零配置 |
| **私信 & 广播** | 一对一私聊、团队群发、全网广播 |
| **Agent 间通信** | 通过 `@agent` 路由消息，实现跨实例委托 |
| **状态感知** | 显示在线状态、项目、角色、团队、编程语言 |
| **文件分享** | 拖拽发送附件（最大 10 MB）|
| **已读回执** | 跟踪消息送达和已读状态 |
| **全平台** | TUI、桌面端、守护进程模式均支持 |
| **隐私安全** | 消息只在本局域网内传输，不经过外部服务器 |

内置社区密钥确保不同 A2A 认证配置的实例之间也能零配置互通。

📖 **[完整局域网聊天文档 →](docs/guide/lan-chat.md)**

---

## 核心功能

- **代码理解** — 读取、理解并编辑整个项目
- **完整开发工具** — 文件编辑、Shell 命令、Git、LSP、搜索
- **MCP 集成** — 连接外部工具和数据源
- **gRPC 插件** — 用 Go、Python、Node.js 等任意语言编写自定义工具插件
- **多 Agent** — 并行工作、团队协作、A2A 协议
- **[局域网聊天](docs/guide/lan-chat.md)** — 零配置 P2P 实时消息，同局域网实例自动发现与协作
- **编辑器集成** — JetBrains、Zed 等 ACP 兼容编辑器
- **WebUI** — 内置 Web 界面，浏览器即可使用
- **IM 集成** — 从 QQ、Telegram、Discord、Slack、飞书、钉钉 控制
- **Harness 工作流** — 隔离任务执行 + 代码审查 + 合并
- **定时任务** — Cron 定时任务、提醒、后台自动化
- **可恢复会话** — 随时暂停和恢复对话
- **桌面 + 移动端** — macOS、Windows、Linux、iOS、Android 原生应用

## 平台

| 平台 | 安装 |
|------|------|
| **CLI** (macOS/Linux/Windows) | 本仓库 — 主要交互界面 |
| **[桌面端](docs/guide/desktop.md)** | macOS / Windows / Linux 原生应用 |
| **[移动端](docs/guide/mobile.md)** | [iOS (App Store)](https://apps.apple.com/us/app/ggcode-mobile/id6770855612) / [Android (Google Play)](https://play.google.com/store/apps/details?id=gg.ai.ggcode.mobile) |

## 文档

| 文档 | 说明 |
|------|------|
| [新手指南](docs/guide/getting-started.md) | 首次使用、API Key 配置、基本操作 |
| [安装指南](docs/guide/install.md) | 所有平台的安装方法 |
| [CLI 参考](docs/guide/cli-reference.md) | 命令、参数、管道模式 |
| [模型配置](docs/guide/providers.md) | LLM 厂商和端点配置 |
| [斜杠命令](docs/guide/slash-commands.md) | TUI 内命令参考 |
| [权限模式](docs/guide/modes.md) | 监督 / 计划 / 自动 / 旁路 / 自动驾驶 |
| [MCP 服务器](docs/guide/mcp.md) | 通过 MCP 连接外部工具 |
| [gRPC 插件](docs/guide/grpc-plugins.md) | 编写和安装自定义工具插件（Go / Python / Node.js）|
| [IM 集成](docs/guide/im-integration.md) | QQ / Telegram / Discord / Slack / 飞书 / 钉钉 |
| [Harness 工作流](docs/guide/harness.md) | 隔离任务 + 审查 + 合并 |
| [A2A 协议](docs/guide/a2a.md) | 跨实例 Agent 委托 |
| [ACP / 编辑器](docs/guide/acp.md) | JetBrains、Zed 等 ACP 兼容编辑器 |
| [任务委托](docs/guide/delegation.md) | 委托任务给 Copilot、Claude、Cursor 等外部 Agent |
| [多 Agent 模式](docs/guide/multi-agent-modes.md) | 子 Agent、团队协作 |
| [局域网聊天](docs/guide/lan-chat.md) | 实例之间的局域网实时消息 |
| [配置参考](docs/guide/configuration.md) | 完整配置文件说明 |
| [项目记忆](docs/guide/project-memory.md) | GGCODE.md / AGENTS.md / CLAUDE.md |
| [技能系统](docs/guide/skills.md) | 可复用工作流模式 |
| [Shell 补全](docs/guide/shell-completion.md) | bash / zsh / fish / powershell |

## 快速链接

- **[下载桌面端](https://ggcode.dev)** · **[发布版本](https://github.com/topcheer/ggcode/releases)**
- **[报告问题](https://github.com/topcheer/ggcode/issues)** · **[功能建议](https://github.com/topcheer/ggcode/issues)**

## 许可证

MIT
