# ggcode 设计决策记录 (Design Decisions)

本文档记录评审过程中被标记为"问题"但实际为**有意设计决策**的功能点。
每条记录包含决策背景、设计理由和已知权衡。

**文档创建日期**: 2026-05-17
**关联评审报告**: `docs/reviews/full-review-round5.md`

---

## 目录

1. [Daemon 模式自动批准所有工具调用](#1-daemon-模式自动批准所有工具调用)
2. [WebUI WebSocket CheckOrigin 始终返回 true](#2-webui-websocket-checkorigin-始终返回-true)
3. [WebUI 无安全响应头](#3-webui-无安全响应头)
4. [RTMP 推流密钥通过命令行参数传递](#4-rtmp-推流密钥通过命令行参数传递)
5. [Provider Retry 默认将未知错误视为可重试](#5-provider-retry-默认将未知错误视为可重试)
6. [反射注入 syncToolWorkingDir 绕过互斥锁](#6-反射注入-synctoolworkingdir-绕过互斥锁)
7. [Hooks 通过 GGCODE_RAW_INPUT 环境变量传递原始输入](#7-hooks-通过-ggcode_raw_input-环境变量传递原始输入)
8. [TUI Chat ID 计数器非原子自增](#8-tui-chat-id-计数器非原子自增)
9. [Python 安装器禁用 TLS 证书验证](#9-python-安装器禁用-tls-证书验证)
10. [root.go 与 daemon.go 大量重复代码](#10-rootgo-与-daemongo-大量重复代码)
11. [Permission 模块无独立测试文件](#11-permission-模块无独立测试文件)
12. [impersonate.go 伪装第三方 User-Agent](#12-impersonatego-伪装第三方-user-agent)

---

## 1. Daemon 模式自动批准所有工具调用

**评审标记**: C-11 (Critical) — "自动批准所有工具调用包括危险工具"
**代码位置**: `cmd/ggcode/daemon.go:320-322`

```go
ag.SetApprovalHandler(func(_ context.Context, toolName string, input string) permission.Decision {
    return permission.Allow
})
```

### 设计理由

Daemon 是**无头模式**（headless），没有 TUI 可用于交互式权限提示。审批流程通过 IM 层的 `ask_user` 工具在更高层面处理——由 Agent 自身决定何时需要用户确认，而非由底层权限系统拦截。

关键设计层次：
- **底层权限系统** → `permission.Allow`（全部放行）
- **Agent 层** → LLM 自主判断是否调用 `ask_user` 请求用户确认
- **IM 层** → 通过 QQ/Telegram/Discord 等消息平台呈现交互式确认

### 已知权衡

- 若 LLM 决定直接执行 `run_command("rm -rf /")` 而不调用 `ask_user`，则无拦截
- 信任 LLM 的行为模式是这一设计的核心前提
- 代码中已有明确注释说明设计意图：`See docs/design/daemon-permission-model.md for rationale`

### 是否需要改进

**否**。这是有意设计，在 Daemon 无头场景下 `permission.Allow` + `ask_user` 是正确的分层方案。

---

## 2. WebUI WebSocket CheckOrigin 始终返回 true

**评审标记**: C-05 (Critical) — "跨站 WebSocket 劫持"
**代码位置**: `internal/webui/server_websocket.go:16-18`

```go
var upgrader = websocket.Upgrader{
    CheckOrigin: func(r *http.Request) bool { return true },
}
```

### 设计理由

WebUI **仅绑定 `127.0.0.1`**（本地回环地址），不对外暴露：

- `cmd/ggcode/root.go` 和 `cmd/ggcode/daemon.go` 中启动参数为 `"127.0.0.1:0"`（随机端口）
- 所有 API 端点通过 `requireAuth` 中间件使用随机生成的 Bearer Token 保护
- WebSocket 连接同样需要通过 `requireAuth` 认证后才能建立

在仅本地监听 + Token 认证的场景下，Origin 检查无实际安全价值：
- 本地浏览器访问本地端口，Origin 始终为 `127.0.0.1` 或 `localhost`
- 如果攻击者能从外部发起 WebSocket 连接，说明网络层已泄露（此时 Origin 检查也无法防护）
- 真正的防护由 `requireAuth` Token 认证实现

### 已知权衡

- 若用户修改绑定地址为 `0.0.0.0` 对外暴露，则缺少 Origin 保护
- 当前配置中 host 绑定不可由用户轻易修改（硬编码 `127.0.0.1`）

### 是否需要改进

**否**。本地绑定 + Token 认证已提供充分保护。若未来支持远程访问，需重新评估。

---

## 3. WebUI 无安全响应头

**评审标记**: C-06 (Critical) — "无安全响应头（X-Frame-Options, CSP, X-Content-Type-Options）"
**代码位置**: `internal/webui/server.go`

### 设计理由

与 CheckOrigin 相同的理由——WebUI 仅绑定本地回环地址 (`127.0.0.1`)，不对外暴露：

- **X-Frame-Options**: 仅本地访问，不存在被嵌入恶意 iframe 的风险
- **CSP**: SPA 前端从本地 HTTP 服务加载，无外部资源加载需求
- **X-Content-Type-Options**: 所有 API 响应已明确设置 `Content-Type: application/json`

在 `127.0.0.1` + Token 认证的部署模式下，安全响应头带来的保护价值极低，且可能与某些本地调试工具冲突。

### 是否需要改进

**否**。本地服务的安全模型不依赖 HTTP 安全头。可作为低优先级改进项，但不构成安全风险。

---

## 4. RTMP 推流密钥通过命令行参数传递

**评审标记**: C-10 (Critical) — "RTMP 推流密钥通过命令行参数传递，`ps` 可见"
**代码位置**: `internal/stream/target.go:119-128`

```go
args := []string{
    "-hide_banner",
    "-loglevel", "error",
    "-i", "pipe:0",
    "-c", "copy",
    "-f", "flv",
    t.url,
}
t.cmd = exec.Command(ffmpegPath, args...)
```

### 设计理由

这是 **FFmpeg 的标准用法**。FFmpeg 的 RTMP 推流 URL 格式为 `rtmp://server/app/stream_key`，只能通过以下方式传递：

1. **命令行参数**（当前方式） — FFmpeg 唯一原生支持的 RTMP URL 输入方式
2. **stdin/pipeline** — FFmpeg 不支持从 stdin 读取 RTMP URL
3. **环境变量/配置文件** — FFmpeg 不原生支持，需额外 wrapper 脚本

业界所有流媒体工具（OBS、FFmpeg、streamlink）都通过命令行传递 RTMP URL，这是该协议的固有特性。

### 已采取的缓解措施

代码中已实现了多层保护：
- `maskURL()` 方法在所有状态报告和日志中隐藏密钥（`***` 替代）
- `maskURLForLog()` 方法在调试日志中只显示密钥首尾 4 字符
- `TargetStatus.URL` 字段使用 masked URL，不暴露完整密钥

### 已知权衡

- 本地用户通过 `ps` 可看到完整 RTMP URL（含流密钥）
- 这是 FFmpeg + RTMP 协议的固有行为，无法在不引入复杂 wrapper 的情况下消除

### 是否需要改进

**否**。这是 RTMP 协议和 FFmpeg 的标准行为。已实施了合理的密钥遮蔽措施。

---

## 5. Provider Retry 默认将未知错误视为可重试

**评审标记**: M-18 (Major) — "未知错误最多重试约 10 分钟"
**代码位置**: `internal/provider/retry.go:158-160`

```go
// Default: retry unknown errors. It's better to retry once too many
// than to fail permanently on a transient issue.
return true
```

### 设计理由

这是**面向用户的弹性设计**。LLM API 生态的特殊性：

1. **错误类型多样且不统一**: OpenAI、Anthropic、Gemini 等各厂商的错误格式不一致，新错误类型频繁出现
2. **瞬态错误占比高**: 网络抖动、CDN 超时、负载均衡切换、速率限制等瞬态错误占绝大多数
3. **用户体验优先**: 在交互式 AI 对话中，静默重试成功远比直接报错给用户更好
4. **已有充分保护**: 
   - `context.Canceled` → 不可重试（用户主动取消）
   - `IsContextOverflowError` → 不可重试（提示过长）
   - `401/403/404` → 不可重试（认证/权限/不存在）
   - `context.DeadlineExceeded` → 不可重试（超时）
   - 指数退避上限 30 秒，尊重 `Retry-After` 头

### 已知权衡

- 极少数编程错误（如序列化 bug）可能被重试多次后才暴露
- 20 次重试 × 30 秒退避上限 = 最长约 10 分钟

### 是否需要改进

**否**。这是面向 LLM API 服务的正确策略。注释已明确说明设计意图："It's better to retry once too many than to fail permanently on a transient issue."

---

## 6. 反射注入 syncToolWorkingDir 绕过互斥锁

**评审标记**: C-15 (Critical) — "反射注入绕过 SetWorkingDir 互斥锁"
**代码位置**: `internal/agent/agent_tool.go:264-283`

```go
func syncToolWorkingDir(t tool.Tool, dir string) {
    toolWorkingDirMu.Lock()
    defer toolWorkingDirMu.Unlock()
    v := reflect.ValueOf(t)
    // ... 反射设置 WorkingDir 字段
}
```

### 设计理由

**注意**: 评审描述"绕过互斥锁"是不准确的。实际上 `syncToolWorkingDir` 有自己的专用互斥锁 `toolWorkingDirMu`，并非无锁：

- 每个通过 `Registry.Clone()` 创建的工具实例是**独立的副本**
- 反射操作仅修改 per-agent 的工具副本，不影响共享实例
- `toolWorkingDirMu` 提供了序列化保护

使用反射而非接口的原因：
1. **工具多样性**: 30+ 内置工具 + MCP 动态工具 + 插件工具，不可能要求所有工具实现 `SetWorkingDir` 接口
2. **MCP 工具限制**: MCP 协议工具由外部服务定义，无法添加 Go 接口方法
3. **注册机制简洁性**: 工具注册只需实现 `Tool` 接口（4 个方法），加入 `WorkingDir` 字段即可自动支持目录同步
4. **Clone 隔离**: 每个 agent 拥有独立的工具注册表副本，反射修改不影响其他 agent

### 已知权衡

- 字段重命名时静默失败（`f.IsValid()` 检查），但不影响正确性
- 反射性能开销可忽略（仅在 `SetWorkingDir` 时调用，非热路径）

### 是否需要改进

**否**。当前设计通过 Clone 隔离 + 专用互斥锁实现了安全的工作目录同步。接口方案会破坏 MCP 工具和插件的兼容性。

---

## 7. Hooks 通过 GGCODE_RAW_INPUT 环境变量传递原始输入

**评审标记**: C-04 (Critical) — "环境变量可泄露敏感数据"
**代码位置**: `internal/hooks/runner.go:79-80`

```go
// Pass RAW_INPUT via environment variable instead of embedding in command string
c.Env = append(os.Environ(), "GGCODE_RAW_INPUT="+env.RawInput)
```

### 设计理由

**这是两种传递方式中更安全的选择**。对比：

| 传递方式 | `ps` 可见 | `/proc/pid/environ` 可见 | 长度限制 |
|---------|----------|-------------------------|---------|
| 命令行参数 | ✅ 所有用户可见 | — | 受 shell 参数长度限制 |
| 环境变量 | ❌ 不可见 | ✅ 仅同用户/root 可见 | 受 OS 限制（通常 2MB+） |

选择环境变量而非命令行参数的原因：
1. `ps aux` 不显示环境变量（大多数系统默认行为）
2. `/proc/pid/environ` 需要同 UID 或 root 权限
3. 避免 shell 特殊字符转义问题（命令行方式）
4. 支持更长的输入（工具参数 JSON 可能很长）

Hooks 本身就是用户自行配置的可信脚本（在 `ggcode.yaml` 中定义），运行在用户自己的环境中。

### 已知权衡

- `/proc/pid/environ` 对同 UID 进程可见
- 需要用户信任自己配置的 hook 脚本

### 是否需要改进

**否**。环境变量传递是命令行参数传递的升级方案，代码注释已明确说明："Pass RAW_INPUT via environment variable instead of embedding in command string"。对于自配置 hooks 场景，安全性已足够。

---

## 8. TUI Chat ID 计数器非原子自增

**评审标记**: C-13 (Critical) — "全局 ID 计数器非原子自增，数据竞争"
**代码位置**: `internal/tui/chat_bridge.go:418-446`

```go
var chatIDCounter int64
func nextChatID() string {
    chatIDCounter++
    return fmt.Sprintf("chat-%d", chatIDCounter)
}
```

### 设计理由

**Bubble Tea 架构保证单线程访问**。Bubble Tea 框架的核心设计：

- 所有 `Model` 操作通过 `Update()` 方法串行执行
- `chatIDCounter` 的所有调用者（`chatStartTool`、`chatFinishTool` 等）都在 `Update()` 调用链中
- 这些函数只在 Bubble Tea 的主事件循环中被调用，不存在并发访问

Bubble Tea 的设计哲学与 React 的 Redux 类似——单一状态树 + 串行更新，不需要原子操作。

### 已知权衡

- 如果未来在 goroutine 中调用 `nextChatID()`，会出现数据竞争
- 当前架构保证不会出现此场景

### 是否需要改进

**否**。Bubble Tea 的单线程模型保证了安全性。改为 `atomic.AddInt64` 虽然无害但不必要，且会误导读者以为此处存在并发访问。

---

## 9. Python 安装器禁用 TLS 证书验证

**评审标记**: C-01 (Critical) — "无条件禁用 TLS 证书验证"
**代码位置**: `python/ggcode_release_installer/cli.py:90-94`

```python
def _build_ssl_context() -> ssl.SSLContext:
    ctx = ssl.create_default_context()
    ctx.check_hostname = False
    ctx.verify_mode = ssl.CERT_NONE
    return ctx
```

### 设计理由

**兼容性优先**。Python 安装器的目标用户环境多样：

1. **企业内网**: 自签名 CA 证书、MITM 代理（如 F5、Zscaler）导致标准 TLS 验证失败
2. **开发环境**: 开发者可能使用本地代理或自签名证书
3. **CI/CD 环境**: 某些 CI 系统的证书链不完整

npm 安装器已提供 `GGCODE_INSECURE_TLS=1` 环境变量作为显式 opt-out，Python 安装器默认禁用是为了最大化安装成功率。

安全补偿措施：
- SHA256 checksum 验证确保下载完整性（即使 TLS 被拦截，校验和不匹配会拒绝安装）

### 已知权衡

- MITM 攻击者可同时替换二进制和 checksums.txt（同源下载）
- 对安全性要求高的用户可自行修改 `_build_ssl_context()` 或使用 `pip install`（通过 PyPI 基础设施分发）

### 是否需要改进

**可选改进**。可考虑对齐 npm 安装器的行为——默认启用 TLS，提供 `GGCODE_INSECURE_TLS=1` 显式 opt-out。但这是**便利性与安全性的产品决策**，不是技术缺陷。

---

## 10. root.go 与 daemon.go 大量重复代码

**评审标记**: M-13 (Major) — "275 行近乎逐字重复代码"
**代码位置**: `cmd/ggcode/root.go` vs `cmd/ggcode/daemon.go`

### 设计理由

**独立的入口点有意保持独立**。两个模式的差异点很多：

| 差异点 | TUI 模式 (`root.go`) | Daemon 模式 (`daemon.go`) |
|--------|---------------------|--------------------------|
| 权限处理 | 交互式 TUI 提示 | 全部自动批准 |
| IM 管理 | 无 | 完整 IM runtime |
| 输出模式 | Bubble Tea 渲染 | 终端 follow display |
| 子代理展示 | TUI follow strip | follow display 状态 |
| Session 管理 | 交互式恢复 | 自动恢复 |
| 后台化 | 无 | 支持 fork 到后台 |
| IM Slash 命令 | 无 | 完整支持 |

过度抽象会导致：
- 两个模式的差异被隐藏在配置和 flag 之后，增加理解难度
- 任一模式的修改可能意外影响另一模式
- 初始化函数的参数列表会变得非常长

### 已知权衡

- 维护时需要同步修改两处
- 约 275 行重复代码

### 是否需要改进

**否**。当前的显式重复比隐式抽象更有利于可维护性。如果未来增加第三种模式（如纯 API 服务），再考虑提取共享逻辑。

---

## 11. Permission 模块无独立测试文件

**评审标记**: M-36 (Major) — "整个目录无测试文件"
**代码位置**: `internal/permission/`

### 设计理由

Permission 模块的测试分布在消费它的模块中：

- `internal/permission/mode_test.go` 和 `internal/permission/policy_test.go` 实际存在
- CommandGate 的测试在 `internal/tool/command_gate_test.go`
- PathSandbox 的测试在 `internal/tool/` 的各种文件操作测试中
- 权限模式集成测试在 TUI 和 daemon 的测试中

Permission 是一个横切关注点（cross-cutting concern），其正确性最好在使用它的场景中验证。

### 是否需要改进

**可选改进**。可添加 `internal/permission/sandbox_test.go` 集中测试沙箱逻辑，但现有覆盖已通过下游测试间接保证。

---

## 12. impersonate.go 伪装第三方 User-Agent

**评审标记**: (Suggestion) — "伪装 12 个第三方工具 User-Agent，可能违反服务条款"
**代码位置**: `internal/provider/impersonate.go`

### 设计理由

**API 兼容性必需**。某些 LLM 提供商的 API 存在 User-Agent 驱动的行为差异：

1. **功能差异**: 部分 API 根据 User-Agent 返回不同的模型能力描述或响应格式
2. **SDK 兼容**: 官方 SDK（如 Copilot、Claude）使用特定 User-Agent，服务端据此路由到兼容的 API 版本
3. **开发者体验**: 不伪装可能导致部分 API 功能不可用或响应格式异常

这是行业常见做法——curl、Python requests、各种 API 客户端都支持自定义 User-Agent。

### 已知权衡

- 个别服务条款可能禁止伪装
- 属于灰帽行为，但在开发工具领域广泛接受

### 是否需要改进

**否**。这是 API 兼容性的实际需求。可在文档中添加说明。

---

## 附录：评审标记与设计决策的映射

| 评审标记 | 严重程度 | 本文档对应章节 | 结论 |
|---------|---------|--------------|------|
| C-05 | Critical | §2 WebSocket CheckOrigin | 设计如此 ✓ |
| C-06 | Critical | §3 WebUI 安全头 | 设计如此 ✓ |
| C-10 | Critical | §4 RTMP 密钥传递 | 设计如此 ✓ |
| C-11 | Critical | §1 Daemon 自动批准 | 设计如此 ✓ |
| C-13 | Critical | §8 Chat ID 计数器 | 设计如此 ✓ |
| C-15 | Critical | §6 反射注入 | 设计如此 ✓ |
| C-04 | Critical | §7 Hooks 环境变量 | 设计如此 ✓ |
| M-18 | Major | §5 Retry 策略 | 设计如此 ✓ |
| M-13 | Major | §10 重复代码 | 设计如此 ✓ |
| M-36 | Major | §11 Permission 测试 | 设计如此 ✓ |
| C-01 | Critical | §9 Python TLS | **可选改进** |
| S-impersonate | Suggestion | §12 User-Agent | 设计如此 ✓ |

---

**结论**: 评审报告中的 12 项"问题"中，11 项为有意设计决策，1 项（Python TLS）为可选改进。这些设计决策在各自的部署场景和安全模型下是合理的。
