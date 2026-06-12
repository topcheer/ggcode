# ggcode 架构审查报告

**项目**: github.com/topcheer/ggcode — 终端 AI 编码助手
**审查员**: arch-reviewer (架构审查员)
**日期**: 2025-07-14
**代码规模**: ~101k LOC (非测试) + ~69k LOC (测试)，43 个 internal 子包

---

## 总体架构评分：7.8 / 10

> 项目展现了出色的工程广度和系统设计能力——agent loop、multi-provider 适配、IM 网关、A2A 协议、harness 工作流等子系统设计合理，接口抽象到位。主要问题集中在 TUI 包的规模失控（~42k LOC）、`Model` 结构体的"God Object"倾向，以及部分注册逻辑分散的问题。

---

## 一、整体架构评估

### 1.1 模块划分 (8/10)

```
cmd/ggcode/          → CLI 入口 (Cobra)
internal/agent/      → 核心 agent loop (~1.7k LOC)
internal/provider/   → LLM provider 适配器 (~2.8k LOC)
internal/tool/       → 内置工具集 (~10.4k LOC)
internal/tui/        → Bubble Tea TUI (~41.9k LOC) ⚠️
internal/im/         → IM 网关 (~22.3k LOC)
internal/harness/    → 工作流引擎 (~6.9k LOC)
internal/knight/     → 后台自主 agent (~7.0k LOC)
internal/a2a/        → A2A 协议 (~3.6k LOC)
internal/webui/      → WebUI HTTP/WS 服务 (~1.8k LOC)
internal/auth/       → 认证栈 (~1.8k LOC)
internal/mcp/        → MCP 客户端 (~2.5k LOC)
+ 30 个辅助包
```

**优点**：
- 核心域（agent, provider, tool）和基础设施（config, session, util）边界清晰
- `internal/` 完全封闭，API 稳定性可控
- 43 个子包的平均规模适中（~2.3k LOC/包），大多数包职责单一

**不足**：
- `tui/` 和 `im/` 是明显的"超级包"（分别为 42k 和 22k LOC）
- `tool/` 包含 52 个文件，有进一步内聚的必要

### 1.2 依赖关系 (8/10)

**分层结构**：
```
cmd/ggcode → internal/tui (顶层编排)
           → internal/agent → internal/provider (核心域)
                           → internal/tool
           → internal/im → internal/config (基础设施)
           → internal/harness → internal/agent
```

**优点**：
- 无循环依赖（通过 `internal/cost/types.go` 定义本地 `TokenUsage` 显式避免循环）
- `internal/context` 使用 `ctxpkg` 别名避免与标准库 `context` 冲突
- Provider 包零业务依赖（仅依赖 config 和外部 SDK）

**不足**：
- TUI `Model` 导入了 **19 个** internal 包，成为事实上的"集成点"，承担了过多编排职责

---

## 二、核心模块设计

### 2.1 Agent Loop (`internal/agent/`) (9/10)

**文件拆分合理**：
| 文件 | 职责 |
|------|------|
| `agent.go` | 核心结构、`RunStreamWithContent` 主循环 |
| `agent_autopilot.go` | 自动续行逻辑 |
| `agent_compact.go` | 自动 compaction |
| `agent_precompact.go` | 后台预 compaction |
| `agent_memory.go` | 项目内存注入 |
| `agent_tool.go` | 工具执行 + 权限检查 |

**设计亮点**：
- `RunStreamWithContent` 主循环 ~250 行，逻辑清晰：发送 → 解析流 → 执行工具 → 反馈结果 → 循环
- 通过回调函数（`ApprovalFunc`, `DiffConfirmFunc`, `runResultHandler`）实现与 UI 的解耦
- 并发安全：`sync.RWMutex` 保护所有可变状态，`shutdownCtx/shutdownCancel` 处理资源清理
- 空响应检测 + 连续 3 次空响应自动重置，防止 context overflow 卡死
- Autopilot 循环保护：`shouldTriggerAutopilotLoopGuard` 避免无限自动续行

**改进建议**：
- 🟢 `streamChatResponse` 中的 reasoning 解析逻辑可以独立为 `agent_reasoning.go`

### 2.2 Provider 适配器模式 (`internal/provider/`) (8.5/10)

**接口设计**：
```go
type Provider interface {
    Name() string
    Chat(ctx, messages, tools) (*ChatResponse, error)
    ChatStream(ctx, messages, tools) (<-chan StreamEvent, error)
    CountTokens(ctx, messages) (int, error)
}
```

**工厂模式** (`registry.go`)：
```go
func NewProvider(resolved *config.ResolvedEndpoint) (Provider, error) {
    switch resolved.Protocol {
    case "anthropic": return NewAnthropicProviderWithBaseURL(...)
    case "openai":    return NewOpenAIProviderWithBaseURL(...)
    case "copilot":   return NewCopilotProvider(...)
    case "gemini":    return NewGeminiProviderWithBaseURL(...)
    }
}
```

**设计亮点**：
- 统一的 `ContentBlock` union type 处理 text/image/tool_use/tool_result/reasoning
- `StreamEvent` 类型系统覆盖完整：Text, ToolCallChunk, ToolCallDone, ToolResult, Done, Error, Reasoning
- 自适应 `adaptiveCap` 按 (vendor, baseURL, model) 学习最大输出 token 限制
- 重试逻辑完善：区分可重试/不可重试错误（仅 401/403/404 为永久失败），支持 Retry-After header

**新增 provider 难度**：🟢 低 — 实现 `Provider` 接口的 4 个方法，在 `registry.go` 添加 switch case

### 2.3 TUI 架构 (`internal/tui/`) (6/10)

**规模统计**：
- **107 个 Go 文件**（含测试），**47+ 非测试文件**
- `Model` 结构体：**165 行字段定义**，~120 个字段
- 导入 **19 个** internal 包

**问题**：

🔴 **严重：`Model` 是 God Object**
- `model.go` 880 行，包含 120+ 字段
- 混合了 UI 状态（`width`, `height`, `loading`）、业务状态（`activeVendor`, `activeModel`）、子面板状态（17 个 `*PanelState`）、编排逻辑（`imManager`, `subAgentMgr`, `swarmMgr`）
- 每个 IM adapter 都有对应的 `xxxPanelState` + `xxx_panel.go` + `i18n_xxx.go`，导致每新增一个 IM adapter 需要修改 TUI 3 个文件

🟡 **中等：文件命名和职责边界模糊**
- `commands.go`, `commands_harness.go`, `commands_slash.go`, `commands_stream.go` 四个文件处理不同命令类型，但命名难以区分
- `model_approval.go`, `model_clipboard.go`, `model_messages.go`, `model_panel.go`, `model_pending.go`, `model_terminal.go`, `model_update.go` 这些 `model_*.go` 扩展文件表明 Model 本身过于庞大

🟡 **中等：i18n 文件碎片化**
- 存在 20+ 个 `i18n_*.go` 文件，每个 IM adapter + 功能模块各一个
- 建议：合并为 `i18n_im.go`（所有 IM 相关）+ 按功能域分组

**改进建议**：
1. 将 IM 面板状态提取到 `internal/tui/panels/` 子包
2. 将编排逻辑提取到 `internal/tui/controller.go` 或独立的 coordinator
3. 引入面板注册机制：IM adapter 注册自己的面板工厂，而非在 Model 中硬编码字段

### 2.4 Harness 工作流引擎 (`internal/harness/`) (8/10)

**规模**：30 个文件，~6.9k LOC

**设计亮点**：
- 完整的任务生命周期：`blocked → queued → running → completed/failed/rejected/abandoned`
- 依赖追踪 (`DependsOn []string`) + 自动依赖解析
- Git worktree 隔离执行
- LLM 分类器 (`llm_classifier.go`) 自动路由任务类型
- Review/Promote/Release 三阶段审批流
- JSON 文件存储，零外部依赖

**不足**：
- 🟡 `Task` 结构体（403 行文件）字段过多（30+ 字段），混合了执行状态、review 状态、promotion 状态、release 状态。建议拆分为 `Task` + `TaskReview` + `TaskPromotion` 嵌入结构

### 2.5 IM 网关架构 (`internal/im/`) (8.5/10)

**规模**：17 个 adapter + Manager，~22.3k LOC

**核心接口设计**：
```go
type Sink interface {
    Name() string
    Send(context.Context, ChannelBinding, OutboundEvent) error
}
// 可选接口：Closer, TypingIndicator, InteractiveSender, ShareLinkProvider
```

**设计亮点**：
- Manager（`runtime.go` 1880 行）是整个 IM 子系统的核心，统一管理绑定、配对、静音、禁用
- Fan-out 并行发送 + 超时 + 重试
- 消息去重（`seenMessages` map + 5 分钟 TTL + 每 100 条清理）
- 多实例检测（`InstanceDetect`）+ 自动静音非主实例
- 配对安全：4 位随机码 + 3 次拒绝后黑名单
- 每通道 echo 抑制（`EmitExcept`）

**新增 IM adapter 难度**：🟢 低
1. 实现 `Sink` 接口（`Name()` + `Send()`）
2. 在 `adapters.go` 添加启动逻辑
3. 在 TUI 添加对应 panel（⚠️ 这步较繁琐）

**不足**：
- 🔴 `runtime.go` 本身 1880 行，是最大的单文件。虽然逻辑内聚（都是 Manager 方法），但可按功能域拆分为 `runtime_binding.go`, `runtime_mute.go`, `runtime_emit.go`, `runtime_pairing.go`

### 2.6 A2A 协议实现 (`internal/a2a/`) (8.5/10)

**设计亮点**：
- 完整实现 A2A 协议：JSON-RPC over HTTP + SSE streaming
- 多认证方案：API Key + OAuth2 (PKCE/Device Flow) + OIDC + mTLS，可同时启用
- Agent Card 自动生成 + 安全声明自动重建 (`rebuildSecuritySchemes`)
- Push Notification 配置管理
- MCP Bridge 透明桥接 A2A 远程工具到本地 MCP 工具
- mDNS 局域网发现（可选）
- 5 实例 Mesh E2E 测试

**不足**：
- 🟢 SSE 流当前只发送 terminal 状态，中间进度事件未实现（注释中 `Streaming: true` 但实际未推送增量）

---

## 三、接口设计评估

### 3.1 Provider 接口 (9/10)

```go
type Provider interface {
    Name() string
    Chat(ctx, messages, tools) (*ChatResponse, error)
    ChatStream(ctx, messages, tools) (<-chan StreamEvent, error)
    CountTokens(ctx, messages) (int, error)
}
```

**评价**：
- ✅ 最小化接口，4 个方法刚好覆盖所有需求
- ✅ `ContentBlock` union type 设计优雅，支持 text/image/tool_use/tool_result/reasoning
- ✅ `StreamEvent` 类型系统完整
- 🟢 考虑添加 `SupportedModels() []string` 或 `Capabilities() ProviderCapabilities` 用于运行时发现

### 3.2 Tool 接口 (9/10)

```go
type Tool interface {
    Name() string
    Description() string
    Parameters() json.RawMessage
    Execute(ctx, input) (Result, error)
}
```

**评价**：
- ✅ 极简接口，扩展性极好
- ✅ `Result` 支持 `FollowUpMessages`（inline skill 注入指令）和 `SuggestedWorkingDir`（工作目录切换）
- ✅ `Registry` 线程安全，支持运行时动态注册/注销
- 🟢 注册逻辑分散：`builtin.go` + `cmd/ggcode/root.go` + `internal/tui/repl.go` 三个位置注册工具

### 3.3 ChatBridge 接口 (9/10)

```go
// webui/server.go
type ChatBridge interface {
    Messages() []provider.Message
    SendUserMessage(content []provider.ContentBlock)
    Subscribe(func(StreamEvent)) func()
}
```

**两种实现**：
- `DaemonBridge`：直接操作 agent + `pendingInterruptions`
- `TUIChatBridge`：通过 `program.Send()` 注入 bubbletea 事件循环

**评价**：
- ✅ 完美解耦 webui 与 agent/tui 实现
- ✅ TUI 模式下 webchat 消息走完整提交流程（idle → startAgent, busy → queuePendingSubmission），避免并发问题
- ✅ 订阅模式支持多 WebSocket 客户端同时接收事件

---

## 四、可维护性评估

### 4.1 TUI 包拆分 (🔴 严重)

**现状**：
- 107 个文件，42k LOC，Model 165 行字段
- 每新增一个 IM adapter 需修改 3+ 个 TUI 文件

**建议拆分方案**：
```
internal/tui/
  model.go           (核心 Model，仅保留顶层状态)
  panels/
    im/              (IM 相关面板：qq_panel.go, tg_panel.go...)
    harness/         (harness_panel.go)
    config/          (provider_panel.go, model_panel.go, mcp_panel.go)
  i18n/              (国际化目录)
  controller.go      (编排逻辑：agent 启停、IM 管理、工具注册)
```

### 4.2 错误处理一致性 (🟡 中等)

**统计**：
- `fmt.Errorf` + `%w`（错误包装）：800 处
- `fmt.Errorf` 无 `%w`（丢失上下文）：736 处
- `errors.Is/As` 使用：60 处

**问题**：约 48% 的 `fmt.Errorf` 调用未使用 `%w` 包装，导致错误链断裂，调用方无法用 `errors.Is/As` 匹配。

**建议**：对所有跨包返回的错误统一使用 `%w` 包装，并添加 golangci-lint 规则 `errorlint` 强制检查。

### 4.3 工具注册分散 (🟡 中等)

**现状**：
| 位置 | 注册的工具 |
|------|-----------|
| `internal/tool/builtin.go` | `ask_user`, `todo_write`, 文件/命令/git/web 工具 |
| `cmd/ggcode/root.go` | `save_memory`, `skill`, `config`, MCP 工具, cron 工具 |
| `internal/tui/repl.go` | `spawn_agent`, `wait_agent`, `list_agents` |

**问题**：新增工具需要知道去哪个文件注册，违反单一入口原则。

**建议**：统一到 `internal/tool/builtin.go` 或新增 `internal/tool/register.go`，通过依赖注入传入所需参数（如 `AutoMemory`, `MCPRuntime`）。

### 4.4 测试覆盖 (8/10)

**测试/代码比率**：
| 包 | 代码 LOC | 测试 LOC | 比率 |
|----|---------|---------|------|
| tui | 41,889 | 20,348 | 49% |
| im | 22,265 | 17,186 | 77% |
| tool | 10,359 | 7,323 | 71% |
| harness | 6,924 | 10,173 | 147% |
| knight | 7,029 | 4,328 | 62% |
| a2a | 3,633 | 5,705 | 157% |
| provider | 2,830 | 1,817 | 64% |
| webui | 1,786 | 3,043 | 170% |
| agent | 1,727 | 2,579 | 149% |

**评价**：测试覆盖整体优秀，harness/a2a/webui/agent 测试 LOC 超过代码 LOC。TUI 的 49% 是合理的（UI 层难以完全单元测试）。

---

## 五、扩展性评估

### 5.1 新增 Provider (🟢 容易)

1. 创建 `internal/provider/xxx.go`
2. 实现 `Provider` 接口 4 个方法
3. 在 `registry.go` 添加 `case "xxx":`
4. 在 `config.go` 的 vendor 预设中添加

**预估工作量**：~300-500 行代码

### 5.2 新增 IM Adapter (🟡 中等)

1. 创建 `internal/im/xxx_adapter.go` — 实现 `Sink` 接口（~200-500 行）
2. 在 `adapters.go` 添加启动逻辑（~20 行）
3. 在 `internal/tui/` 创建 `xxx_panel.go` + `i18n_xxx.go`（~200-400 行）
4. 在 `Model` 结构体添加 `xxxPanel *xxxPanelState` 字段
5. 在多个 TUI 文件中添加面板初始化/路由/渲染代码

**瓶颈**：步骤 3-5 是主要摩擦点，每个 adapter 需要修改 TUI 4-5 个文件。

**建议**：引入面板注册机制——adapter 自带 panel factory，TUI 通过注册表动态创建。

### 5.3 新增 Tool (🟢 容易)

1. 创建 `internal/tool/xxx.go`
2. 实现 `Tool` 接口 4 个方法
3. 在注册入口添加 `registry.Register(&XXXTool{})`

**预估工作量**：~100-300 行代码

---

## 六、问题汇总（按严重程度）

### 🔴 严重

| # | 问题 | 位置 | 建议 |
|---|------|------|------|
| 1 | TUI `Model` God Object（165 行字段，120+ 字段） | `internal/tui/model.go` | 拆分为 CoreModel + 子系统状态，引入 Controller 层 |
| 2 | TUI 包规模失控（42k LOC，107 文件） | `internal/tui/` | 拆分为 `panels/`, `i18n/`, `controller` 子包 |
| 3 | IM `runtime.go` 单文件 1880 行 | `internal/im/runtime.go` | 按功能域拆分为 4-5 个文件 |

### 🟡 中等

| # | 问题 | 位置 | 建议 |
|---|------|------|------|
| 4 | 错误包装不一致（48% 未使用 `%w`） | 全局 | 添加 `errorlint` 规则，逐步修复 |
| 5 | 工具注册分散在 3 个文件 | `builtin.go`, `root.go`, `repl.go` | 统一到单一注册入口 |
| 6 | IM adapter 面板注册硬编码 | `internal/tui/model.go` | 引入面板注册表/工厂模式 |
| 7 | `harness.Task` 字段过多（30+） | `internal/harness/task.go` | 拆分为嵌入结构 |
| 8 | i18n 文件碎片化（20+ 文件） | `internal/tui/i18n_*.go` | 按功能域合并 |

### 🟢 建议

| # | 问题 | 位置 | 建议 |
|---|------|------|------|
| 9 | Agent reasoning 解析逻辑可独立 | `internal/agent/agent.go` | 提取到 `agent_reasoning.go` |
| 10 | A2A SSE 流未推送中间进度 | `internal/a2a/server.go` | 实现 streaming artifact events |
| 11 | Provider 缺少能力发现接口 | `internal/provider/provider.go` | 添加 `Capabilities()` 方法 |
| 12 | `ContentBlock` union type 字段过多 | `internal/provider/provider.go` | 考虑使用 `json.RawMessage` + 延迟解析 |
| 13 | Provider retry 中有中文字符串硬编码 | `internal/provider/retry.go:101` | `"网络错误"` 应走 i18n |

---

## 七、架构优势总结

1. **Agent Loop 设计精良**：主循环清晰、回调解耦、并发安全、保护机制完善
2. **Provider 适配器模式成熟**：最小化接口 + 工厂模式 + 自适应 token 限制 + 完善重试
3. **IM 网关设计优秀**：统一 Sink 接口 + 可选接口扩展 + 消息去重 + 多实例检测 + 安全配对
4. **ChatBridge 解耦完美**：webui 完全不依赖 tui/agent 实现，通过接口抽象实现双模式
5. **测试覆盖优秀**：多个包测试 LOC 超过代码 LOC，E2E 测试充分
6. **工程规范到位**：`internal/` 封闭、平台特定代码用 build tags、context 别名避免冲突

---

## 八、重构优先级建议

1. **P0（架构风险）**：拆分 TUI `Model`，引入面板注册机制
2. **P1（可维护性）**：拆分 `runtime.go`，统一工具注册入口
3. **P2（代码质量）**：修复错误包装不一致，合并 i18n 文件
4. **P3（长期演进）**：harness Task 结构拆分，Provider 能力发现
