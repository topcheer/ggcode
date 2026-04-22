# ggcode ACP (Agent Client Protocol) 实现计划

> 版本：1.0  
> 日期：2026-04-21  
> 状态：规划中  
> 目标：让 ggcode 实现 ACP 协议，成为 ACP 生态中的 Agent，可被 JetBrains IDE、Zed 等编辑器直接调用

---

## 一、ACP 协议概述

**Agent Client Protocol (ACP)** 由 JetBrains 和 Zed Industries 联合发起，定义了 IDE/编辑器（Client）与 AI 编码 Agent 之间的标准通信协议。

| 维度 | 说明 |
|------|------|
| 传输层 | **stdio**（JSON-RPC 2.0，换行分隔） |
| 架构 | Client 启动 Agent 子进程，通过 stdin/stdout 双向通信 |
| 消息类型 | Methods（请求-响应）+ Notifications（单向通知） |
| 生态 | JetBrains IDE、Zed、Neovim、Emacs 等作为 Client；Claude Agent、Gemini CLI、Copilot、Cursor 等作为 Agent |

### 协议生命周期

```
┌─────────────────────────────────────────────────────────┐
│                    ACP 生命周期                          │
├─────────────────────────────────────────────────────────┤
│                                                         │
│  1. Client 启动 Agent 子进程                             │
│     ggcode acp                                          │
│              │                                          │
│  2. initialize ← ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─    │
│     (协商版本、能力、认证方式)                             │
│              │                                          │
│  3. authenticate ← ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─    │
│     (如果 Agent 要求认证)                                 │
│              │                                          │
│  4. session/new ← ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─   │
│     (创建会话，传入 cwd + MCP servers)                    │
│              │                                          │
│  5. session/prompt ← ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─    │
│     (用户提问)                                           │
│     session/update → (Agent 流式回复)                    │
│              │                                          │
│  6. session/cancel ← ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─    │
│     (取消当前操作)                                       │
│              │                                          │
│  7. 重复 5-6 直到会话结束                                 │
│                                                         │
└─────────────────────────────────────────────────────────┘
```

---

## 二、ggcode 现状分析

### 已有基础

| 组件 | 状态 | 可复用程度 |
|------|------|-----------|
| A2A Server (`internal/a2a/`) | 完整实现 | **低** — ACP 是 stdio JSON-RPC，A2A 是 HTTP JSON-RPC，架构不同 |
| Tool 系统 (`internal/tool/`) | 完整 | **高** — ACP Tool Calls 可直接映射到 ggcode 工具 |
| IM/会话管理 | 完整 | **中** — session 概念可借鉴 |
| Agent Loop | 完整 | **高** — 核心推理循环可复用 |
| Config 系统 (`internal/config/`) | 完整 | **高** — 扩展 ACP 配置即可 |
| TUI (`internal/tui/`) | 完整 | **低** — ACP 模式下不需要 TUI，需要 headless 模式 |
| MCP Client | 已有 | **高** — ACP session/new 传入的 MCP servers 需要动态连接 |

### 新增需求

1. **`ggcode acp` 子命令** — 启动 ACP stdio 服务
2. **JSON-RPC 2.0 stdio 编解码器** — 换行分隔的消息读写
3. **ACP 协议处理** — initialize, authenticate, session/*, notifications
4. **Headless Agent Loop** — 无 TUI 的推理执行循环
5. **流式输出** — 通过 `session/update` 通知回传 Agent 响应
6. **认证方法** — Agent Auth（Device Flow）+ Env Var Auth
7. **ACP Registry 注册** — agent.json + CI 验证

---

## 三、架构设计

### 整体架构

```
JetBrains IDE / Zed (ACP Client)
         │
         │ stdin/stdout (JSON-RPC 2.0, newline-delimited)
         │
         ▼
┌─────────────────────────────────────────┐
│         ggcode acp (subprocess)         │
│                                         │
│  ┌────────────────────────────────────┐ │
│  │        ACP Transport Layer         │ │
│  │   stdin → JSON-RPC decoder         │ │
│  │   JSON-RPC encoder → stdout        │ │
│  └────────────┬───────────────────────┘ │
│               │                         │
│  ┌────────────▼───────────────────────┐ │
│  │        ACP Protocol Handler        │ │
│  │  initialize / authenticate         │ │
│  │  session/new, session/prompt       │ │
│  │  session/cancel, session/update    │ │
│  │  tool_calls → Tool Registry        │ │
│  └────────────┬───────────────────────┘ │
│               │                         │
│  ┌────────────▼───────────────────────┐ │
│  │     Headless Agent Loop            │ │
│  │  (复用现有 LLM 调用 + Tool 执行)    │ │
│  │  → 流式 session/update 回传        │ │
│  └────────────────────────────────────┘ │
└─────────────────────────────────────────┘
```

### 目录结构

```
internal/acp/
├── transport.go      # stdio JSON-RPC 编解码器
├── handler.go        # ACP 协议方法处理
├── session.go        # 会话管理
├── types.go          # ACP JSON-RPC 类型定义
├── auth.go           # 认证处理（Device Flow）
├── agent_loop.go     # Headless Agent 循环
└── acp_test.go       # 单元测试
```

---

## 四、详细设计

### 4.1 Transport Layer (`internal/acp/transport.go`)

JSON-RPC 2.0 over stdio 的编解码器。

```go
package acp

// JSON-RPC 2.0 消息类型
type JSONRPCRequest struct {
    JSONRPC string          `json:"jsonrpc"`          // "2.0"
    ID      *int            `json:"id"`               // null for notifications
    Method  string          `json:"method"`
    Params  json.RawMessage `json:"params,omitempty"`
}

type JSONRPCResponse struct {
    JSONRPC string          `json:"jsonrpc"`
    ID      *int            `json:"id"`
    Result  interface{}     `json:"result,omitempty"`
    Error   *JSONRPCError   `json:"error,omitempty"`
}

type JSONRPCError struct {
    Code    int    `json:"code"`
    Message string `json:"message"`
    Data    interface{} `json:"data,omitempty"`
}

type JSONRPCNotification struct {
    JSONRPC string          `json:"jsonrpc"`
    Method  string          `json:"method"`
    Params  json.RawMessage `json:"params,omitempty"`
}

// Transport 管理 stdio 读写
type Transport struct {
    decoder *json.Decoder   // stdin
    encoder *json.Encoder   // stdout
    mu      sync.Mutex      // stdout 写锁
}

func NewTransport(r io.Reader, w io.Writer) *Transport
func (t *Transport) ReadMessage() (JSONRPCMessage, error)
func (t *Transport) WriteResponse(id int, result interface{}) error
func (t *Transport) WriteError(id int, code int, msg string) error
func (t *Transport) WriteNotification(method string, params interface{}) error
```

**关键约束**：
- stdout **只能** 写 ACP JSON-RPC 消息，日志输出到 stderr
- 消息以 `\n` 分隔，不能包含嵌入换行
- 使用 `bufio.Scanner` 逐行读取

### 4.2 ACP 类型 (`internal/acp/types.go`)

```go
// Initialize
type InitializeParams struct {
    ProtocolVersion    int                `json:"protocolVersion"`
    ClientCapabilities ClientCapabilities `json:"clientCapabilities"`
    ClientInfo         *ImplementationInfo `json:"clientInfo,omitempty"`
}

type InitializeResult struct {
    ProtocolVersion    int                `json:"protocolVersion"`
    AgentCapabilities  AgentCapabilities  `json:"agentCapabilities"`
    AgentInfo          ImplementationInfo `json:"agentInfo"`
    AuthMethods        []AuthMethod       `json:"authMethods"`
}

type ClientCapabilities struct {
    FS       *FSCapability       `json:"fs,omitempty"`
    Terminal bool                `json:"terminal,omitempty"`
    Auth     *AuthCapabilities   `json:"auth,omitempty"`
}

type FSCapability struct {
    ReadTextFile  bool `json:"readTextFile"`
    WriteTextFile bool `json:"writeTextFile"`
}

type AuthCapabilities struct {
    Terminal bool `json:"terminal"`
}

type AgentCapabilities struct {
    LoadSession       bool               `json:"loadSession"`
    PromptCapabilities *PromptCapabilities `json:"promptCapabilities,omitempty"`
    MCPCapabilities   *MCPCapabilities   `json:"mcpCapabilities,omitempty"`
}

type PromptCapabilities struct {
    Image           bool `json:"image"`
    Audio           bool `json:"audio"`
    EmbeddedContext bool `json:"embeddedContext"`
}

type MCPCapabilities struct {
    HTTP bool `json:"http"`
    SSE  bool `json:"sse"`
}

type ImplementationInfo struct {
    Name    string `json:"name"`
    Title   string `json:"title,omitempty"`
    Version string `json:"version"`
}

// Auth Methods
type AuthMethod struct {
    ID          string       `json:"id"`
    Name        string       `json:"name"`
    Description string       `json:"description,omitempty"`
    Type        string       `json:"type,omitempty"`    // "agent" | "env_var" | "terminal"
    Vars        []AuthEnvVar `json:"vars,omitempty"`    // for env_var type
    Link        string       `json:"link,omitempty"`    // URL to get credentials
    Args        []string     `json:"args,omitempty"`    // for terminal type
    Env         map[string]string `json:"env,omitempty"` // for terminal type
}

type AuthEnvVar struct {
    Name     string `json:"name"`
    Label    string `json:"label,omitempty"`
    Secret   *bool  `json:"secret,omitempty"`   // default true
    Optional *bool  `json:"optional,omitempty"` // default false
}

// Session
type SessionNewParams struct {
    CWD        string        `json:"cwd"`
    MCPServers []MCPServer   `json:"mcpServers,omitempty"`
}

type SessionNewResult struct {
    SessionID string `json:"sessionId"`
}

type MCPServer struct {
    Name    string        `json:"name"`
    Command string        `json:"command,omitempty"`
    Args    []string      `json:"args"`
    Env     []EnvVariable `json:"env,omitempty"`
    // HTTP transport
    Type    string        `json:"type,omitempty"`  // "http" | "sse"
    URL     string        `json:"url,omitempty"`
    Headers []HTTPHeader  `json:"headers,omitempty"`
}

type EnvVariable struct {
    Name  string `json:"name"`
    Value string `json:"value"`
}

type HTTPHeader struct {
    Name  string `json:"name"`
    Value string `json:"value"`
}

// Prompt
type SessionPromptParams struct {
    SessionID string         `json:"sessionId"`
    Prompt    []ContentBlock `json:"prompt"`
}

type ContentBlock struct {
    Type string `json:"type"` // "text", "image", "audio", "resource", "resource_link"
    Text string `json:"text,omitempty"`
    // ... 其他字段按需扩展
}

type SessionPromptResult struct {
    // empty on success, agent sends updates via notifications
}

// Session Update Notification
type SessionUpdateParams struct {
    SessionID string       `json:"sessionId"`
    Update    SessionUpdate `json:"update"`
}

type SessionUpdate struct {
    SessionUpdate string       `json:"sessionUpdate"` // "agent_message_chunk", "user_message_chunk", etc.
    Content       *ContentBlock `json:"content,omitempty"`
    // tool calls, etc.
}

// Tool Calls (Agent → Client)
type ToolCall struct {
    ToolName string `json:"toolName"`
    Params   interface{} `json:"params"`
}

// Permission Request (Agent → Client)
type PermissionRequestParams struct {
    SessionID string `json:"sessionId"`
    Request   PermissionRequest `json:"request"`
}

type PermissionRequest struct {
    Type        string `json:"type"` // "fs_write", "fs_read", "terminal"
    Path        string `json:"path,omitempty"`
    Command     string `json:"command,omitempty"`
    Description string `json:"description,omitempty"`
}
```

### 4.3 Protocol Handler (`internal/acp/handler.go`)

```go
type Handler struct {
    transport    *Transport
    sessions     map[string]*Session
    sessionsMu   sync.RWMutex
    agentLoop    *AgentLoop
    initialized  bool
    authenticated bool
    version      int
    clientCaps   ClientCapabilities
    clientInfo   *ImplementationInfo
    cfg          *config.Config
    toolRegistry *tool.Registry
}

func NewHandler(cfg *config.Config, registry *tool.Registry) *Handler

// 主循环 — 从 stdin 读取消息并分派
func (h *Handler) Run(ctx context.Context) error

// 方法分派
func (h *Handler) handleInitialize(params json.RawMessage) (interface{}, error)
func (h *Handler) handleAuthenticate(params json.RawMessage) (interface{}, error)
func (h *Handler) handleSessionNew(params json.RawMessage) (interface{}, error)
func (h *Handler) handleSessionPrompt(params json.RawMessage) (interface{}, error)
func (h *Handler) handleSessionCancel(params json.RawMessage) error

// 处理 Client → Agent 的方法请求
func (h *Handler) handleFSReadTextFile(params json.RawMessage) (interface{}, error)
func (h *Handler) handleFSWriteTextFile(params json.RawMessage) (interface{}, error)
func (h *Handler) handleTerminalCreate(params json.RawMessage) (interface{}, error)
func (h *Handler) handleTerminalOutput(params json.RawMessage) (interface{}, error)
```

**方法路由表**：

| Client → Agent 方法 | 处理函数 |
|---------------------|---------|
| `initialize` | `handleInitialize` |
| `authenticate` | `handleAuthenticate` |
| `session/new` | `handleSessionNew` |
| `session/prompt` | `handleSessionPrompt` |
| `session/load` | `handleSessionLoad` |
| `session/cancel` | `handleSessionCancel` |

| Agent → Client 方法 | 说明 |
|---------------------|------|
| `session/request_permission` | 请求文件系统/终端权限 |
| `session/update` (notification) | 流式输出 Agent 消息 |

### 4.4 Session 管理 (`internal/acp/session.go`)

```go
type Session struct {
    ID          string
    CWD         string
    MCPServers  []MCPServer
    CreatedAt   time.Time
    Cancel      context.CancelFunc
    
    // 会话状态
    conversation []Message
    mu          sync.Mutex
}

type Message struct {
    Role    string         // "user" | "assistant"
    Content []ContentBlock
}
```

### 4.5 Headless Agent Loop (`internal/acp/agent_loop.go`)

这是 ACP 的核心 — 无 TUI 的推理执行循环。复用 ggcode 现有的 LLM 调用和工具执行。

```go
type AgentLoop struct {
    cfg        *config.Config
    registry   *tool.Registry
    transport  *Transport
    session    *Session
    clientCaps ClientCapabilities
}

func NewAgentLoop(cfg *config.Config, registry *tool.Registry, 
    transport *Transport, session *Session, clientCaps ClientCapabilities) *AgentLoop

// 执行一次 prompt 转换
func (al *AgentLoop) ExecutePrompt(ctx context.Context, prompt []ContentBlock) error

// 内部流程：
// 1. 将 ContentBlock[] 转为 LLM 消息格式
// 2. 调用 LLM（流式）
// 3. 流式输出通过 session/update notification 发送给 Client
// 4. 遇到 tool_call 时：
//    a. 如果 Client 支持 fs/terminal 能力，通过 request_permission 请求权限
//    b. 获得许可后执行工具
//    c. 将工具结果加入消息历史
//    d. 继续调用 LLM
// 5. 直到 LLM 输出完成或收到 cancel
```

**关键差异：与 TUI 模式相比**

| 方面 | TUI 模式 | ACP 模式 |
|------|---------|---------|
| 输出 | Bubble Tea 渲染到终端 | `session/update` JSON-RPC notification |
| 权限 | TUI 弹窗让用户选择 | `session/request_permission` 请求 Client |
| 工具执行 | 直接执行 | 可选通过 Client 的 fs/terminal 能力 |
| 输入 | Bubble Tea 键盘事件 | JSON-RPC `session/prompt` |
| MCP 连接 | 配置文件定义 | `session/new` 动态传入 |

### 4.6 认证 (`internal/acp/auth.go`)

ggcode 支持 ACP 三种认证方式：

#### 方式 1：Agent Auth（推荐，用于 Registry）

Agent 独立处理 OAuth 流程。使用 **GitHub Device Flow**（不需要 Client Secret）。

```go
func (h *Handler) getAuthMethods() []AuthMethod {
    return []AuthMethod{
        {
            ID:          "agent",
            Name:        "ggcode Agent Auth",
            Description: "Authenticate through ggcode (GitHub Device Flow)",
            Type:        "agent", // or omit, agent is default
        },
    }
}

func (h *Handler) handleAuthenticate(params json.RawMessage) (interface{}, error) {
    // 解析 params 获取 authMethod ID
    // 如果是 "agent" type:
    //   1. POST https://github.com/login/device/code (只需 client_id)
    //   2. 通过 session/update 发送 user_code + verification_uri 给 Client 显示
    //   3. 轮询 POST https://github.com/login/oauth/access_token
    //   4. 获得 token 后保存到配置
    //   5. 返回成功
}
```

**Device Flow 细节**：

```
Step 1: POST /login/device/code
  client_id=Iv1.xxx  (GitHub OAuth App 的 Client ID)
  scope=read:user

Response:
  device_code=xxx
  user_code=XXXX-XXXX
  verification_uri=https://github.com/login/device
  expires_in=900
  interval=5

Step 2: 显示给用户
  通过 session/update notification 发送给 Client
  Client 在 UI 中显示 code 和 URL

Step 3: 轮询（每 interval 秒）
  POST /login/oauth/access_token
  client_id=Iv1.xxx
  device_code=xxx
  grant_type=urn:ietf:params:oauth:grant-type:device_code

  直到获得 access_token 或 expired

Step 4: 保存 token
  写入 ~/.ggcode/auth.json 或 config
```

#### 方式 2：Env Var Auth

通过环境变量传入 API Key。这是 ggcode 最简单的认证方式。

```go
{
    ID:   "api-key",
    Name: "API Key",
    Type: "env_var",
    Vars: []AuthEnvVar{
        {
            Name:  "GGCODE_API_KEY",
            Label: "API Key",
        },
    },
}
```

#### 方式 3：Terminal Auth（可选）

让 Client 在终端中启动交互式登录。ggcode 已有 TUI 登录流程，可复用。

### 4.7 子命令入口 (`cmd/ggcode/acp.go`)

```go
// ggcode acp — 启动 ACP 服务
func newACPCommand() *cobra.Command {
    return &cobra.Command{
        Use:   "acp",
        Short: "Start ggcode as an ACP agent (stdio JSON-RPC)",
        RunE: func(cmd *cobra.Command, args []string) error {
            cfg, _ := config.Load()
            registry := tool.NewRegistry()
            tool.RegisterBuiltinTools(registry, cfg)
            
            transport := acp.NewTransport(os.Stdin, os.Stdout)
            handler := acp.NewHandler(cfg, registry, transport)
            return handler.Run(cmd.Context())
        },
    }
}
```

**注意**：所有日志输出必须到 stderr，stdout 严格保留给 JSON-RPC 消息。

### 4.8 Config 扩展 (`internal/config/config.go`)

```go
// 新增 ACP 配置
type ACPConfig struct {
    Enabled  bool   `yaml:"enabled"`            // 是否在 config 中启用（可选，主要靠子命令）
    Protocol string `yaml:"protocol,omitempty"`  // "stdio" (default)
    LogLevel string `yaml:"log_level,omitempty"` // ACP 模式的日志级别
}

// Config 结构体中新增：
// ACP ACPConfig `yaml:"acp,omitempty"`
```

---

## 五、ACP Registry 注册

### 5.1 注册流程

1. Fork https://github.com/agentclientprotocol/registry
2. 创建 `ggcode/` 目录
3. 添加 `agent.json`
4. 添加 `icon.svg`（16×16，monochrome `currentColor`）
5. 提交 PR

### 5.2 agent.json

```json
{
  "id": "ggcode",
  "name": "ggcode",
  "version": "1.1.44",
  "description": "An open-source AI coding agent with multi-model support, A2A protocol, and extensible tool system",
  "repository": "https://github.com/topcheer/ggcode",
  "authors": ["topcheer"],
  "license": "MIT",
  "icon": "icon.svg",
  "distribution": {
    "binary": {
      "darwin-aarch64": {
        "archive": "https://github.com/topcheer/ggcode/releases/latest/download/ggcode-darwin-arm64.tar.gz",
        "cmd": "./ggcode",
        "args": ["acp"]
      },
      "darwin-x86_64": {
        "archive": "https://github.com/topcheer/ggcode/releases/latest/download/ggcode-darwin-amd64.tar.gz",
        "cmd": "./ggcode",
        "args": ["acp"]
      },
      "linux-aarch64": {
        "archive": "https://github.com/topcheer/ggcode/releases/latest/download/ggcode-linux-arm64.tar.gz",
        "cmd": "./ggcode",
        "args": ["acp"]
      },
      "linux-x86_64": {
        "archive": "https://github.com/topcheer/ggcode/releases/latest/download/ggcode-linux-amd64.tar.gz",
        "cmd": "./ggcode",
        "args": ["acp"]
      },
      "windows-aarch64": {
        "archive": "https://github.com/topcheer/ggcode/releases/latest/download/ggcode-windows-arm64.zip",
        "cmd": "./ggcode.exe",
        "args": ["acp"]
      },
      "windows-x86_64": {
        "archive": "https://github.com/topcheer/ggcode/releases/latest/download/ggcode-windows-amd64.zip",
        "cmd": "./ggcode.exe",
        "args": ["acp"]
      }
    }
  }
}
```

### 5.3 CI 验证要求

Registry PR 会自动检查：
1. `agent.json` 符合 JSON Schema
2. ID 唯一且符合 `^[a-z][a-z0-9-]*$`
3. icon.svg 格式正确（16×16，monochrome `currentColor`）
4. 所有 distribution URL 可访问
5. binary 覆盖所有 3 个 OS
6. **认证支持**：通过 ACP handshake 验证 agent 的 `initialize` 响应包含非空 `authMethods`

---

## 六、实现阶段

### Phase 1：最小可用的 ACP Agent（预计 3-5 天）

**目标**：实现基本的 ACP 协议，能让 JetBrains IDE 发送 prompt 并收到回复。

| 任务 | 文件 | 说明 |
|------|------|------|
| 1.1 创建 ACP 包骨架 | `internal/acp/` | 目录结构 |
| 1.2 Transport 实现 | `internal/acp/transport.go` | stdio JSON-RPC 编解码 |
| 1.3 类型定义 | `internal/acp/types.go` | 所有 ACP 消息类型 |
| 1.4 Initialize 处理 | `internal/acp/handler.go` | 版本协商 + 能力声明 |
| 1.5 Session 管理 | `internal/acp/session.go` | session/new, session ID 生成 |
| 1.6 Headless Agent Loop | `internal/acp/agent_loop.go` | 核心推理 + 流式 session/update |
| 1.7 子命令入口 | `cmd/ggcode/acp.go` | `ggcode acp` 命令 |
| 1.8 基础测试 | `internal/acp/acp_test.go` | 单元测试 |

**MVP 验收标准**：
- `echo '{"jsonrpc":"2.0","id":0,"method":"initialize",...}' | ggcode acp` 能返回正确的 initialize 响应
- JetBrains IDE 能发现并启动 ggcode
- 能发送 prompt 并收到流式回复

### Phase 2：Tool Calls + 权限请求（预计 2-3 天）

**目标**：Agent 能调用工具（文件读写、命令执行），通过 Client 权限系统。

| 任务 | 说明 |
|------|------|
| 2.1 Tool Call 映射 | ACP tool_call → ggcode tool registry 查找 + 执行 |
| 2.2 权限请求 | `session/request_permission` 发送给 Client |
| 2.3 FS 能力 | 如果 Client 声明 fs 能力，通过 Client 读写文件 |
| 2.4 Terminal 能力 | 如果 Client 声明 terminal 能力，通过 Client 执行命令 |
| 2.5 工具结果回传 | 工具执行结果转为 LLM 消息继续循环 |

### Phase 3：认证 + MCP（预计 2-3 天）

**目标**：实现 Device Flow 认证 + MCP 服务器动态连接。

| 任务 | 说明 |
|------|------|
| 3.1 Auth Methods 声明 | initialize 返回支持的认证方式 |
| 3.2 Device Flow 实现 | GitHub Device Flow 认证（只需 client_id） |
| 3.3 Env Var Auth | 通过环境变量传入 API key |
| 3.4 authenticate 处理 | 处理 Client 的 authenticate 请求 |
| 3.5 MCP 动态连接 | session/new 传入的 MCP servers 动态启动 |
| 3.6 Token 持久化 | 认证 token 保存到 ~/.ggcode/auth.json |

### Phase 4：Session 持久化 + 高级特性（预计 2-3 天）

**目标**：session/load 恢复历史对话 + 自定义能力。

| 任务 | 说明 |
|------|------|
| 4.1 Session 持久化 | 会话历史保存到磁盘 |
| 4.2 session/load | 恢复历史会话 + replay session/update |
| 4.3 session/set_mode | 支持 session 模式切换 |
| 4.4 Slash Commands | 支持 ACP slash_commands 能力 |
| 4.5 Agent Plan | 支持 agentPlan 能力（任务计划） |

### Phase 5：Registry 注册 + 发布（预计 1-2 天）

**目标**：注册到 ACP Registry，正式加入生态。

| 任务 | 说明 |
|------|------|
| 5.1 agent.json | 编写 Registry manifest |
| 5.2 icon.svg | 制作 16×16 monochrome 图标 |
| 5.3 GitHub Release 调整 | 确保 CI 产出所有平台的 binary |
| 5.4 提交 Registry PR | Fork → 提交 → CI 验证 |
| 5.5 文档更新 | README 中添加 ACP 使用说明 |

---

## 七、关键设计决策

### 7.1 为什么新建 `internal/acp/` 而不是扩展 `internal/a2a/`？

| 维度 | A2A | ACP |
|------|-----|-----|
| 传输 | HTTP/SSE | stdio |
| 启动方式 | 嵌入 ggcode 进程内 | Client 启动子进程 |
| 消息格式 | A2A JSON-RPC | ACP JSON-RPC |
| 会话模型 | Task (state machine) | Session (conversation) |
| 认证 | API Key header | Device Flow / Env Var |
| 发现 | `/.well-known/agent.json` | Registry PR |

两者差异太大，强行合并会增加复杂度。独立包更清晰。

### 7.2 Headless 模式 vs 复用 Agent Loop

**方案 A**：复用现有 `internal/agent/` 的循环，注入 ACP 输出适配器。
**方案 B**：在 `internal/acp/` 中新建精简的 Agent Loop。

**选择方案 A**：ggcode 的 Agent Loop 已经处理了 LLM 调用、工具执行、上下文管理等复杂逻辑。ACP 只需提供一个不同的"输出接口"：

```go
// 输出接口
type OutputSink interface {
    SendMessageChunk(content ContentBlock) error
    SendToolCall(name string, params interface{}) error
    SendToolResult(name string, result string) error
    RequestPermission(req PermissionRequest) (bool, error)
}

// TUI 实现
type TUIOutputSink struct { /* ... */ }

// ACP 实现
type ACPOutputSink struct {
    transport *Transport
    sessionID string
}
```

### 7.3 Client FS/Terminal vs Agent 直接执行

ACP 允许两种模式：

1. **Agent 直接执行**：Agent 自己读写文件、执行命令（当前 ggcode 的方式）
2. **通过 Client 执行**：Agent 调用 Client 的 `fs/read_text_file`、`terminal/create` 等方法

**策略**：优先使用 Client 能力（更安全、权限由 Client 控制），降级到 Agent 直接执行。

```go
func (al *AgentLoop) readFile(path string) (string, error) {
    if al.clientCaps.FS != nil && al.clientCaps.FS.ReadTextFile {
        // 通过 Client 的 fs/read_text_file
        return al.requestClientReadFile(path)
    }
    // 降级：Agent 直接读取
    return os.ReadFile(path)
}
```

---

## 八、测试策略

### 8.1 单元测试

| 测试 | 说明 |
|------|------|
| `TestTransport_ReadWrite` | JSON-RPC 消息编解码 |
| `TestInitialize` | 版本协商 + 能力声明 |
| `TestSessionNew` | 会话创建 + ID 生成 |
| `TestSessionPrompt` | 基本对话流 |
| `TestSessionCancel` | 取消正在进行的操作 |
| `TestAuthMethods` | 认证方式声明 |
| `TestToolCallMapping` | ACP tool → ggcode tool 映射 |
| `TestPermissionRequest` | 权限请求流程 |

### 8.2 集成测试

使用管道模拟 Client-Agent 交互：

```go
func TestACPIntegration(t *testing.T) {
    clientIn, agentOut := io.Pipe()
    agentIn, clientOut := io.Pipe()
    
    // 启动 Agent
    transport := NewTransport(agentIn, agentOut)
    handler := NewHandler(cfg, registry, transport)
    go handler.Run(ctx)
    
    // 模拟 Client
    client := NewTransport(clientIn, clientOut)
    
    // 1. Initialize
    resp := client.SendRequest("initialize", params)
    // 2. Session New
    resp = client.SendRequest("session/new", params)
    // 3. Prompt
    resp = client.SendRequest("session/prompt", params)
    // 4. 验证 session/update notifications
}
```

### 8.3 手动测试

```bash
# 1. 基本协议测试
echo '{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":1,"clientCapabilities":{}}}' | ggcode acp

# 2. 使用 JetBrains IDE
# Settings → Tools → Agent Client Protocol → Add Agent → 选择 ggcode

# 3. 使用 Zed
# 在 Zed 配置中添加 ggcode 作为 ACP agent
```

---

## 九、风险与注意事项

### 9.1 stdout 污染

**风险**：任何 fmt.Println 或 log 输出到 stdout 都会破坏 JSON-RPC 协议。

**对策**：
- ACP 模式下，全局将 stdout 重定向到 stderr（或用 `log.SetOutput(os.Stderr)`）
- Transport 使用独立的 writer（实际是 os.Stdout，但需要确保没有其他代码写入）
- CI 中添加测试验证 stdout 输出都是合法 JSON

### 9.2 进程生命周期

**风险**：Client 可能随时关闭 stdin 或杀进程。

**对策**：
- 监听 stdin EOF → 优雅退出
- 使用 context.WithCancel 控制所有 goroutine
- Session 状态不丢失（持久化到磁盘）

### 9.3 并发安全

**风险**：多个 session/prompt 可能并发到达。

**对策**：
- 当前 ACP 协议似乎是单 session 的，但实现上支持多 session
- 每个 session 有独立的 mutex
- Transport 写操作有全局 mutex

### 9.4 ACP 协议版本

**风险**：ACP 还在快速演进，规范可能变化。

**对策**：
- 当前实现 `protocolVersion: 1`
- 通过 capabilities 机制做向前兼容
- 关注 https://github.com/agentclientprotocol/agent-client-protocol 的更新

---

## 十、参考资源

- [ACP 官方文档](https://agentclientprotocol.com/)
- [ACP GitHub](https://github.com/agentclientprotocol/agent-client-protocol)
- [ACP Registry](https://github.com/agentclientprotocol/registry)
- [ACP Registry RFD](https://agentclientprotocol.com/rfds/acp-agent-registry)
- [ACP Auth Methods RFD](https://agentclientprotocol.com/rfds/auth-methods)
- [JetBrains ACP 介绍](https://www.jetbrains.com/acp/)
- [GitHub Device Flow](https://docs.github.com/en/apps/oauth-apps/building-oauth-apps/authorizing-oauth-apps#device-flow)
- [JSON-RPC 2.0 Spec](https://www.jsonrpc.org/specification)
