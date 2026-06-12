# Round 8 Full Review — 2025-05-31

> 4 个子代理并行审查：Go 后端核心、基础设施与安全、构建与运维、IM/Swarm/MCP/Harness

## 总览

| 严重程度 | 数量 | 分布 |
|----------|------|------|
| **Critical** | 7 | 安全(2) + Provider(1) + Session(1) + Swarm(1) + IM(1) + MCP(1) |
| **High** | 30 | 安全(3) + Agent(1) + Provider(2) + Config(2) + Session(2) + Swarm(2) + IM(3) + MCP(2) + Harness(3) + DevOps(10) |
| **Medium** | 42 | 分布在所有模块 |
| **Low** | 20 | 代码风格、配置默认值、文档补充 |

---

## Critical 发现（7 项）

### C1. Relay WebSocket CheckOrigin 全放行
**文件**: `ggcode-relay/relay.go:961`
```go
var upgrader = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
```
Relay 服务器接受任意来源的 WebSocket 连接，且设计上可被网络访问。恶意网页可发起跨站 WebSocket 劫持 (CSWSH)。
**修复**: 验证 `Origin` 头与预期 relay 主机匹配。

### C2. Relay 无 TLS
**文件**: `ggcode-relay/relay.go:1473`
```go
if err := http.ListenAndServe(":"+port, mux); err != nil {
```
Relay 监听明文 HTTP，所有 WebSocket 流量（包括加密隧道载荷、认证票据、会话数据）均以明文传输。
**修复**: 添加 `ListenAndServeTLS()` 支持，或在前端强制要求 TLS 反向代理并明确文档说明。

### C3. Gemini Provider 吞掉消息转换错误
**文件**: `internal/provider/gemini.go:399`
```go
contents, _ := p.convertMessages(messages)
```
`probeChat` 忽略 `convertMessages` 错误，可能向 Gemini API 发送 nil/空 contents。
**修复**: 检查 error，非 nil 则直接返回。

### C4. Session JSONL 半行写入导致无法恢复
**文件**: `internal/session/store.go:1053-1075`
`AppendTunnelEventToDisk` 写入中途崩溃会产生半行记录。加载端 `loadSession` 使用 `json.Decoder` 遇到损坏行会中断整个会话加载。
**修复**: 在 `loadSession` 中对 `Decode` 失败的行记录 warning 并跳过。

### C5. Swarm Manager CancelAll 锁排序风险
**文件**: `internal/swarm/manager.go:328-351`
CancelAll 路径为 `m.mu → team.mu → tm.mu`，但 SpawnTeammate 路径为 `m.mu → team.mu`（不获取 tm.mu）。锁排序不一致可能导致未来死锁。
**修复**: 统一所有跨团队操作遵循 `m.mu → team.mu → tm.mu` 严格排序并文档化。

### C6. IM daemon_bridge TOCTOU 导致 goroutine 阻塞
**文件**: `internal/im/daemon_bridge.go:232-244`
`pendingApproval` 检查存在 TOCTOU：先读取 `approvalCh`（释放 mu），再写入，然后重新获取 mu 清除。两个并发 IM 消息可能同时通过检查，导致向 unbuffered channel 发送两次，第二个永久阻塞。
**修复**: 在同一个 `mu.Lock()` 范围内完成「检查 → 发送 → 清除」。

### C7. MCP Client sendRequest 在锁内做 I/O
**文件**: `internal/mcp/client.go:298-370`
`sendRequest` 在 `c.mu` 保护下通过 `c.stdin.Write` 写入 JSON-RPC 消息。如果子进程 stdin buffer 满导致写入阻塞，整个 Client 会被锁住，所有后续请求（包括 `Close()`）阻塞。
**修复**: 将 stdin 写入移到锁外，或使用带超时的 context。

---

## High 发现（30 项，按模块分组）

### 安全 (3)

| # | 文件 | 问题 |
|---|------|------|
| H1 | `internal/webui/server.go:328` | Request body 无大小限制，可发送任意大 JSON 耗尽内存 |
| H2 | `internal/webui/server_websocket.go:16` | WebSocket CheckOrigin=true，允许跨站劫持 |
| H3 | `internal/a2a/server.go:802` | 无任务创建速率限制，已认证客户端可创建无限任务耗尽内存 |

### Agent (1)

| # | 文件 | 问题 |
|---|------|------|
| H4 | `internal/agent/agent.go:570-600` | tool 执行出错时 contextManager 无回滚机制 |

### Provider (2)

| # | 文件 | 问题 |
|---|------|------|
| H5 | `internal/provider/gemini.go:185` | `FunctionCall.Args` 序列化错误被丢弃 |
| H6 | `internal/provider/openai.go:240-262` | ChatStream 重试循环中未关闭前一个 streamer |

### Config (2)

| # | 文件 | 问题 |
|---|------|------|
| H7 | `internal/config/config.go:26-38` | `configFileLocks` map 永不缩容，长期运行内存泄漏 |
| H8 | `internal/config/config.go:1329` | Save 方法 rename 失败后临时文件不清理 |

### Session (2)

| # | 文件 | 问题 |
|---|------|------|
| H9 | `internal/session/store.go:200-230` | `updateIndex` read-modify-write 非原子 |
| H10 | `internal/session/store.go:350-370` | Save 方法全量重写大型 session 占用大量内存 |

### Swarm (2)

| # | 文件 | 问题 |
|---|------|------|
| H11 | `internal/swarm/idle_runner.go:141` | `msg.ReplyTo` channel 发送无超时保护，消费者超时后 goroutine 永久阻塞 |
| H12 | `internal/swarm/manager.go:402` | `SendToTeammate` 非阻塞发送失败时消息丢失，无重试 |

### IM (3)

| # | 文件 | 问题 |
|---|------|------|
| H13 | `internal/im/daemon_bridge.go:248` | `pendingAsk` 与 `pendingApproval` 相同 TOCTOU 模式 |
| H14 | `internal/im/feishu_adapter.go` | 1730 行 god object，应拆分为多个文件 |
| H15 | `internal/im/qq_adapter.go` | 1538 行 god object，应拆分 |

### MCP (2)

| # | 文件 | 问题 |
|---|------|------|
| H16 | `internal/mcp/client.go:192-233` | 未知 ID 的 response 被静默忽略，无 metric/计数 |
| H17 | `internal/mcp/client.go:395-440` | Start 失败时 Close() 可能因 readLoop 阻塞而永久阻塞 |

### Harness (3)

| # | 文件 | 问题 |
|---|------|------|
| H18 | `internal/harness/worker.go` | Context 取消后 SIGKILL 可能导致 git worktree 脏状态 |
| H19 | `internal/harness/monitor.go:100` | 文件系统轮询高频 I/O，应改用 fsnotify 或缓存 |
| H20 | `internal/harness/worktree.go` | CreateWorktree 崩溃后留下孤立 worktree，无 recovery |

### 构建与运维 (10)

| # | 文件 | 问题 |
|---|------|------|
| H21 | `Makefile:28` | `install` 目标缺少 `-tags goolm` |
| H22 | `Makefile:31` | `install-installer` 同样缺少 `-tags goolm` |
| H23 | `npm.yml:52` | npm publish 缺少 `NODE_AUTH_TOKEN`，发布必然失败 |
| H24 | `npm.yml:52` | `--provenance=false` 禁用 npm provenance 签名 |
| H25 | `release.yml:64` | goreleaser `version: latest` 未锁定版本 |
| H26 | `release.yml:501` | Python heredoc 嵌入 shell 变量存在注入风险 |
| H27 | `mobile-release.yml:84` | Android signing secrets 可能泄露到日志 |
| H28 | `ggcode-relay/Dockerfile:8` | alpine 运行镜像缺非 root 用户 |
| H29 | 全局 | 无 SLSA provenance / 构建出处证明 |
| H30 | 全局 | 缺少回滚策略文档 |

---

## Medium 发现（42 项，按模块概要）

### 安全
- WebUI auth token 为简单共享密钥，无 per-session/expiry/CSRF
- WebUI 内部错误消息泄露到 HTTP 响应（多处）
- Auth store 目录权限 0755 应改为 0700
- JWT validation 缺少 `aud` claim 检查
- A2A instance ID 泄露 hostname 和 PID
- A2A `allow_unauthenticated` 标志无启动警告
- Relay admin token 使用非标准 header

### Agent
- `RunStreamWithContent` 超 200 行，圈复杂度高
- `SummarizeIfRequired` reactive compact retry 无内置上限

### Provider
- `retryWithBackoffCtx` 不区分可重试和不可重试错误（4xx 也重试）
- `ProbeContextWindow` 递归二分搜索无迭代上限
- `saveAdaptiveCaps` 在 hot path 中做文件 I/O

### Config
- `LoadInstanceConfig` deepCopy 用 JSON marshal/unmarshal 性能差
- `ExpandEnvValues` 正则解析 `${VAR:-default}` 边界 case 不正确

### Session
- `repairIndex` 逐个打开文件，sessions 多时 List() 慢
- `EndpointStats.Metrics` slice 无界增长
- `loadSession` checkpoint 恢复逻辑复杂

### Swarm
- `SpawnTeammate` 与 `DeleteTeam` 存在 TOCTOU

### IM
- `wecomAdapter` seen/replyReqIDs map 无清理机制
- `whatsapp_adapter` Stop() 未获取 mu 锁
- `runtime.go` 1113 行需拆分
- `processInboundWithBinding` outputMode 检查逻辑嵌套

### MCP
- `CallTool` 无超时保护
- OAuth `refreshToken` TOCTOU
- OAuth callback handler 无 panic recovery

### Harness
- `SaveTask`/`LoadTask` 无文件锁
- `RunChecks` 无超时保护
- `bootstrapHarnessState` 错误被静默忽略

### 构建与运维
- CI 缺 golangci-lint 步骤
- CI 无 govulncheck 漏洞扫描
- CI 未使用 `-race` 竞态检测
- Dockerfile 缺 .dockerignore
- `verify-ci.sh` 测试 tags 与 CI 文档不一致
- AppImage 工具下载未版本锁定
- git push --force 应改 --force-with-lease
- Keychain 密码熵低

---

## Low 发现（20 项，概要）

- agent: `ToolExecution` 缺 JSON tag；`detectAutopilotLoop` 状态分散
- provider: Copilot token counting 可能不精确；Impersonate RoundTrip marshal 失败无日志
- config: runtime state 字段未文档化；`knownModelCapabilities` 数据表 700+ 行增大二进制
- session: `generateSessionID` 两次 time.Now() 不一致
- swarm: `pollInterval` 默认值分散在 idle_runner 而非 config
- IM: adapter 工厂签名不完全一致；slack maskToken 短 token 边界
- MCP: JSONRPCRequest.Params 为 nil 时输出 `"params":null`；parseLegacyInstallArgs 嵌套条件
- harness: renderConfigTemplate 用 `%q` 非 YAML 标准；Config 缺 Validate()
- DevOps: Dockerfile 缺 HEALTHCHECK；.PHONY 不完整；action 版本不一致

---

## 架构级建议

### 1. 安全加固（最高优先级）
- **Relay 必须支持 TLS** 或强制要求反向代理，并在启动时无 TLS 输出明确警告
- **WebSocket CheckOrigin** 必须验证 Origin，至少对非 localhost 连接
- **HTTP 请求体** 统一添加 `MaxBytesReader` 大小限制
- **错误响应** 全面审查 `err.Error()` 直接返回客户端的位置

### 2. 并发安全统一
- **锁排序文档化**: 在 `docs/` 或 `internal/locks.md` 中记录所有跨模块锁获取顺序
- **TOCTOU 修复**: daemon_bridge.go 的 pendingApproval/pendingAsk 必须在单个锁区间完成
- **MCP Client I/O**: 将 stdin 写入移出 mutex 保护区
- **CI 添加 `-race`**: 至少在一个平台启用竞态检测

### 3. 资源管理
- **Relay 连接限制**: 添加 maxClients/maxRooms/maxMessageSize
- **Session 增量写入**: 大型 session 避免全量重写
- **Map 缩容**: configFileLocks、seen map 等长期运行结构需要清理机制
- **Goroutine 生命周期**: 统一使用 WaitGroup 或 done channel 确保优雅关闭

### 4. 代码质量
- **大文件拆分**: feishu_adapter.go(1730行)、qq_adapter.go(1538行)、runtime.go(1113行)、agent.go RunStreamWithContent(200+行)
- **错误处理统一**: 禁止 `_ =` 吞掉错误，至少记录 warning 日志
- **Provider 重试逻辑**: 区分可重试（5xx、429、网络）和不可重试（4xx）错误

### 5. 构建与发布
- **Makefile install 目标**: 添加 `-tags goolm`
- **npm publish**: 修复 NODE_AUTH_TOKEN
- **goreleaser 版本锁定**: 不要用 `latest`
- **添加 Dependabot**: 自动更新依赖
- **添加回滚文档**: 记录如何撤回错误发布

---

## 与前轮关系

- Round 7 的 6 项 Critical 已审查，本轮发现 3 项已修复（Desktop goroutine 泄漏、temp icon 权限、Flutter Stream 泄漏）
- Round 6 的安全发现（Relay CheckOrigin、无 TLS）**仍未修复**，本轮 re-validated
- 新发现集中在：Relay 安全加固、MCP Client 阻塞、daemon_bridge TOCTOU、构建一致性

---

## 审查方法论

- 使用 4 个并行子代理，每个专注一个领域
- 每个子代理使用 `read_file` / `search_files` / `grep` 工具逐文件审查
- 发现按 Critical / High / Medium / Low 四级分类
- 所有发现均标注具体文件路径和行号
