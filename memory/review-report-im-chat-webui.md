# IM / Chat / WebUI 模块深度代码审查报告

审查范围：
- `internal/im/` — IM 网关运行时（~100 文件, ~24k LOC）
- `internal/chat/` — 聊天核心逻辑（8 文件, ~2.1k LOC）
- `internal/webui/` — WebUI HTTP 服务器 + WebSocket（14 文件, ~2.1k LOC）

审查人：im-webui-reviewer
日期：2025-07-14

---

## 摘要

三个模块整体代码质量较高。架构清晰，IM 适配器模式实现良好，WebUI 的 ChatBridge 抽象干净地解耦了 TUI/daemon 两种模式。但存在若干值得注意的安全、并发和可靠性问题。

| 严重程度 | 数量 |
|----------|------|
| Critical | 2 |
| High | 5 |
| Medium | 7 |
| Low | 5 |

---

## Critical（严重）

### C-1: WebUI Auth Token 比较未使用 constant-time — 时序侧信道

**文件**: `internal/webui/auth.go:35,43`

```go
if strings.TrimPrefix(auth, "Bearer ") == s.authToken {
// ...
if r.URL.Query().Get("token") == s.authToken {
```

使用普通字符串 `==` 比较 auth token，在理论上允许通过时序攻击逐字节恢复 token。虽然此服务绑定在 `127.0.0.1`，但最佳实践仍应使用 `crypto/subtle.ConstantTimeCompare` 或 `hmac.Equal`。

**修复建议**: 使用 `subtle.ConstantTimeCompare([]byte(provided), []byte(s.authToken)) == 1`

---

### C-2: WebSocket `wsSend` 方法存在并发写竞态（非 bridge 模式）

**文件**: `internal/webui/server_websocket.go:280-286`

```go
func (s *Server) wsSend(conn *websocket.Conn, msg interface{}) {
    s.mu.RLock()
    defer s.mu.RUnlock()
    if err := conn.WriteJSON(msg); err != nil { ... }
}
```

`wsSend`/`wsSendError`/`wsSendEvent` 这些旧方法直接对 gorilla/websocket 连接写数据，但 gorilla/websocket 要求所有写操作在同一线程上串行化。在 bridge 模式下，`handleChatWS` 使用了正确的 write channel 模式（第 99 行），但这些遗留方法仍然存在且在非 bridge（legacy）路径被调用。`s.mu.RLock()` 无法保证写串行化——多个读锁可以同时持有。

**修复建议**: 废弃 `wsSend` 系列方法，统一使用 bridge 模式的 write channel 模式。或者至少将 RLock 改为互斥 Lock。

---

## High（高）

### H-1: HTTP 请求体无大小限制 — 拒绝服务风险

**文件**: `internal/webui/server.go:328-331`

```go
func readJSON(r *http.Request, v interface{}) error {
    defer r.Body.Close()
    return json.NewDecoder(r.Body).Decode(v)
}
```

所有 REST API 端点的 `readJSON` 函数没有对请求体大小做限制。恶意客户端可以发送极大的 JSON payload 导致 OOM。特别是 WebSocket 文件上传（base64 解码后的文件）也没有大小限制。

**修复建议**: 在 `readJSON` 中添加 `http.MaxBytesReader(w, r.Body, maxBodySize)` 限制（例如 1MB），并在 WebSocket handler 中限制 base64 数据大小。

---

### H-2: WebSocket `CheckOrigin` 始终返回 true — 跨站 WebSocket 劫持

**文件**: `internal/webui/server_websocket.go:16-18`

```go
var upgrader = websocket.Upgrader{
    CheckOrigin: func(r *http.Request) bool { return true },
}
```

虽然服务绑定在 `127.0.0.1` 且需要 auth token，但 `CheckOrigin: always true` 意味着任何能猜到 token 的恶意网页都可以建立 WebSocket 连接。在 token 泄露的情况下，攻击者可以从任意网站发起跨站 WebSocket 连接。

**修复建议**: 至少验证 `Origin` 是 localhost 或与 `Host` 匹配。

---

### H-3: DaemonBridge `pendingApproval` channel 可能泄漏（goroutine 泄漏）

**文件**: `internal/im/daemon_bridge.go:442-444, 478-480`

```go
// 设置
b.pendingApproval = ch
// 清除
b.pendingApproval = nil
```

`pendingApproval` channel 在 agent 运行期间被创建，但只在 `runAgentStream` 成功完成后（第 478 行）或被显式写入时清除。如果 agent 在 `AskUser` 等待审批时崩溃或被取消，`pendingApproval` channel 不会被清理，后续的 approval 回复可能被写入一个永远不会被读取的 channel，造成 goroutine 泄漏。

**修复建议**: 在 agent 上下文取消时也清理 `pendingApproval`，并确保 channel 有缓冲或有超时机制。

---

### H-4: IM 适配器重启时 seenEvents / seenNonces map 无清理机制

**文件**: `internal/im/feishu_adapter.go:90-94`

```go
seenMu     sync.Mutex
seenEvents map[string]time.Time
seenNonces map[string]time.Time
```

Feishu 适配器的去重 map `seenEvents` 和 `seenNonces` 在适配器生命周期内只增不减。对于长时间运行的 daemon，这些 map 会无限增长。虽然单个条目很小，但高频消息场景下可能积累大量条目。

**修复建议**: 添加定期清理过期条目的 GC 逻辑（例如每 10 分钟清理超过 1 小时的条目）。

---

### H-5: Legacy WebSocket 路径中 agent 互斥模式不安全

**文件**: `internal/webui/server_websocket.go:206-236`

在 legacy（非 bridge）模式下：
```go
if !s.agentBusy.CompareAndSwap(false, true) { ... }
s.agentMu.Lock()
// ... agent 运行 ...
s.agentMu.Unlock()
s.agentBusy.Store(false)
```

如果 agent 运行时发生 panic，`s.agentMu.Unlock()` 永远不会被调用（虽然 `safego` 捕获了 panic，但这里是同步代码，不在 safego 内）。同时，`s.agentBusy` 也不会被重置为 false，导致所有后续 WebSocket 消息被拒绝。

**修复建议**: 使用 `defer` 确保 unlock 和 reset。或完全废弃 legacy 模式，强制使用 ChatBridge。

---

## Medium（中等）

### M-1: REST API 缺少 Content-Type 验证

**文件**: `internal/webui/server.go:328-331`

`readJSON` 函数不验证请求的 `Content-Type` 是否为 `application/json`。使用 `Content-Type: text/plain` 的 POST 请求也能正常处理，这在某些代理/CDN 场景下可能成为安全问题。

**修复建议**: 在 `readJSON` 中检查 `r.Header.Get("Content-Type")` 是否包含 `application/json`。

---

### M-2: JSON Decoder 未禁用未知字段

**文件**: 所有 `readJSON` 调用

使用 `json.NewDecoder(r.Body).Decode(v)` 时未调用 `Decoder.DisallowUnknownFields()`。这意味着客户端可以发送包含未知字段的 JSON，不会被拒绝。虽然不是严重问题，但在 API 演进中可能导致客户端依赖未文档化的行为。

**修复建议**: 根据严格程度需求，选择性添加 `DisallowUnknownFields()`。

---

### M-3: WebUI SPA 路径遍历风险（低概率）

**文件**: `internal/webui/server_static.go`

SPA 服务使用 `embed.FS`，这本身就防止了路径遍历（embed.FS 不允许 `..` 路径）。但如果未来改为从磁盘读取静态文件，需要注意路径清理。当前实现安全。

**风险等级**: 提醒级别，当前无实际风险。

---

### M-4: Discord 适配器心跳 goroutine 可能泄漏

**文件**: `internal/im/discord_adapter.go:147-148, 183`

```go
heartbeatCtx, heartbeatCancel := context.WithCancel(ctx)
defer heartbeatCancel()
safego.Go("im.discord.heartbeat", func() { a.heartbeatLoop(heartbeatCtx, conn, interval) })
```

当 `connectAndServe` 返回时（例如连接断开），`heartbeatCancel()` 会被调用以停止心跳。但如果心跳 goroutine 在写入已关闭的连接时阻塞，`heartbeatCtx.Done()` 不会被检查（取决于心跳实现）。`conn.Close()` 在 defer 中执行，所以最终会解除阻塞，但时序可能不够理想。

**修复建议**: 确保心跳循环在每次迭代开始时检查 context。

---

### M-5: `CachedItem` 不是线程安全的

**文件**: `internal/chat/item.go:20-47`

`CachedItem` 的 `GetCached`/`SetCached`/`Invalidate` 方法没有任何同步机制。`AssistantItem.SetText()` 可能在 streaming 期间从一个 goroutine 调用，而 `Render()` 从另一个 goroutine 调用。

虽然 `List` 本身有 `sync.RWMutex` 保护 `items` 切片的增删操作，但 item 内部状态的修改（如 `SetText`）和读取（如 `Render`）不在锁保护范围内。

**修复建议**: 给 `CachedItem` 添加 `sync.Mutex` 或确保调用方在访问 item 时持有 List 的锁。

---

### M-6: Feishu adapter token 刷新缺乏互斥

**文件**: `internal/im/feishu_adapter.go` (token 字段)

`feishuAdapter.token` 和 `tokenExpire` 在 `mu` 读写锁保护下读写。但 `refreshToken` 方法获取新 token 后更新这些字段时是否持有锁需要仔细审查。如果发送消息和 token 刷新并发，可能使用过期的 token。

**修复建议**: 确保 `refreshToken` 在写入 token 字段时持有 `mu.Lock()`。

---

### M-7: WebUI config 保存竞争 — 并发写入同一文件

**文件**: `internal/webui/server_handlers.go` 多处 `s.saveConfig()`

多个 API handler 都调用 `s.saveConfig()`（例如同时更新 vendor 和 endpoint）。虽然有 `s.mu.Lock()` 保护内存中的 config，但 `saveConfig` 内部的文件写入不是原子操作（写入一半崩溃会损坏文件）。

**修复建议**: 使用 write-to-temp-then-rename 模式确保原子性。

---

## Low（低）

### L-1: 测试覆盖不均衡

`internal/chat/` 的 `coverage_test.go` 主要是覆盖率测试而非行为测试。`tools.go`（1062 行）的大量渲染逻辑缺少针对性单元测试。`internal/im/` 的 E2E 测试（如 `daemon_bridge_e2e_test.go`）很好，但单元测试在部分适配器中偏少。

---

### L-2: `feishuMaxTextLen = 4000` 硬编码

**文件**: `internal/im/feishu_adapter.go:60`

消息长度限制硬编码为常量。如果飞书 API 调整限制，需要修改代码重新编译。建议做成可配置的常量。

---

### L-3: `writeJSON` 忽略编码错误

**文件**: `internal/webui/server.go:315-320`

```go
func writeJSON(w http.ResponseWriter, v interface{}) {
    w.Header().Set("Content-Type", "application/json")
    enc := json.NewEncoder(w)
    enc.SetIndent("", "  ")
    _ = enc.Encode(v)
}
```

编码错误被静默忽略。如果 `v` 包含无法 JSON 序列化的类型（如 channel、function），客户端会收到截断的 JSON。

---

### L-4: IM emitter 的 fanout 没有背压机制

**文件**: `internal/im/runtime.go:686`

```go
safego.Go("im.runtime.fanOut", func() { ... })
```

Fanout 使用独立 goroutine 发送消息到各适配器，但没有背压控制。如果某个适配器发送缓慢，消息会在 channel 中积压。当前使用 buffered channel（容量未明确看到），但应有监控或限制。

---

### L-5: 嵌入式 SPA 的 `dist/` 目录较大

`internal/webui/dist/` 包含嵌入的 SPA 前端，这是一个单文件 HTML（约 2500+ 行内联 JS/CSS）。虽然用 `//go:embed` 很方便，但使得二进制体积较大。考虑是否需要外部化或按需加载。

---

## 架构亮点

1. **ChatBridge 接口设计优秀**: `webui.ChatBridge` 干净地解耦了 TUI/daemon 模式，`TUIChatBridge` 和 `DaemonBridge` 分别实现，通过 `program.Send()` 和 `pendingInterruptions` 路由消息。

2. **IM 适配器模式一致**: 所有适配器（QQ、Telegram、Discord、Slack、DingTalk、Feishu、WeChat、WhatsApp、Signal、Matrix、Mattermost、Nostr、IRC、Twitch）遵循统一的 `Adapter` 接口，注册机制清晰。

3. **TOCTOU 安全的 run slot**: `DaemonBridge.SendUserMessage` 在单个 mutex lock 下检查 `cancelFunc` 并决定是排队 interruption 还是启动新 run，防止了竞态条件。

4. **safego 全局覆盖**: 所有 goroutine 通过 `safego.Go` 启动，内置 panic 恢复，防止单个子系统崩溃导致整个进程退出。

5. **虚拟列表渲染优化**: `chat.List` 使用虚拟滚动，只渲染视口内的 items，配合 `CachedItem` 缓存机制，对长会话性能友好。

6. **Echo suppression**: IM 网关的 echo suppression 机制（通过 `LastInboundMessageID` / `LastOutboundMessageID` 对比）防止消息回环。

7. **WebUI write channel 模式**: Bridge 模式下的 WebSocket 使用独立 write goroutine + buffered channel（容量 64），正确处理了 gorilla/websocket 的并发写限制。

---

## 修复优先级建议

| 优先级 | 问题 | 修复复杂度 |
|--------|------|-----------|
| P0 | C-1: constant-time token 比较 | 低（1行改动）|
| P0 | C-2: wsSend 并发竞态 | 中（移除旧方法或改用互斥锁）|
| P1 | H-1: 请求体大小限制 | 低（3行改动）|
| P1 | H-2: WebSocket Origin 检查 | 低（5行改动）|
| P1 | H-5: Legacy 路径 defer 保护 | 低（添加 defer）|
| P2 | H-3: pendingApproval channel 泄漏 | 中（需添加 context 取消清理）|
| P2 | H-4: seenEvents map 清理 | 低（添加定时 GC）|
| P2 | M-5: CachedItem 线程安全 | 中（需要评估性能影响）|
| P3 | 其余 Medium / Low | 按迭代规划 |

---

## 结论

代码整体质量高，架构设计合理。`safego` 的全面使用和 `ChatBridge` 接口设计值得称赞。主要关注点在 WebUI 安全层面（token 时序攻击、请求体无限制、WebSocket Origin 检查）和少数并发安全问题。建议按优先级逐一修复 Critical 和 High 级别问题。
