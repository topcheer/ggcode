# ggcode

<p align="center">
  <img src="ggcode_cli_banner_1775456774280.png" alt="ggcode" width="600" />
</p>

**[English](README.md)** | **中文**

**ggcode** 是一个终端 AI 编程助手。它能理解代码仓库、编辑文件、执行命令、管理检查点、使用一流的 LSP / MCP / skill 工作流，并在精致的 TUI 界面中持续工作，而不需要在脚本和浏览器标签之间来回切换。

如果你想要一个像产品而非 demo 的终端编程工作流，ggcode 就是为此而生的。

## 为什么选择 ggcode

- **留在终端** — 聊天、检查代码、编辑文件、查看 diff、管理会话，全在一个地方
- **支持真实编码计划和端点** — 兼容 OpenAI、Anthropic、Gemini、GitHub Copilot，以及多个编程向厂商预设
- **关键时刻保持控制** — supervised、plan、auto、bypass 和 autopilot 五种模式，让你自由选择 agent 的自主程度
- **提问不打断流程** — 当 agent 真正被卡住时，TUI 会弹出结构化的多问题 ask_user 问卷
- **快速恢复** — 用 checkpoint 撤销文件修改，而不是手动修复错误编辑
- **按需扩展** — 使用一流的 LSP、MCP 工具、skills、插件、记忆、后台命令和子 agent
- **日常使用友好** — 双语界面、可恢复会话、agent 忙碌时排队输入、本地 shell 模式、多平台安装方式

## 安装

### Homebrew（macOS / Linux）

```bash
brew tap topcheer/ggcode
brew install ggcode
```

### Go 安装器

```bash
go install github.com/topcheer/ggcode/cmd/ggcode-installer@latest
ggcode-installer
```

安装器会将对应的 GitHub Release 二进制文件下载到 `GOBIN`、`GOPATH/bin` 或 `~/go/bin`。

### npm

```bash
npm install -g @ggcode-cli/ggcode
```

npm 包装器默认下载最新的 ggcode GitHub Release。如果需要指定版本，设置 `GGCODE_INSTALL_VERSION`。

### pip

```bash
pip install ggcode
```

Python 包装器同样默认下载最新的 ggcode GitHub Release，也支持 `GGCODE_INSTALL_VERSION` 指定版本。

### Release 压缩包和安装包

每个 tagged release 都会发布桌面压缩包和原生安装包：

| 平台 | Release 文件 | 安装示例 |
| --- | --- | --- |
| macOS | `.pkg` | `sudo installer -pkg ./ggcode_<version>_darwin_universal.pkg -target /` |
| Windows | `.msi` | `msiexec /i .\ggcode_<version>_windows_x64.msi` |
| Debian / Ubuntu | `.deb` | `sudo dpkg -i ./ggcode_<version>_linux_<arch>.deb` |
| Fedora / RHEL / openSUSE | `.rpm` | `sudo rpm -i ./ggcode-<version>-1.<arch>.rpm` |
| Alpine | `.apk` | `sudo apk add --allow-untrusted ./ggcode-<version>-r1.<arch>.apk` |
| OpenWrt / opkg | `.ipk` | `opkg install ./ggcode_<version>_<arch>.ipk` |
| Arch Linux | `.pkg.tar.zst` | `sudo pacman -U ./ggcode-<version>-1-<arch>.pkg.tar.zst` |

桌面版 release 还包含压缩包文件（Unix 平台为 `.tar.gz`，Windows 为 `.zip`），适合手动解压。

如果你不想使用包管理器安装，也可以使用 release 压缩包、Go 安装器、npm 包装器或 Python 包装器。

### 从源码构建

```bash
git clone https://github.com/topcheer/ggcode.git
cd ggcode
go build -o ggcode ./cmd/ggcode
./ggcode
```

### 可选：安装本地 CI pre-commit hook

如果你希望本地提交时也执行 CI 级别的 Go 检查：

```bash
make install-git-hooks
```

hook 会执行以下操作：

- 用 `gofmt` 自动格式化已暂存的 Go 文件
- 运行 `go mod download`
- 运行 `go build -o /tmp/ggcode ./cmd/ggcode`
- 运行 `go vet ./...`
- 运行 `go test -tags=!integration ./...`

也可以手动运行相同的检查链：

```bash
make verify-ci
```

### 平台说明

- **macOS / Linux** 命令执行使用 `sh`
- **Windows** 命令执行优先使用 **Git Bash**，回退到 **PowerShell**
- Shell 补全支持 **bash**、**zsh**、**fish** 和 **PowerShell**

## 快速开始

### 1. 配置模型端点

最简单的方式是设置厂商 API Key：

```bash
export ZAI_API_KEY="your-key"
# 或 OPENAI_API_KEY / ANTHROPIC_API_KEY / GEMINI_API_KEY / OPENROUTER_API_KEY / DASHSCOPE_API_KEY / ...
```

如果你想使用 GitHub Copilot，也可以通过应用内的 **`/provider`** 流程登录，无需导出 API Key。

阿里云百炼 Coding Plan 可以使用内置的 **`aliyun`** 厂商：

- `coding-openai` → `https://coding.dashscope.aliyuncs.com/v1`
- `coding-anthropic` → `https://coding.dashscope.aliyuncs.com/apps/anthropic`

内置的 OpenAI 兼容路由预设还包括：

- `aihubmix` → `https://aihubmix.com/v1`（`AIHUBMIX_API_KEY`）
- `getgoapi` → `https://api.getgoapi.com/v1`（`GETGOAPI_API_KEY`）
- `novita` → `https://api.novita.ai/openai/v1`（`NOVITA_API_KEY`）
- `poe` → `https://api.poe.com/v1`（`POE_API_KEY`）
- `requesty` → `https://router.requesty.ai/v1`（`REQUESTY_API_KEY`）
- `vercel` → `https://ai-gateway.vercel.sh/v1`（`VERCEL_AI_GATEWAY_API_KEY`）

如果你使用 **Anthropic 兼容端点**，ggcode 也可以在首次启动时自动配置：

```bash
export ANTHROPIC_BASE_URL="https://your-endpoint"
export ANTHROPIC_AUTH_TOKEN="your-token"
```

### 2. 启动 ggcode

```bash
ggcode
```

首次启动时，ggcode 会让你选择界面语言。

### 3. 从真实任务开始

示例：

```text
解释这个项目的结构
将 auth 中间件重构为使用 JWT
为 session store 添加测试
排查 TUI 启动缓慢的原因
```

### 4. 使用内置工作流功能

- **`Ctrl+C`** 取消当前运行
- **`Ctrl+F`** 打开全屏项目文件浏览器，支持树形导航、文件名过滤和实时预览
- 如果 agent 正忙，你可以继续输入 — 新提示会**排队等待**
- 在空输入框中输入 **`$`** 或 **`!`** 进入**本地 shell 模式**；按 **`Esc`** 退出
- **`/undo`** 撤销最后一次文件编辑
- **`/provider`** 切换厂商 / 端点 / 模型
- **`/mode`** 切换 agent 自主程度
- **`/status`** 查看当前运行状态，可在应用内安装缺失的 LSP 服务器
- **`/mcp`** 查看已连接的 MCP 服务器及其工具
- **`/im`** 打开统一 IM 面板 — 管理所有频道绑定、启用/禁用适配器、查看状态
- **`/qq`**、**`/telegram`**、**`/discord`**、**`/slack`**、**`/feishu`**、**`/dingtalk`** 打开对应平台的专属面板进行配对和绑定
- **`/harness`** 运行仓库 harness 工作流，如脚手架、检查和清理
- 当 agent 确实无法继续时，它会打开一个**分页 ask_user 问卷**，并从你的批量回答中恢复

## ggcode 能做什么

从产品角度来看，ggcode 不只是"和模型聊天"：

- **代码理解** — 读取文件、搜索仓库、检查 git 状态和 diff
- **快速项目浏览** — 打开全屏文件浏览器，展开文件夹、按文件名过滤、预览 Markdown / 源码文件
- **语义代码导航** — 使用一流的本地 LSP 服务器（如 `gopls`、`rust-analyzer`、`clangd`、`sourcekit-lsp`、`lua-language-server`、Python 服务器，以及 YAML / JSON / Dockerfile / shell 文件的配置语言服务器）进行 hover、定义跳转、引用查找、符号搜索、工作区符号、诊断、代码操作、重命名，并可从 `/status` 在应用内安装
- **代码修改** — 创建文件、编辑指定区域、通过 checkpoint 撤销编辑
- **命令执行** — 运行前台命令或长时间运行的后台任务
- **并行协助** — 生成子 agent、检查进度、轮询长时间运行的 worker，不会阻塞主循环
- **记忆和上下文** — 加载项目记忆文件如 `GGCODE.md`、`AGENTS.md`、`CLAUDE.md`、`COPILOT.md`
- **可扩展性** — 连接 MCP 服务器、自定义插件和 skills，与内置 LSP 工作流协同
- **远程 IM 交互面** — 将当前工作区绑定到 IM 频道（QQ、Telegram、Discord、Slack、钉钉、飞书、PC），远程聊天可以镜像状态并接收 agent 输出的实时流；来源频道的用户回显会被抑制以避免重复消息；详见下方 [IM 集成](#im-集成)
- **会话连续性** — 保存、恢复、导出和压缩对话；自动压缩使用智能截断和 LLM 摘要，配合 session checkpoint 实现快速恢复
- **Harness 工作流** — 脚手架仓库指导、执行不变量、跟踪运行、垃圾回收过期任务状态

## 模式：agent 有多大自由度

| 模式 | 适合场景 | 含义 |
| --- | --- | --- |
| `supervised` | 大多数用户 | 工具未明确允许或拒绝时询问 |
| `plan` | 安全探索 | 只读式调查；阻止写入和命令执行 |
| `auto` | 日常快速工作 | 较安全的操作自动执行，危险操作保持谨慎 |
| `bypass` | 高信任工作流 | 几乎允许一切，仅在关键操作时暂停 |
| `autopilot` | 高级用户 | 类似 bypass，但更倾向安全猜测，只在真正被卡住时通过 `ask_user` 最后求助 |

## 常用斜杠命令

### 核心工作流

| 命令 | 功能 |
| --- | --- |
| `/help` 或 `/?` | 显示应用内帮助 |
| `/provider [vendor]` | 打开 provider 管理器，切换厂商 / 端点 / 模型，包括 GitHub Copilot 登录/登出流程 |
| `/model <name>` | 直接切换模型 |
| `/mode <mode>` | 切换权限模式 |
| `/status` | 查看当前状态，包括本地 LSP 可用性、安装提示和应用内安装 |
| `/config` | 查看或更新配置 |
| `/update` | 下载最新 release 并重启到更新后的二进制文件 |
| `/lang <en|zh-CN>` | 切换界面语言 |

### 会话和恢复

| 命令 | 功能 |
| --- | --- |
| `/sessions` | 列出已保存的会话 |
| `/resume <id>` | 恢复之前的会话 |
| `/export <id>` | 将会话导出为 Markdown |
| `/clear` | 清空当前对话 |
| `/compact` | 压缩对话历史 |
| `/undo` | 撤销最后一次文件编辑 |
| `/checkpoints` | 列出可用的编辑检查点 |

对于直接下载 GitHub release 不稳定的环境，可以设置 `GGCODE_UPDATE_BASE_URLS` 为逗号分隔的 release 源列表。每项可以是普通的 release 基础 URL 或完整的 URL 代理前缀。

### 扩展能力

| 命令 | 功能 |
| --- | --- |
| `/mcp` | 查看 MCP 服务器和 MCP 工具 |
| `/plugins` | 列出已加载的插件 |
| `/skills` | 浏览 agent 可用的 skills |
| `/memory` | 查看已存储的记忆 |
| `/agents` | 列出活跃的子 agent |
| `/agent <id>` | 查看子 agent 详情 |
| `/im` | 打开统一 IM 面板 — 管理所有频道、启用/禁用绑定、查看适配器状态 |
| `/qq` | 打开 QQ 专属面板进行配对和绑定 |
| `/telegram` 或 `/tg` | 打开 Telegram 面板 |
| `/discord` | 打开 Discord 面板 |
| `/slack` | 打开 Slack 面板 |
| `/feishu` 或 `/lark` | 打开飞书面板 |
| `/dingtalk` 或 `/ding` | 打开钉钉面板 |
| `/todo` | 查看或管理 todo 状态 |
| `/image` | 附加图片 |
| `/bug` | 报告 bug |
| `/init` | 为当前项目生成 `GGCODE.md` |
| `/harness` | 打开 harness 面板，支持 `init/check/monitor/queue/tasks/run/run-queued/review/promote/release/gc/doctor`；`queue` 和 `run` 使用当前输入草稿，直接输入 `/harness ...` 命令同样有效 |
| `/exit`、`/quit` | 退出 ggcode |

## 非交互和脚本化使用

ggcode 也支持简单的管道模式，当你不想打开 TUI 时：

```bash
ggcode \
  --prompt "总结这个仓库的改动" \
  --allowedTools read_file \
  --output summary.md
```

对于 harness 工程化风格的仓库，ggcode 还提供了追踪控制面：

```bash
ggcode harness init --goal "构建一个 ERP 系统"
ggcode harness check
ggcode harness queue "实现采购工作流"
ggcode harness queue --context internal-inventory "实现库存工作流"
ggcode harness queue --depends-on <purchasing-task-id> "实现库存工作流"
ggcode harness tasks
ggcode harness monitor
ggcode harness monitor --watch --interval 2s
ggcode harness contexts
ggcode harness inbox
ggcode harness inbox promote --owner inventory-team
ggcode harness inbox retry --owner unowned
ggcode harness run --all-queued
ggcode harness run --all-queued --retry-failed
ggcode harness run --resume-interrupted --retry-failed
ggcode harness review
ggcode harness review approve <task-id>
ggcode harness review reject <task-id> --note "需要边界清理"
ggcode harness promote
ggcode harness promote apply <task-id>
ggcode harness promote apply --all-approved
ggcode harness release
ggcode harness release --owner inventory-team
ggcode harness release --context internal/inventory
ggcode harness release --environment staging
ggcode harness release --group-by owner
ggcode harness release apply --note "staging wave"
ggcode harness release apply --group-by context --batch-id release-erp
ggcode harness release rollouts --environment prod
ggcode harness release rollouts
ggcode harness release advance rollout-erp
ggcode harness release approve rollout-erp --wave 2 --note "变更委员会已批准"
ggcode harness release reject rollout-erp --wave 2 --note "等待策略审查"
ggcode harness release pause rollout-erp --note "等待签字"
ggcode harness release resume rollout-erp --note "已收到签字"
ggcode harness release abort rollout-erp --note "冻结窗口"
ggcode harness doctor
ggcode harness gc
```

### Harness 工作流：从脚手架到发布

Harness 是 ggcode 中的仓库级控制面。它设计用于那些对单次聊天来说太大或状态太复杂、但仍想复用相同 agent、工具和审批模型的工作。

1. **初始化项目平面**
   - `ggcode harness init` 在目录为空或非 git 时自动引导 git
   - 创建 `.ggcode/harness.yaml`、根指导文件和监控状态文件 `.ggcode/harness/events.jsonl` 及 `.ggcode/harness/snapshot.db`
   - 在较大的仓库中，它还会检测明显的限界上下文（如 `cmd/` 和 `internal/*`），然后写入嵌套的 `AGENTS.md` 文件，使局部指导靠近它管理的代码

2. **排队和执行追踪工作**
   - `ggcode harness queue` 创建持久化的待办项而非立即启动一次性运行
   - 任务可以用 `--context` 限定范围，用 `--depends-on` 设置依赖门控，下游工作在前置条件完成前保持 `blocked` 状态
   - 排队执行**默认由 worker 支持**，通常在 `.ggcode/harness/worktrees/<task-id>` 下的隔离工作树中运行，保持仓库根目录在长时间工作中更整洁
   - 如果仓库根目录有真实的未提交或未跟踪项目文件，`harness run` 现在会询问是否先在主工作区创建一个 **checkpoint 提交**；拒绝则取消运行而非静默从过期基础分叉
   - `run.max_attempts`（在 `.ggcode/harness.yaml` 中）、`ggcode harness run --retry-failed` 和 `ggcode harness run --resume-interrupted` 覆盖有界重试和中断会话恢复

3. **检查和路由操作状态**
   - `ggcode harness monitor` 显示实时任务/release/rollout 快照，支持 `--watch`
   - `ggcode harness contexts` 按限界上下文汇总待办状态，包括验证失败和发布就绪性
   - 上下文可以携带轻量级 owner 元数据，`ggcode harness inbox` 将其转换为按 owner 分组的可操作桶（review 就绪、promotion 就绪和可重试的工作）
   - `ggcode harness doctor` 和 `ggcode harness gc` 处理熵信号，如过期的 blocked 任务、孤立的工作树和 worker/任务漂移

4. **将验证通过的工作推进到发布**
   - 完成的任务持久化交付证据（如变更文件和交付报告），然后进入**审查循环**
   - `ggcode harness review` 批准或拒绝已验证的任务；被拒绝的项带着备注重新进入重试路径
   - 批准的工作通过 `ggcode harness promote` 进入**晋升循环**，应用时也可将 git-worktree 任务分支合并回仓库根目录
   - 晋升的工作通过 `ggcode harness release` 和 `ggcode harness release apply` 进入**发布循环**，支持可选的 `--owner`、`--context`、`--environment` 和 `--group-by owner|context`
   - 分组发布变成由 `harness release rollouts`、`approve`、`reject`、`advance`、`pause`、`resume` 和 `abort` 管理的阶段性 rollout 波次

Rollout 和门控状态在 release 命令之外也可见：`harness doctor`、`harness contexts` 和 `harness inbox` 都会展示 rollout 活动和门控计数（`pending`、`approved`、`rejected`），操作者无需在狭窄的子命令之间跳转即可监控进度。

有用的标志：

- `--prompt` / `-p` — 运行非交互式提示
- `--allowedTools` — 限制管道模式下允许的工具
- `--output` — 将结果写入文件而非 stdout
- `--bypass` — 以 bypass 模式启动
- `--resume <id>` — 立即恢复之前的会话
- `--config <path>` — 使用指定的配置文件

## IM 集成

ggcode 可以连接到聊天平台，让你从手机、另一台机器或共享团队频道与 agent 交互。支持的平台：

| 平台 | 命令 | 传输方式 |
| --- | --- | --- |
| QQ | `/qq` | WebSocket（QQ 机器人网关） |
| Telegram | `/telegram` 或 `/tg` | 长轮询或 webhook |
| Discord | `/discord` | Discord Gateway |
| 飞书 / Lark | `/feishu` 或 `/lark` | 飞书 WebSocket |
| 钉钉 | `/dingtalk` 或 `/ding` | 钉钉 Stream 模式 |
| Slack | `/slack` | Socket Mode |
| PC | `/pc` | PC 中继 |

### 快速设置

1. **配置适配器** — 在 `ggcode.yaml` 的 `im.adapters` 中配置（完整示例见 `ggcode.example.yaml`）：

```yaml
im:
  enabled: true
  adapters:
    telegram:
      enabled: true
      platform: telegram
      extra:
        bot_token: ${TELEGRAM_BOT_TOKEN}
```

2. **启动 ggcode** — 适配器在启动时自动连接。

3. **绑定频道** — 在 TUI 中使用 `/im` 查看所有适配器并绑定/解绑频道，或使用平台专属命令（如 `/telegram`）配对。

### 功能特性

- **实时流式传输** — agent 输出实时推送到 IM 频道
- **远程命令** — 从聊天中发送 `/provider`、`/model`、`/mode` 等命令
- **Ask-user 转发** — 当 agent 需要确认时，问题会出现在聊天中，你的回复会反馈给 agent
- **回显抑制** — 你从来源频道发送的消息不会被回显，避免重复噪音
- **按频道绑定** — 将多个频道绑定到同一工作区；每个频道独立接收 agent 输出
- **语音消息** — 可选的 STT 集成将语音消息转录为文本提示（配置 `im.stt`）
- **启用/禁用频道** — 使用 `/im` 临时禁用绑定而不删除
- **适配器管理** — 在 daemon 模式下，使用 `/listim`、`/muteim`、`/muteall`、`/muteself`、`/restart` 从 IM 中管理适配器

### 统一 IM 面板（`/im`）

在 TUI 中输入 `/im` 打开统一 IM 面板。你可以：

- 查看所有平台上活跃和已禁用的频道绑定
- **禁用**频道（按 `d`）— 暂停该频道的 agent 输出
- **启用**之前禁用的频道（按 `e`）
- 使用 `j`/`k` 或方向键导航；按 `Esc` 关闭

### Daemon 模式 IM 斜杠命令

在 daemon 模式（`ggcode daemon`）下，IM 频道可通过斜杠命令管理适配器：

| 命令 | 说明 |
|------|------|
| `/listim` | 列出所有 IM 适配器及其状态（在线/静音/活跃） |
| `/muteim <名称>` | 静音指定适配器（不能静音自己 — 用 `/muteself`） |
| `/muteall` | 静音所有适配器（你正在使用的除外） |
| `/muteself` | 静音当前适配器（停止所有回复；从其他适配器用 `/restart` 恢复） |
| `/restart` | 重启 daemon（解除所有静音 — 静音状态不持久化） |
| `/help` | 显示可用命令 |

## A2A（Agent-to-Agent）

多个 ggcode 实例可以通过 A2A 协议互相发现、认证并跨实例调用工具。默认启用，端口自动分配。

### 认证方式

五种认证方案可单独或组合启用：

| 方案 | 配置键 | 适用场景 |
|------|--------|---------|
| **API Key** | `a2a.auth.api_key` | 开发环境、可信网络 |
| **OAuth2 + PKCE** | `a2a.auth.oauth2` | 需人工触发的 agent |
| **Device Flow** | `a2a.auth.oauth2` + `flow: "device"` | 无头服务器、SSH |
| **OpenID Connect** | `a2a.auth.oidc` | 企业 SSO |
| **双向 TLS** | `a2a.auth.mtls` | 机器对机器、零信任网络 |

```yaml
# 最简：共享 API Key
a2a:
  auth:
    api_key: "my-secret-key"

# GitHub 零配置
a2a:
  auth:
    oauth2:
      provider: "github"
```

完整认证指南（所有方案、提供商预设、决策矩阵）见 [`docs/a2a-auth.md`](docs/a2a-auth.md)。

## 配置

大多数用户只需要一个简短的配置文件：

```yaml
vendor: zai
endpoint: cn-coding-openai
model: glm-5-turbo
language: zh-CN
default_mode: supervised

allowed_dirs:
  - .

tool_permissions:
  read_file: allow
  search_files: allow
  run_command: ask
  write_file: ask
```

ggcode 内置了主流厂商和多个编程向端点的预设，包括阿里云百炼 Coding Plan，所以通常你只需选择厂商或设置 API Key，而不需要自己编写完整的 provider 目录。

近期内置的路由式预设包括 **AIHubMix**、**GetGoAPI**、**Novita AI**、**Poe**、**Requesty** 和 **Vercel AI Gateway**。在 TUI 的 `/provider` 面板中，ggcode 还会显示当前选中厂商/端点所需的 API Key 环境变量，无需猜测。

对于长时间运行或交互式 shell 工作，内置的异步命令工具让 agent 可以启动后台命令、轮询进度、发送后续 stdin 输入、停止任务，而不阻塞整个会话。

Skills 通过 `/skills` 浏览和查阅文档，但它们不是用户的斜杠命令接口。它们主要是 agent 可复用能力清单的一部分，由模型在适当时通过 `skill` 工具调用。

内置 skills 现在覆盖了多个高价值操作工作流，包括**调试**、**验证**、**ggcode 配置编辑**和**通过 MCP 的浏览器自动化指导**。

完整的配置参考、示例、厂商目录、hooks、MCP 服务器、插件和子 agent 设置，请参见：

- [`ggcode.example.yaml`](ggcode.example.yaml)

## MCP、插件、hooks 和记忆

### MCP 服务器

当你希望 ggcode 访问外部工具生态时使用 MCP。

```yaml
mcp_servers:
  - name: filesystem
    command: npx
    args:
      - -y
      - "@anthropic/mcp-filesystem"
      - /path/to/allowed/dir
```

ggcode 自动发现 MCP 工具并在 agent 循环中使其可用。

#### MCP OAuth 2.1

需要认证的 HTTP 类型 MCP 服务器通过标准 MCP OAuth 2.1 流程支持。当 MCP 服务器返回 `401 Unauthorized` 时，ggcode 自动：

1. 发现 OAuth 授权服务器元数据
2. 通过动态客户端注册（DCR）注册客户端（或使用内置/配置的 client ID）
3. 打开浏览器进行设备流授权
4. 在 `~/.ggcode/provider_auth.json` 中存储和刷新访问令牌

对于不支持动态客户端注册的服务器，可以配置 client ID：

```yaml
mcp_servers:
  - name: github
    type: http
    url: https://api.githubcopilot.com/mcp/
    oauth_client_id: "Iv1.xxxxxxxxxxxx"
```

常见服务器（如 GitHub MCP）已内置 client ID。

如果你需要浏览器自动化，先连接一个浏览器相关的 MCP 服务器。连接后，其工具会出现在 `/mcp` 中，任何基于提示的浏览器工作流也会出现在 `/skills` 中。ggcode **不会**在没有配置浏览器 MCP 服务器的情况下假装有内置浏览器控制能力。

内置的快速启动路径是 `/mcp` 面板（`b`）中的 Playwright 预设：

```yaml
mcp_servers:
  - name: playwright
    type: stdio
    command: npx
    args:
      - -y
      - "@playwright/mcp"
```

### 插件和 Skills

- **插件** 从配置中添加自定义工具
- **Skills** 添加更高级别的功能和工作流
- **`/skills`** 是查看当前可用技能的最简单入口，包括 MCP 服务器连接后基于提示的技能
- 内置 skills 包括操作辅助工具，如 `debug`、`verify`、`update-config` 和 `browser-automation`

### 项目记忆

ggcode 可以从以下文件加载项目指导：

- `GGCODE.md`
- `AGENTS.md`
- `CLAUDE.md`
- `COPILOT.md`

使用这些文件告诉 ggcode 你的项目如何运作、遵循什么约定、避免什么。

## Shell 补全

```bash
# Bash
ggcode completion bash > /etc/bash_completion.d/ggcode

# Zsh
ggcode completion zsh > "${fpath[1]}/_ggcode"

# Fish
ggcode completion fish > ~/.config/fish/completions/ggcode.fish

# PowerShell
ggcode completion powershell | Out-String | Invoke-Expression
```

## 更多文档

- **想使用产品？** 从本 README 开始
- **想要完整配置参考？** 参见 [`ggcode.example.yaml`](ggcode.example.yaml)
- **想了解实现细节？** 参见 [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md)

## 许可证

MIT
