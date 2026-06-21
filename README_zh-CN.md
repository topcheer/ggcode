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
#    OpenAI:      ggcode --vendor openai
#    Anthropic:   ggcode --vendor anthropic

# 3. 开始编程 — 直接输入你的需求
```

第一次用？阅读 **[新手指南](docs/guide/getting-started.md)**。

## 核心功能

- **代码理解** — 读取、理解并编辑整个项目
- **完整开发工具** — 文件编辑、Shell 命令、Git、LSP、搜索
- **MCP 集成** — 连接外部工具和数据源
- **多 Agent** — 并行工作、团队协作、A2A 协议
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
| **[移动端](docs/guide/mobile.md)** | iOS (TestFlight) / Android (Google Play) |

## 文档

| 文档 | 说明 |
|------|------|
| [新手指南](docs/guide/getting-started.md) | 首次使用、API Key 配置、基本操作 |
| [安装指南](docs/guide/install.md) | 所有平台的安装方法 |
| [CLI 参考](docs/guide/cli-reference.md) | 命令、参数、管道模式 |
| [模型配置](docs/guide/providers.md) | LLM 厂商和端点配置 |
| [斜杠命令](docs/guide/slash-commands.md) | TUI 内命令参考 |
| [权限模式](docs/guide/modes.md) | 默认 / 旁路 / 只读 / 自动 |
| [MCP 服务器](docs/guide/mcp.md) | 通过 MCP 连接外部工具 |
| [IM 集成](docs/guide/im-integration.md) | QQ / Telegram / Discord / Slack / 飞书 / 钉钉 |
| [Harness 工作流](docs/guide/harness.md) | 隔离任务 + 审查 + 合并 |
| [A2A 协议](docs/guide/a2a.md) | 跨实例 Agent 委托 |
| [ACP / 编辑器](docs/guide/acp.md) | JetBrains、Zed 等 ACP 兼容编辑器 |
| [任务委托](docs/guide/delegation.md) | 委托任务给 Copilot、Claude、Cursor 等外部 Agent |
| [多 Agent 模式](docs/guide/multi-agent-modes.md) | 子 Agent、团队协作 |
| [配置参考](docs/guide/configuration.md) | 完整配置文件说明 |
| [项目记忆](docs/guide/project-memory.md) | GGCODE.md / AGENTS.md / CLAUDE.md |
| [技能系统](docs/guide/skills.md) | 可复用工作流模式 |
| [Shell 补全](docs/guide/shell-completion.md) | bash / zsh / fish / powershell |

## 快速链接

- **[下载桌面端](https://ggcode.dev)** · **[发布版本](https://github.com/topcheer/ggcode/releases)**
- **[报告问题](https://github.com/topcheer/ggcode/issues)** · **[功能建议](https://github.com/topcheer/ggcode/issues)**

## 许可证

MIT
