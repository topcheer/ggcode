# Agent-to-Agent 协议标准调研报告

> 调研时间：2025-07  
> 背景：ggcode 正在构建 Knight 自动进化系统，需要评估 agent 互操作性协议的现状，为未来多 agent 协作架构做准备。

---

## 一、协议全景

当前 Agent 互操作领域形成了 **4 + 1** 的格局：

| 协议 | 全称 | 发起方 | 定位 | 状态 |
|------|------|--------|------|------|
| **MCP** | Model Context Protocol | Anthropic | Agent ↔ 工具/数据源 | 成熟，事实标准 |
| **A2A** | Agent-to-Agent Protocol | Google → Linux Foundation | Agent ↔ Agent | v1.0 发布，快速增长 |
| **ACP** | Agent Connect Protocol | AGNTCY (Cisco) | Agent ↔ Agent (REST) | **已归档**，并入 A2A |
| **ANP** | Agent Network Protocol | 开源社区 (chgaowei) | Agent 互联网基础协议 | 早期，概念验证 |
| **AGP** | Agentic Gateway Protocol | AGNTCY | 网关/代理层 | 随 ACP 归档 |

---

## 二、MCP — Model Context Protocol

### 基本信息
- **发起方**：Anthropic（Claude 的公司）
- **定位**：Agent 连接工具和数据源的协议（Agent → Tool）
- **传输**：JSON-RPC 2.0，支持 stdio 和 HTTP+SSE
- **协议版本**：2025-11 规范发布（一周年版本）
- **开源**：github.com/modelcontextprotocol
- **生态数据**：
  - GitHub Stars: ~35k+（protocol 仓库）
  - 客户端 SDK：Python, TypeScript, Java, Go, Rust, C#, Swift
  - 服务端实现：数百个社区 MCP Server
  - 厂商支持：Anthropic Claude Desktop, Cursor, Zed, Sourcegraph Cody, 终端工具（ggcode 等）

### 核心概念
```
┌──────────────┐     MCP      ┌──────────────┐
│   MCP Host   │◄────────────►│  MCP Server  │
│  (Agent/App) │  JSON-RPC    │  (Tool/Data) │
└──────────────┘              └──────────────┘
```
- **Host**：发起连接的 AI 应用（如 Claude Desktop、ggcode）
- **Server**：暴露工具、资源、提示词的服务端
- **能力**：tools（可调用函数）、resources（可读数据）、prompts（模板）

### 成熟度评估
| 维度 | 评分 | 说明 |
|------|------|------|
| 规范完整度 | ★★★★★ | 协议定义清晰，有完整的 capability negotiation |
| SDK 覆盖 | ★★★★★ | 7+ 语言官方 SDK，ggcode 已内置 MCP 客户端 |
| 生产可用 | ★★★★★ | 大量生产级 Server，Claude Desktop/Cursor 默认集成 |
| 社区活跃 | ★★★★★ | 最大的 AI 工具生态 |
| 厂商支持 | ★★★★☆ | Anthropic 主导，OpenAI 未原生支持（但可兼容） |

### 关键结论
**MCP 解决的是 "Agent 如何使用工具"，不是 "Agent 如何与 Agent 对话"。** 这是 ggcode 已经集成的协议，不需要额外决策。

---

## 三、A2A — Agent-to-Agent Protocol（⭐ 重点关注）

### 基本信息
- **发起方**：Google（2025年4月发布）→ 2025年6月移交 Linux Foundation
- **定位**：Agent 与 Agent 之间的协作协议（Agent ↔ Agent）
- **传输**：JSON-RPC 2.0 over HTTP(S)，支持 SSE 流式和推送通知
- **协议版本**：v1.0（2026年3月），规范稳定
- **开源**：github.com/a2aproject/A2A
- **生态数据**：
  - GitHub Stars: **23.3k**（增长极快）
  - Forks: 2.4k
  - 支持厂商: **150+**（从最初 50 家增长）
  - SDK: Python, **Go**, Java, JavaScript, C#/.NET, Rust（6 种官方 SDK）
  - Go SDK: github.com/a2aproject/a2a-go（349 stars, 22 个 release, v2.2.0）

### 核心架构
```
┌──────────────┐     A2A      ┌──────────────┐
│  A2A Client  │◄────────────►│  A2A Server  │
│  (发起方)     │  JSON-RPC    │  (远程Agent) │
│              │  HTTP/SSE    │              │
└──────────────┘              └──────────────┘
      │                              │
      │    Agent Card (发现)          │
      │◄─────────────────────────────┘
      │    Task (协作)                │
      │◄─────────────────────────────►│
```

### 核心概念

1. **Agent Card**（`.well-known/agent.json`）：Agent 的"名片"，描述能力、认证方式、传输协议
2. **Task**：协作的基本单位。Client 创建 Task，Server 执行，支持多轮对话
3. **Message & Part**：消息包含文本、文件、结构化数据等 Parts
4. **不透明原则**：Agent 不暴露内部状态、记忆或工具实现——只通过 Task 接口交互
5. **Push Notification**：长任务完成后通过 webhook 通知

### Task 生命周期
```
submitted → working → completed
                    → failed
                    → canceled
         → input-required (等待用户输入)
```

### 与 MCP 的关系（互补，非竞争）
```
┌─────────────────────────────────────────┐
│              A2A 层                      │
│        Agent ↔ Agent 协作               │
│  "你能帮我做 code review 吗？"           │
└──────────────┬──────────────────────────┘
               │
┌──────────────▼──────────────────────────┐
│              MCP 层                      │
│        Agent ↔ 工具/数据                 │
│  "读取这个文件"、"执行这条命令"            │
└─────────────────────────────────────────┘
```
- A2A 是 Agent 之间的协议（横向）
- MCP 是 Agent 与工具之间的协议（纵向）
- 两者互补，一个 Agent 可以同时是 A2A Server 和 MCP Client

### 厂商支持（150+）

| 类别 | 厂商 |
|------|------|
| **云厂商** | Google Cloud、**Microsoft Azure**（正式合作）、AWS |
| **企业软件** | SAP、Salesforce、ServiceNow、Atlassian、Intuit |
| **AI 平台** | LangChain、Cohere、LlamaIndex |
| **基础设施** | Cisco、IBM |
| **垂直领域** | Box、Adobe、PayPal |

**Microsoft 的立场尤其重要**——Azure AI Foundry 和 Copilot Studio 正式支持 A2A 互操作，意味着 A2A 不会是 Google-only 的协议。

### Go SDK 详情

```
go get github.com/a2aproject/a2a-go/v2
```

- 支持传输：gRPC、REST、JSON-RPC
- 模块：a2asrv (服务端), a2aclient (客户端), a2agrpc, a2apb (protobuf)
- CLI 工具：`a2a discover` / `a2a send` / `a2a serve`
- 状态：v2.2.0，活跃开发中

### 成熟度评估
| 维度 | 评分 | 说明 |
|------|------|------|
| 规范完整度 | ★★★★☆ | v1.0 稳定，核心概念清晰，扩展机制在完善中 |
| SDK 覆盖 | ★★★★☆ | 6 种官方 SDK，Go SDK 质量不错 |
| 生产可用 | ★★★☆☆ | SDK 稳定但真正的生产部署案例还不多 |
| 社区活跃 | ★★★★★ | 23k stars，150+ 厂商支持，Linux Foundation 托管 |
| 厂商支持 | ★★★★★ | Google + Microsoft + AWS 三大云厂商齐 backing |

---

## 四、ACP — Agent Connect Protocol

### 基本信息
- **发起方**：AGNTCY Collective（Cisco 主导）
- **定位**：Agent 间 REST API 标准接口
- **规范**：OpenAPI 定义，纯 REST 风格
- **状态**：**2026年4月归档**（repository archived）
- **数据**：164 stars, 9 forks

### 归档原因
ACP 被 AGNTCY 集体决策并入 A2A 生态。ACP 的核心思想（RESTful Agent API、Agent 描述）被吸收进 A2A 规范。AGNTCY 转向构建 agentgateway（同时支持 A2A 和 MCP 的网关代理）。

### 评估
**ACP 已死，不建议投入。** 但其设计理念（OpenAPI 定义、REST 优先）对 A2A 有影响。

---

## 五、ANP — Agent Network Protocol

### 基本信息
- **发起方**：个人开源项目（chgaowei），非商业
- **定位**：Agent 互联网的基础协议——"Agentic Web 的 HTTP"
- **三层架构**：身份层（W3C DID）→ 元协议层（协议协商）→ 应用层（语义网）
- **状态**：早期，1.3k stars，480 commits
- **技术特点**：
  - 去中心化身份（DID），不依赖任何中心化系统
  - 元协议：Agent 间可以协商使用什么协议通信
  - 端到端加密
  - W3C 社区组参与

### 评估
**理想很宏大但现实很骨感。** ANP 的"Agent 互联网"愿景远超当前市场需求。没有大型厂商 backing，社区小，没有生产部署。技术上有创新（DID 身份、元协议协商），但短期内不可能成为主流。

---

## 六、综合对比

```
成熟度/采用度
    ▲
    │  MCP ★★★★★
    │  ████████████████████████
    │  "Agent 用工具" 的事实标准
    │
    │  A2A ★★★★☆
    │  ██████████████████
    │  "Agent 找 Agent" 的最大公约数
    │
    │  ACP ★★☆☆☆ (归档)
    │  ████
    │
    │  ANP ★☆☆☆☆
    │  ██
    │  "Agent 互联网" 的理想主义
    │
    └──────────────────────────────────────►
     工具连接 ←────────────────→ Agent 对等网络
```

| 维度 | MCP | A2A | ACP | ANP |
|------|-----|-----|-----|-----|
| 定位 | Agent→Tool | Agent→Agent | Agent→Agent | Agent 互联网 |
| 传输 | stdio/HTTP | HTTP/SSE/gRPC | REST | HTTP/DID |
| 规范成熟度 | 成熟 | v1.0 稳定 | 已归档 | 早期草案 |
| Stars | ~35k | 23k | 164 | 1.3k |
| 官方 SDK | 7+ | 6 | 0 | 1 (Python) |
| 大厂 Backing | Anthropic | Google+MS+AWS | Cisco | 无 |
| 生产部署 | 广泛 | 起步中 | 无 | 无 |
| 与 ggcode 关系 | 已集成 | 候选集成 | 不考虑 | 观察 |

---

## 七、对 ggcode 的战略建议

### 近期（1-3 个月）：观望 A2A
- A2A v1.0 刚发布，Go SDK 还在快速迭代（v2.2.0）
- 建议：**跟踪 a2a-go 仓库**，关注 API 稳定性，不急于集成
- ggcode 当前的 subagent 系统已经够用（spawn_agent + MCP tools）

### 中期（3-6 个月）：评估 A2A 集成
当以下条件满足时，考虑将 ggcode 作为 A2A Server 暴露：
1. a2a-go 达到 v3.0+ 且 API 稳定
2. 至少有一个主流 IDE/终端 Agent 支持 A2A Client
3. 社区有成熟的 A2A 服务发现方案（Agent Card 目录）

集成方式：
```
ggcode 作为 A2A Server:
  Agent Card → 暴露"代码编辑"、"文件搜索"、"Git 操作"等 Skills
  Task → 接收其他 Agent 的代码请求，执行后返回结果

ggcode 作为 A2A Client:
  发现 → 查找"测试 Agent"、"文档 Agent"、"Review Agent"
  Task → 委派子任务给专门的 Agent
```

### 远期（6-12 个月）：Agent 市场网络
如果 A2A 生态成熟，ggcode 可以：
1. 在 Knight 系统中集成 A2A 服务发现——自动发现可用的外部 Agent
2. 将 Knight 生成的 Skills 通过 Agent Card 发布给其他 Agent
3. 构建 ggcode 专用的 A2A Skill 市场网络

### 风险
- A2A 可能面临与 MCP 的标准战（虽然定位互补，但边界可能模糊）
- Go SDK 仍然年轻，API 可能有大变动
- 推送通知和安全模型还需要生产验证

---

## 八、参考资料

- A2A 规范：https://a2aproject.github.io/A2A/latest/specification/
- A2A GitHub：https://github.com/a2aproject/A2A (23.3k stars)
- A2A Go SDK：https://github.com/a2aproject/a2a-go (349 stars, v2.2.0)
- MCP 规范：https://modelcontextprotocol.io/
- ACP (归档)：https://github.com/agntcy/acp-spec (164 stars)
- ANP：https://github.com/agent-network-protocol/AgentNetworkProtocol (1.3k stars)
- Linux Foundation 公告：https://www.linuxfoundation.org/press/announcing-agent2agent-protocol-project
- Microsoft + Google A2A 合作：https://azure.microsoft.com/en-us/blog/empowering-multi-agent-apps-with-a2a
- A2A DeepLearning.AI 课程：https://www.deeplearning.ai/shortcourses/a2a-the-agent2agent-protocol
