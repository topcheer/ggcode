# ggcode 代码质量审查报告

审查人：code-quality-reviewer  
审查日期：2026-05-10  
审查范围：`internal/agent/`, `internal/provider/`, `internal/webui/`, `internal/subagent/`, `internal/harness/`, `internal/mcp/`, `internal/session/`, `internal/im/`

---

## 一、错误处理（Error Handling）

### ✅ 优点
1. **`fmt.Errorf` + `%w` 包装一致性优秀**：整个代码库在 `internal/agent/` 中未发现 `%v` 代替 `%w` 的错误格式化，统一使用 `%w` 进行错误链包装（如 `agent_tool.go:197`, `agent_tool.go:203`, `agent.go:669`）。
2. **关键路径错误处理完善**：Agent loop 在每次迭代开始、工具执行前后都检查 `ctx.Err()`（`agent.go:401, 532, 577, 582`），能及时响应取消。
3. **`safeExecute` panic recovery**（`agent_tool.go:123-135`）：工具执行有 panic 恢复机制，避免单个工具的 bug 导致整个应用崩溃。
4. **Provider retry 逻辑完善**（`retry.go`）：区分可重试和不可重试错误（401/403/404 不重试，其余重试），支持 Retry-After header，指数退避有上限（30s），且尊重 context 取消。

### ⚠️ 问题

**P2-ERR-1: `webui/server.go` 多处忽略错误**
```go
// server.go:258 — HTTP Serve 错误被完全忽略
_ = http.Serve(ln, s.mux)

// server.go:330 — Write 错误被忽略
_, _ = w.Write(data)

// server.go:1754, 1760 — JSON encode 错误被忽略  
_ = enc.Encode(v)
_ = json.NewEncoder(w).Encode(...)
```
**建议**：HTTP handler 中的 Write 错误通常无法恢复，忽略是合理的。但 `http.Serve` 的错误应至少记录日志，因为 listener 关闭之外的错误可能表示真实问题。

**P2-ERR-2: `DaemonBridge` 吞掉 session 存储错误**
```go
// daemon_bridge.go:748, 767
_ = s.AppendMessage(b.sess, msg)
```
Session 持久化失败被静默忽略，可能导致用户会话数据丢失而无感知。
**建议**：至少记录 debug log。

**P2-ERR-3: `web_search.go` 缺少响应体大小限制**
```go
// web_search.go:96 — 直接 ReadAll 无大小限制
body, err := io.ReadAll(resp.Body)
```
对比 `web_fetch.go` 使用了 `io.LimitReader(resp.Body, maxResponseBodyBytes)`（10MB限制），`web_search.go` 缺少同样的保护。
**建议**：添加 `io.LimitReader` 限制，与 `web_fetch.go` 保持一致。

**P2-ERR-4: IM adapter 中多处 `io.ReadAll` 无大小限制**
- `feishu_adapter.go:438, 500, 910, 913, 1157, 1194` 等多处
- 这些是从第三方 API 读取响应体，正常情况下 API 会返回合理大小的响应，但无防御性限制。
**建议**：添加 `io.LimitReader` 限制（如 1MB）。

---

## 二、并发安全（Concurrency Safety）

### ✅ 优点
1. **`safego` 包设计优秀**：全局的 goroutine panic recovery 机制，带 PanicHook 支持，确保任何 goroutine 的 panic 不会导致进程崩溃。
2. **`DaemonBridge.SendUserMessage` TOCTOU-safe**（`daemon_bridge.go:1254-1278`）：在单个 `mu.Lock()` 中检查 `cancelFunc != nil` 并设置新值，避免了竞态条件。
3. **WebSocket 写保护**（`server.go:1318-1328`）：使用专用写 goroutine + buffered channel (64) 序列化 WebSocket 写操作，符合 gorilla/websocket 的线程安全要求。
4. **`JSONLStore` 互斥设计**（`session/store.go:77-83`）：明确注释了为什么需要全局互斥锁（O_APPEND 写入非原子性 >4KB），文档引用 `locks.md S3`。
5. **Sub-agent Manager 信号量**（`subagent/manager.go:219`）：使用 channel semaphore 控制并发，`rootCtx` 与 per-call ctx 分离设计合理。
6. **MCP Client `abortOnce`**：确保 Abort 只执行一次。

### ⚠️ 问题

**P1-CON-1: `Agent.Provider()` 使用写锁而非读锁**
```go
// agent.go:229-233
func (a *Agent) Provider() provider.Provider {
    a.mu.Lock()        // ← 写锁！
    defer a.mu.Unlock()
    return a.provider
}
```
这是一个只读操作，应该使用 `RLock/RUnlock`。对比同文件的 `ToolRegistry()` (line 237-239) 正确使用了 `RLock`。
**影响**：不必要地阻塞所有并发的读操作，降低吞吐量。

**P2-CON-2: `SubAgent` 的 `getStatus`/`setStatus` 使用 `Mutex` 而非 `RWMutex`**
```go
// subagent/manager.go:141-145
func (s *SubAgent) getStatus() Status {
    s.mu.Lock()         // 读操作用写锁
    defer s.mu.Unlock()
    return s.Status
}
```
**建议**：如果频繁读取状态，考虑使用 `RWMutex` 或 `atomic.Value`。

**P2-CON-3: `DaemonBridge.Subscribe` 取消订阅的索引脆弱**
```go
// daemon_bridge.go:1349-1356
return func() {
    b.eventSubMu.Lock()
    defer b.eventSubMu.Unlock()
    if b.eventSubs[idx] != nil {    // idx 是 append 时的位置
        close(b.eventSubs[idx].ch)
        <-b.eventSubs[idx].done
        b.eventSubs[idx] = nil
    }
}
```
使用 append 时的索引作为取消订阅标识，但 `b.eventSubs` 切片在 nil 化后不会压缩，导致无限增长。
**建议**：使用 map 或 linked list 替代，或在 unsubscribe 时清理 nil 条目。

**P2-CON-4: WebSocket 写 channel 满时静默丢消息**
```go
// server.go:1331-1336
send := func(msg interface{}) {
    select {
    case writeCh <- msg:
    default:
        debug.Log("webui", "ws write channel full, dropping message")
    }
}
```
当 channel 满时丢弃消息而非阻塞。对于 text_delta 等消息这是合理的（流式），但如果 tool_result 等关键消息被丢弃，用户可能丢失重要信息。
**建议**：考虑区分消息重要性，关键消息阻塞等待。

**P2-CON-5: `Wait` 函数忙轮询**
```go
// subagent/runner.go:197-217
func Wait(ctx context.Context, mgr *Manager, id string) (string, error) {
    ticker := time.NewTicker(100 * time.Millisecond)
    for {
        sa.mu.Lock()
        status := sa.Status
        ...
        sa.mu.Unlock()
        select {
        case <-ticker.C:
            continue
```
每 100ms 轮询一次状态，产生不必要的锁竞争。
**建议**：使用 `sync.Cond` 或 done channel 替代轮询。

---

## 三、资源管理（Resource Management）

### ✅ 优点
1. **Session 文件原子写入**（`session/store.go:308-336`）：使用 temp file + rename 模式，写入失败时清理临时文件，`f.Close()` 的错误也正确处理。
2. **Context 传播一致**：`Agent.Close()` 通过 `shutdownCancel()` 取消所有 in-flight 操作；`subagent.Manager.Shutdown()` 通过 `rootCancel` 取消所有子 agent。
3. **MCP Client Close 流程合理**（`mcp/client.go:229-263`）：先 Abort 传输层（释放阻塞的读），再关闭资源，有 3s 超时等待进程退出。
4. **`run_command` 输出限制**（`tool/run_command.go:20`）：`maxOutputSize = 100KB`，防止命令输出消耗过多内存。

### ⚠️ 问题

**P1-RES-1: MCP `readHeaderFramedMessage` 无 Content-Length 上限**
```go
// mcp/client.go:646
body := make([]byte, contentLength)
```
`contentLength` 直接从 MCP server 发来的 header 中解析，没有任何上限检查。恶意的 MCP server 可以声明巨大的 Content-Length 导致 OOM。
**建议**：添加 `if contentLength > maxMessageSize { return error }` 限制（如 10MB）。

**P2-RES-2: MCP Client Close 中进程清理不彻底**
```go
// mcp/client.go:257-261
select {
case <-done:
case <-time.After(3 * time.Second):
    // 超时后未 Kill 进程
}
```
3 秒超时后只是放弃等待，但没有 `cmd.Process.Kill()`。如果 MCP server 进程未自行退出，会变成僵尸进程。
**建议**：超时后调用 `cmd.Process.Kill()` + `cmd.Wait()`。

**P2-RES-3: `SubAgent.Mailbox` channel 无清理**
```go
// subagent/manager.go:260
Mailbox: make(chan AgentMessage, 16),
```
Sub-agent 完成后 Mailbox channel 不会被关闭或清理。如果有 goroutine 尝试向已完成 agent 的 Mailbox 发送消息，会永远阻塞。
**建议**：在 `Complete()` 中关闭 Mailbox channel。

**P2-RES-4: WebUI Server Close 不等待活跃连接**
```go
// server.go:272-277
func (s *Server) Close() error {
    if s.listener != nil {
        return s.listener.Close()
    }
    return nil
}
```
仅关闭 listener，不等待活跃的 HTTP/WebSocket 连接完成。可能导致 WebSocket 连接被强制断开。
**建议**：使用 `http.Server.Shutdown(ctx)` 优雅关闭。

**P2-RES-5: `web_fetch.go` 10MB 限制可能仍然过大**
对于 CLI agent 工具来说，10MB 的响应体可能消耗大量内存，特别是在 context window 紧张时。如果 agent 连续多次调用 web_fetch，可能累积大量内存。
**建议**：考虑将内容截断到合理大小后再传递给 LLM。

---

## 四、Go 惯用写法（Go Idioms）

### ✅ 优点
1. **Import alias 规范**：`ctxpkg` 别名避免与标准库 `context` 冲突，在 AGENTS.md 中有明确文档。
2. **接口定义小巧**：`ChatBridge`、`AgentRunner`、`AgentFactory` 等接口方法少，符合 Go 的"小接口"哲学。
3. **错误类型区分**：`errStreamInterruptedForReplan` 使用 `errors.New` 定义 sentinel error，通过 `errors.Is` 检查。
4. **Panic recovery 标准模式**：`safeExecute` 使用 named return value + defer recover 的标准模式。

### ⚠️ 问题

**P2-GO-1: `syncToolWorkingDir` 使用反射**
```go
// agent_tool.go:254-270
func syncToolWorkingDir(t tool.Tool, dir string) {
    v := reflect.ValueOf(t)
    ...
    f := v.FieldByName("WorkingDir")
    if f.IsValid() && f.CanSet() && f.Kind() == reflect.String {
        f.SetString(dir)
    }
}
```
使用反射来设置工具的 `WorkingDir` 字段，性能不佳且绕过了编译时类型检查。
**建议**：定义一个 `WorkingDirSetter` 接口，让需要的工具实现它：
```go
type WorkingDirSetter interface {
    SetWorkingDir(dir string)
}
```

**P2-GO-2: `webui/server.go` 大量 `map[string]interface{}`**
在 JSON 序列化场景中大量使用 `map[string]interface{}`（如 `streamEventToJSON`），而不是定义结构体。虽然在这些场景中 struct tag 的额外代码量不值得，但缺乏类型安全。
**建议**：对核心协议消息（如 WebSocket 协议）使用 struct 定义。

**P2-GO-3: `indexOf` 手工字符串搜索**
```go
// agent_tool.go:242-249
func indexOf(s, substr string) int {
    for i := 0; i <= len(s)-len(substr); i++ {
        if s[i:i+len(substr)] == substr {
            return i
        }
    }
    return -1
}
```
标准库 `strings.Index` 功能完全相同且更高效。
**建议**：替换为 `strings.Index(s, substr)`。

**P2-GO-4: `isJSON` 函数使用 `interface{}`**
```go
// agent.go:770-773
func isJSON(data json.RawMessage) bool {
    var v interface{}
    return json.Unmarshal(data, &v) == nil
}
```
可简化为 `json.Valid(data)`。

---

## 五、性能关注点（Performance）

### ✅ 优点
1. **`web_fetch.go` 使用 `io.LimitReader`**：限制响应体大小为 10MB，防止内存爆炸。
2. **`run_command` 输出限制 100KB**：防止命令输出占用过多内存。
3. **Sub-agent 事件限制 200 条**（`subagent/manager.go:32`）：`maxAgentEvents = 200`，超出时丢弃旧事件。
4. **Adaptive capacity**（`provider/adaptive_cap.go`）：有自适应 token 容量管理。
5. **Agent loop 多层 compact**：microcompact → precompact → reactive compact → force compact，分级处理避免不必要的 LLM 调用。

### ⚠️ 问题

**P1-PERF-1: `Agent.Messages()` 无锁返回共享状态**
```go
// agent.go:217-219
func (a *Agent) Messages() []provider.Message {
    return a.contextManager.Messages()
}
```
返回内部 slice 的引用，外部代码可以修改。`contextManager.Messages()` 可能在 agent loop 运行时被并发访问。
**建议**：确认 `contextManager.Messages()` 的实现是否返回副本或加锁保护。

**P2-PERF-2: `appendUserMessage` 中的 `len(b.sess.Messages)` 跟踪**
```go
// daemon_bridge.go:761-770
start := len(b.sess.Messages)
...
for i := start; i < len(messages); i++ {
    if s, ok := b.store.(*session.JSONLStore); ok {
        _ = s.AppendMessage(b.sess, messages[i])
    }
}
b.sess.Messages = messages
```
用 `len(b.sess.Messages)` 追踪上次保存位置，但 `b.sess.Messages` 可能被其他地方修改，导致重复或遗漏消息。
**建议**：使用显式的 `lastSaveIndex` 字段追踪。

**P2-PERF-3: `SubAgent.Wait` 轮询产生大量锁竞争**
如前述 P2-CON-5，100ms 轮询间隔在大量 sub-agent 场景下会显著影响性能。

**P2-PERF-4: `compactLocallyForSendBudget` 循环 truncation**
```go
// agent_compact.go:184-192
for tokens >= budget && cm.TruncateOldestGroupForRetry() {
    changed = true
    dropped++
    tokens = a.contextManager.TokenCount()
}
```
每次 truncation 后重新计算 token count，可能涉及对整个对话的重新计数。如果差距很大，循环次数多。
**建议**：TruncateOldestGroupForRetry 可返回新的 token count 避免重复计算。

---

## 六、总结评级

| 维度 | 评级 | 关键发现 |
|------|------|---------|
| 错误处理 | ⭐⭐⭐⭐☆ | 整体优秀，`%w` 使用一致，关键路径处理完善。少量错误静默忽略和 HTTP 响应体无限制问题。 |
| 并发安全 | ⭐⭐⭐⭐☆ | TOCTOU 保护到位，safego 优秀。`Provider()` 误用写锁、Subscribe 索引管理可改进。 |
| 资源管理 | ⭐⭐⭐☆☆ | Session 原子写入良好，Context 传播一致。MCP Content-Length 无上限、进程清理不彻底是主要风险。 |
| Go 惯用写法 | ⭐⭐⭐⭐☆ | 整体符合 Go 规范。反射设置 WorkingDir、手工 indexOf、少量 interface{} 使用可优化。 |
| 性能 | ⭐⭐⭐⭐☆ | 多级 compact 设计精巧。轮询等待、反射操作、无上限 buffer 是性能隐患。 |

### 最高优先级修复项（P1）

1. **MCP Content-Length 无上限**（P1-RES-1）— 安全风险，恶意 MCP server 可导致 OOM
2. **`Agent.Provider()` 使用写锁**（P1-CON-1）— 性能问题，容易修复
3. **`web_search.go` 无响应体限制**（P2-ERR-3）— 安全风险，缺少防御性编程
4. **`Agent.Messages()` 返回共享引用**（P1-PERF-1）— 数据竞争风险

### 整体评价

ggcode 项目代码质量整体优秀。错误处理遵循 Go 社区最佳实践，`%w` 错误包装一致性非常好。并发设计在关键路径（DaemonBridge TOCTOU、WebSocket 写保护、Sub-agent 信号量）上考虑周到。`safego` 包是一个很好的防御性编程实践。

主要改进方向集中在：(1) 资源限制的防御性编程（MCP Content-Length、HTTP 响应体大小）；(2) 细粒度锁使用（`Provider()` 应该用读锁）；(3) 轮询改为事件通知模式。
