# ggcode A2A Server 架构设计

> 设计时间：2025-07  
> 背景：ggcode 支持多实例运行（TUI + daemon + harness），需要设计 A2A Server 架构，使多个 ggcode 实例能通过 A2A 协议互相协作。

---

## 一、多实例现状分析

### 当前 ggcode 运行模式

| 模式 | 触发方式 | 生命周期 | 进程形态 |
|------|---------|---------|---------|
| **TUI 交互** | `ggcode` | 前台，用户控制 | 单进程，Bubble Tea |
| **Daemon** | `ggcode daemon` / `Ctrl+D` detach | 后台长驻，IM 驱动 | 单进程，headless |
| **Pipe** | `ggcode -p "prompt"` | 一次性，输出到 stdout | 单进程，非交互 |
| **Harness** | `ggcode harness` | 后台，多 task | 单进程 + git worktrees |
| **Sub-agent** | `spawn_agent` tool | TUI/Daemon 内子任务 | 同进程内 goroutine |

### 实例间当前交互方式

```
                    无直接通信
                 ┌─────────────┐
                 │             │
   ┌─────┐      │  ┌─────┐   │    ┌─────┐
   │TUI 1│──────┤  │Daemon│   ├───▶│TUI 2│
   │ProjA│  IM  │  │ProjA │   │ IM │ProjB│
   └─────┘      │  └─────┘   │    └─────┘
                └─────────────┘
```

**当前问题：**
- 同项目的 TUI 和 Daemon 只能通过 IM 中转
- 不同项目的 ggcode 完全隔离，无法协作
- Sub-agent 只能在单进程内，不能跨进程/跨机器

---

## 二、设计目标

1. **单实例暴露**：每个 ggcode 进程（TUI/Daemon/Harness）都能作为 A2A Server
2. **多实例发现**：同一机器/网络内的 ggcode 实例能互相发现
3. **Task 委派**：实例 A 可以向实例 B 发送 Task（代码修改、文件搜索、review 等）
4. **不侵入现有架构**：A2A 作为可选层，不改变现有 TUI/Daemon/IM 流程
5. **资源隔离**：每个 Task 有独立的 context/workspace 权限

---

## 三、核心架构

### 3.1 总体拓扑

```
┌─────────────────────────────────────────────────────────┐
│                    A2A Mesh 层                          │
│                                                         │
│  ┌───────────┐   ┌───────────┐   ┌───────────┐        │
│  │ ggcode-A  │   │ ggcode-B  │   │ ggcode-C  │        │
│  │ TUI       │   │ Daemon    │   │ Harness   │        │
│  │ proj/web  │   │ proj/api  │   │ proj/full │        │
│  │ :5170     │   │ :5171     │   │ :5172     │        │
│  └─────┬─────┘   └─────┬─────┘   └─────┬─────┘        │
│        │               │               │                │
│        └───────┬───────┴───────┬───────┘                │
│                │  A2A Registry │                        │
│                │  (本地文件)    │                        │
│                └───────────────┘                        │
└─────────────────────────────────────────────────────────┘
         │                           │
         │ A2A Client                │ A2A Server
         ▼                           ▼
┌─────────────────┐    ┌─────────────────┐
│  外部 Agent     │    │  外部 Agent     │
│  (Claude/A2A)   │    │  (Copilot/A2A)  │
└─────────────────┘    └─────────────────┘
```

### 3.2 每个 ggcode 实例的 A2A 组件

```
┌──────────────────────────────────────────┐
│              ggcode 进程                  │
│                                          │
│  ┌──────────┐  ┌──────────────────────┐  │
│  │ 现有组件  │  │    A2A Layer         │  │
│  │          │  │                      │  │
│  │ TUI/     │  │ ┌────────┐ ┌──────┐  │  │
│  │ Daemon/  │  │ │ A2A    │ │ A2A  │  │  │
│  │ Harness  │  │ │ Server │ │Client│  │  │
│  │          │◀─▶│ │        │ │      │  │  │
│  │ Agent    │  │ │ Agent  │ │Task  │  │  │
│  │ Loop     │  │ │ Card   │ │Sender│  │  │
│  │          │  │ │        │ │      │  │  │
│  │ IM       │  │ │ Task   │ │      │  │  │
│  │ Manager  │  │ │ Handler│ │      │  │  │
│  │          │  │ └────────┘ └──────┘  │  │
│  │ Knight   │  │                      │  │
│  │ Session  │  │ ┌──────────────────┐ │  │
│  │ Provider │  │ │ Local Registry   │ │  │
│  │          │  │ │ (instance.json)  │ │  │
│  └──────────┘  │ └──────────────────┘ │  │
│                └──────────────────────┘  │
└──────────────────────────────────────────┘
```

### 3.3 端口分配策略

**问题**：多实例在同一机器上，每个都需要 HTTP 端口。

**方案**：自动端口发现 + 配置覆盖

```yaml
# ggcode.yaml
a2a:
  enabled: true
  port: 0                    # 0 = 自动分配（推荐）
  host: "127.0.0.1"          # 默认仅本机
  # host: "0.0.0.0"         # 开放给网络
  advertised_host: ""        # 外部可达地址（用于跨机器）
```

- `port: 0` → OS 分配空闲端口 → 写入 registry 文件
- `port: 5170` → 固定端口（适合已知部署）
- `host: "127.0.0.1"` → 单机多实例（默认）
- `host: "0.0.0.0"` + `advertised_host` → 跨机器协作

---

## 四、实例注册与发现

### 4.1 本地 Registry

`~/.ggcode/a2a/instances.json` — 所有本机运行实例的注册表：

```json
[
  {
    "id": "ggcode-a1b2c3",
    "pid": 12345,
    "workspace": "/home/user/projects/web",
    "started_at": "2025-07-15T10:30:00Z",
    "a2a_endpoint": "http://127.0.0.1:5170",
    "agent_card": "http://127.0.0.1:5170/.well-known/agent.json",
    "capabilities": ["code-edit", "file-search", "git", "web-fetch"],
    "status": "ready",
    "tags": ["frontend", "react"]
  },
  {
    "id": "ggcode-d4e5f6",
    "pid": 12390,
    "workspace": "/home/user/projects/api",
    "started_at": "2025-07-15T10:35:00Z",
    "a2a_endpoint": "http://127.0.0.1:5171",
    "agent_card": "http://127.0.0.1:5171/.well-known/agent.json",
    "capabilities": ["code-edit", "file-search", "git"],
    "status": "busy",
    "tags": ["backend", "go"]
  }
]
```

### 4.2 生命周期管理

```
启动:
  1. 读 ~/.ggcode/a2a/instances.json
  2. 清理已死实例（检查 PID 是否存活）
  3. 启动 HTTP Server（自动/固定端口）
  4. 写入自己的注册信息
  5. 心跳更新 status 字段

关闭:
  1. 从 instances.json 移除自己
  2. 关闭 HTTP Server

崩溃:
  → PID 不存活 → 下一个启动的实例清理
```

### 4.3 发现方式

```go
// 发现同机器所有 ggcode 实例
func DiscoverLocal() ([]Instance, error)

// 按能力过滤
func DiscoverByCapability(cap string) ([]Instance, error)

// 按项目路径匹配
func DiscoverByWorkspace(path string) ([]Instance, error)
```

跨机器场景：用 DNS-SD (mDNS/Bonjour) 或配置文件中的静态列表。

---

## 五、Agent Card 设计

每个 ggcode 实例的 Agent Card 描述其能力：

```json
{
  "name": "ggcode",
  "description": "AI coding agent for project: web-frontend",
  "url": "http://127.0.0.1:5170",
  "version": "1.1.34",
  "capabilities": {
    "streaming": true,
    "pushNotifications": false
  },
  "skills": [
    {
      "id": "code-edit",
      "name": "Code Editing",
      "description": "Read, write, and edit source files with diff support",
      "inputModes": ["text"],
      "outputModes": ["text"]
    },
    {
      "id": "file-search",
      "name": "File Search",
      "description": "Search files by name, content, or glob pattern",
      "inputModes": ["text"],
      "outputModes": ["text"]
    },
    {
      "id": "command-exec",
      "name": "Command Execution",
      "description": "Run shell commands with timeout and output capture",
      "inputModes": ["text"],
      "outputModes": ["text"]
    },
    {
      "id": "git-ops",
      "name": "Git Operations",
      "description": "Status, diff, log, commit, branch operations",
      "inputModes": ["text"],
      "outputModes": ["text"]
    },
    {
      "id": "code-review",
      "name": "Code Review",
      "description": "Review code changes and provide feedback",
      "inputModes": ["text"],
      "outputModes": ["text"]
    },
    {
      "id": "full-task",
      "name": "Full Task Execution",
      "description": "Execute a complete coding task end-to-end",
      "inputModes": ["text"],
      "outputModes": ["text"]
    }
  ],
  "metadata": {
    "workspace": "/home/user/projects/web",
    "tags": ["frontend", "react"],
    "project_type": "javascript"
  }
}
```

---

## 六、Task Handler 设计

### 6.1 Task 路由

A2A Task 进入后，根据 skill 路由到不同的内部 handler：

```
A2A Task → TaskRouter → ┌─────────────────┐
                        │ code-edit       │→ agent.RunStream (受限 prompt)
                        │ file-search     │→ tool.Execute (直接工具调用)
                        │ command-exec    │→ tool.Execute
                        │ git-ops         │→ tool.Execute
                        │ code-review     │→ agent.RunStream (review prompt)
                        │ full-task       │→ agent.RunStream (完整 prompt)
                        └─────────────────┘
```

### 6.2 权限模型

A2A Task 的权限**低于**本机用户操作：

```go
type A2APermission struct {
    ReadOnly      bool     // 默认 true，只有 full-task 才可写
    AllowedTools  []string // 白名单
    MaxIterations int      // 限制 agent loop 轮次
    Workspace     string   // 限制在指定目录
    Timeout       time.Duration
}
```

| Skill | 读写 | 允许工具 | MaxIterations |
|-------|------|---------|--------------|
| code-edit | 读写 | read_file, write_file, edit_file, search_files | 5 |
| file-search | 只读 | read_file, list_directory, search_files, glob | 3 |
| command-exec | 读写 | run_command | 1 |
| git-ops | 只读 | git_status, git_diff, git_log | 3 |
| code-review | 只读 | read_file, list_directory, search_files, git_diff | 5 |
| full-task | 读写 | 全部 | 配置上限 |

### 6.3 Task 执行流程

```
A2A Client                    A2A Server (ggcode)
    │                              │
    │  POST /a2a                   │
    │  {method: "tasks/send"}      │
    │  {skill: "file-search",      │
    │   input: "find all TODOs"}   │
    │─────────────────────────────▶│
    │                              │ 1. 验证权限
    │                              │ 2. 创建受限 agent
    │                              │ 3. agent.RunStream()
    │                              │
    │  SSE: TaskUpdate             │
    │  {status: "working"}         │
    │◀─────────────────────────────│
    │                              │
    │  SSE: TaskUpdate             │
    │  {status: "completed",       │
    │   artifact: {...}}           │
    │◀─────────────────────────────│
    │                              │
```

---

## 七、与现有架构的集成点

### 7.1 代码结构

```
internal/a2a/              # 新包
├── server.go              # A2A HTTP Server + Agent Card endpoint
├── handler.go             # Task handler（路由 + 权限检查）
├── registry.go            # 本地实例注册/发现/心跳
├── client.go              # A2A Client（向外发送 Task）
├── card.go                # Agent Card 生成
├── permission.go          # A2A 专用权限模型
└── config.go              # A2A 配置解析

cmd/ggcode/
└── daemon.go              # 在 runDaemon 中启动 A2A Server
                           # 在 runRoot 中启动 A2A Server
```

### 7.2 启动集成

```go
// cmd/ggcode/daemon.go 或 root.go

func startA2AServer(cfg *config.Config, agent *agent.Agent, ...) (*a2a.Server, error) {
    if !cfg.A2A.Enabled {
        return nil, nil  // 不启动
    }
    
    srv := a2a.NewServer(a2a.Config{
        Port:       cfg.A2A.Port,
        Host:       cfg.A2A.Host,
        Workspace:  workingDir,
        Agent:      agent,
        Permission: a2a.DefaultPermission(),
    })
    
    if err := srv.Start(); err != nil {
        return nil, err
    }
    
    // 注册到本地 registry
    a2a.Register(srv.InstanceInfo())
    
    return srv, nil
}
```

### 7.3 TUI 集成

在 TUI 中新增 A2A 面板/命令：

```
/a2a discover          — 列出所有可用的 ggcode 实例
/a2a send <id> <task>  — 向指定实例发送 Task
/a2a status            — 查看当前 A2A Server 状态
/a2a tasks             — 查看本实例收到的 Task 列表
```

### 7.4 与 Sub-agent 的关系

```
┌─────────────────────────────────────┐
│          Sub-agent 选择             │
│                                     │
│  本进程 goroutine (现有)             │
│    ↓ 适合短任务、无隔离需求          │
│                                     │
│  本机 A2A (新)                      │
│    ↓ 适合不同项目、需要 workspace    │
│    ↓ 隔离的任务                     │
│                                     │
│  远程 A2A (远期)                    │
│    ↓ 适合跨机器协作                 │
└─────────────────────────────────────┘
```

Agent loop 中的 `spawn_agent` 可以新增 `target` 参数：
- `"local"` — 当前 goroutine（默认，向后兼容）
- `"a2a:<instance-id>"` — 路由到指定本机实例
- `"a2a://host:port"` — 路由到远程实例

---

## 八、配置示例

```yaml
# ggcode.yaml
a2a:
  enabled: true
  port: 0                      # 自动分配端口
  host: "127.0.0.1"            # 本机访问
  advertised_host: ""          # 跨机器时填写公网 IP

  # 默认权限
  default_permission: "readonly"  # readonly / readwrite
  
  # 允许的来源（A2A Client 白名单）
  allowed_origins:
    - "127.0.0.1"              # 本机任意进程
    # - "10.0.0.*"            # 局域网

  # Task 限制
  max_tasks: 5                 # 同时处理的 Task 数
  task_timeout: 300s           # 单个 Task 超时
  
  # 实例标签（用于发现过滤）
  tags:
    - "frontend"
    - "react"

  # 远程实例（静态配置，替代/补充自动发现）
  remote_peers: []
    # - name: "api-server"
    #   url: "http://10.0.1.5:5170"
```

---

## 九、安全考量

| 风险 | 缓解措施 |
|------|---------|
| 未授权 Task 执行 | `allowed_origins` 白名单 + 可选 API Key |
| 恶意代码注入 | A2A Task 使用独立的受限 permission policy |
| 资源耗尽 | `max_tasks` + `task_timeout` + `max_iterations` |
| 敏感文件泄露 | `allowed_dirs` 白名单 + A2A 只读默认 |
| Task 投毒 | 输入 sanitization + system prompt 隔离 |

---

## 十、实施路线

### Phase 1：基础框架（2-3 周）
- `internal/a2a/` 包骨架
- HTTP Server + Agent Card endpoint
- 本地 Registry（instances.json + PID 清理）
- 基础 Task handler（只支持 file-search）
- 配置结构 + 启动集成

### Phase 2：完整 Skill 支持（2 周）
- 所有 6 个 Skill 的 handler 实现
- 权限模型
- Task 生命周期管理（submitted → working → completed）
- SSE 流式响应

### Phase 3：A2A Client + TUI 集成（2 周）
- A2A Client 实现（向其他实例发送 Task）
- TUI `/a2a` 命令
- `spawn_agent` 的 `target` 参数扩展
- 实例发现 UI

### Phase 4：跨机器 + 高级特性（远期）
- DNS-SD / mDNS 自动发现
- 推送通知 (webhook)
- 认证 (API Key / mTLS)
- Knight 集成（自动委派 Task）
