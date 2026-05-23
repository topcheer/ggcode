# ggcode 全量代码评审报告

**评审日期**: 2025-05-23
**评审范围**: `internal/` 全部 334 个 Go 源文件（~101k LOC 非测试，~69k LOC 测试）
**评审方式**: 6 个并行 sub-agent 分模块评审

---

## 统计概览

| 严重程度 | 数量 | 说明 |
|---------|------|------|
| Critical | 13 | 安全漏洞、panic 风险、数据竞态 |
| High | 24 | 并发安全、缺失防护、测试空白 |
| Medium | 33 | 性能、代码重复、锁设计 |
| Low | 30 | 风格、小优化、文档 |

---

## 一、Config / WebUI / Tool / Permission / Plugin / Hooks

### Critical

**C-1: WebUI WebSocket 并发写入竞态**
- 文件: `internal/webui/`
- WebSocket 使用 per-connection write goroutine（buffered channel of 256），但 `ChatBridge` 接口的 `DaemonBridge.SendUserMessage` 在 claim run slot 时存在 TOCTOU — 检查 `activeCancel != nil` 和设置 `cancelFunc` 之间释放了锁。

### High

**H-1: Permission sandbox 路径遍历**
- 需确认 `allowed_dirs` 检查是否规范化路径后再比较（如 symlink 解析、`..` 处理）。

### Medium

**M-1: Config `Save()` 写入非原子**
- `Save()` 末尾的 `MigratePlaintextAPIKeys()` 会覆盖 `stripDefaultsFromYAML` 的 compact 格式，需要 `recompactConfigFile()` 修复。

---

## 二、Agent 核心 / Provider 适配器

### Critical

**C-1: `saveAdaptiveCaps` 锁序耦合脆弱**
- 文件: `internal/provider/adaptive_cap.go`, 行 227-250
- `OnTruncated()` 和 `OnRejected()` 在持有 `c.mu` 后调用 `saveAdaptiveCaps()` 获取 `capRegistryMu`。虽然当前不会死锁（`AdaptiveCapFor` 不获取 cap.mu），但锁序耦合脆弱，未来改动可能引入 AB-BA 死锁。

**C-2: `chatNoRetry` 裸类型断言 — 可 panic**
- 文件: `internal/provider/context_probe.go`, 行 397-398
- `p.(prober)` 如果传入的 Provider 未实现 `prober` 接口会 panic。公开 API 不应做此假设。
- **修复**: 使用 `ok` 模式安全断言，失败返回错误。

**C-3: Agent `contextManager` 并发保护缺失**
- 文件: `internal/agent/agent.go`, 行 382-839
- `RunStreamWithContent` 大量调用 `contextManager` 在 `a.mu` 锁外。`SetContextManager()` 在 `a.mu` 保护下替换整个 manager 引用，可能导致对旧 manager 的操作结果被丢弃。

### High

**H-1: 重试次数过多 (`providerRetryAttempts = 20`)**
- 文件: `internal/provider/retry.go`, 行 18
- 20 次重试 + 指数退避最长约 10 分钟，交互式场景用户会以为卡死。

**H-2: `isRetryable` 字符串匹配过于宽松**
- 文件: `internal/provider/retry.go`, 行 144-151
- `strings.Contains(msg, "400")` 会匹配路径中含 "400" 的消息。

**H-3: `probeLoaded` 标志竞态条件**
- 文件: `internal/provider/context_probe.go`, 行 46, 99-102
- `probeLoaded` 无锁读写，多 goroutine 可能多次调用 `loadProbeCache()`。
- **修复**: 改用 `sync.Once`。

**H-4: Anthropic `Chat()` 缺少重试**
- 文件: `internal/provider/anthropic.go`, 行 90-115
- OpenAI 和 Gemini 的 Chat 都用 `retryWithBackoffCtx`，Anthropic 没有。

### Medium

**M-1: `Agent.Provider()` 使用写锁而非读锁**
- 文件: `internal/agent/agent.go`, 行 238-242
- 只读操作应用 `RLock`。

**M-2: `OpenAI convertMessages` 工具结果转换代码重复**
- 文件: `internal/provider/openai.go`, 行 536-573 和 666-700
- 约 60 行重复代码，应提取为辅助函数。

**M-3: `RunStreamWithContent` 函数复杂度过高**
- 文件: `internal/agent/agent.go`, 行 387-656
- ~270 行，3 层嵌套循环，应拆分为独立方法。

**M-4: `adaptiveCap` 的 `OnRejected` 可能设 `cur` 为 1**
- 文件: `internal/provider/adaptive_cap.go`, 行 95-132
- 一次 rejection 就能把 cap 降到 1 token。应设合理最小值。

**M-5: `tryTierProbe` 构建巨大 padding 字符串**
- 文件: `internal/provider/context_probe.go`, 行 442
- `strings.Repeat("a ", tier)` 对 1M tier 产生 ~2MB 字符串。

### Low

**L-1: `estimateTokensForMessages` 对 `ImageData` 未计数**
**L-2: `minInt` 可由内置 `min` 替代**
**L-3: 多个 provider 的 `CountTokens` 使用相同粗略估算**

---

## 三、TUI / Chat 渲染 / Markdown

### Critical

**C-1: Tunnel OnCommand 回调在非 Bubble Tea goroutine 直接访问 Model 状态**
- 文件: `internal/tui/tunnel.go`, 行 165-166
- `OnCommand` 回调运行在 broker goroutine，但 `handleTunnelClientCommand` 直接读取 `m.program`、`m.tunnelBroker` 等字段，与 Update 循环形成无锁并发读写。

**C-2: `pushTunnelEvent` 在流回调 goroutine 访问 Model 字段**
- 文件: `internal/tui/tunnel.go`, 行 190-228; `internal/tui/submit.go`, 行 267-275
- 从 `RunStreamWithContent` 的流回调访问 `m.tunnelBroker`、`m.tunnelMsgID` 等字段。
- **修复**: 流回调应仅通过 `m.program.Send()` 转发消息，所有状态修改集中在 Update 中。

### High

**H-1: `commands_slash_admin.go` ~300 行函数，45+ 处硬编码英文**
- 文件: `internal/tui/commands_slash_admin.go`
- 所有 knight/config 命令的帮助文本、错误提示均未通过 i18n 系统。

**H-2: `tunnel.go` 所有用户消息硬编码英文（~10 处）**

**H-3: `stream_panel.go` 硬编码英文消息**

**H-4: Model 结构体 100+ 字段，无分组封装**
- 文件: `internal/tui/model.go`
- 20+ 个 IM 面板直接作为 Model 字段，`isAnyPanelOpen()` 使用 20+ nil 检查的 switch。
- **建议**: 抽象为 `Panel` 接口 + 注册模式。

**H-5: `handleKeyPress` 面板分发 O(n) 线性扫描**
- 文件: `internal/tui/update_keys.go`, 行 14-578
- 25+ 个连续 `if m.xxxPanel != nil` 判断。

**H-6: `View()` 中面板重复渲染**
- 文件: `internal/tui/view.go`
- 先渲染面板测量高度，再在 `conversationPanelHeight()` 中重新渲染，同一帧 2x 开销。

### Medium

**M-1: `chat.List.Render()` 使用写锁阻塞 View()**
- 文件: `internal/chat/list.go`, 行 197-265
- Render 是只读操作，应用 RLock。

**M-2: `commands_harness.go` 943 行巨型文件**
- 嵌套 switch-case 深度达 4 层，应按子命令拆分。

**M-3: `renderStatusBar` 字符串缩减 O(n^2)**
- 文件: `internal/tui/view_status.go`, 行 76-84
- 逐字符缩减并逐次调用 `lipgloss.Width()`。

**M-4: `renderApprovalOptions` 和 `renderLanguageOptions` 完全重复**
- 文件: `internal/tui/view_chat.go`, 行 89-113

### Low

**L-1: `brewing` 标签内联语言判断而非使用 i18n 系统**
**L-2: `inspector_panel.go` 中 `x`/`X` 键空 case（可能遗漏删除功能）**
**L-3: `chat/tools.go` 行 430-431 注释重复**
**L-4: `BashToolItem` renderCronBody 使用分散的 i18n 实现**

---

## 四、IM 网关 / Daemon

### Critical

**C-1: IMEmitter goroutine 永不退出，生命周期泄漏**
- 文件: `internal/im/emitter.go`, 行 51-65
- 消费者 goroutine（`range s.ch`）无退出机制，channel 从不关闭。
- **修复**: 添加 `Close()` 方法关闭 channel，或使用 `context.CancelFunc`。

**C-2: DaemonBridge.SubmitInboundMessage TOCTOU 竞态**
- 文件: `internal/im/daemon_bridge.go`, 行 270-282
- 检查 `activeCancel != nil` 和设置 `cancelFunc` 之间存在时间窗口。Slack/Discord 适配器的 WebSocket 消息可在不同 goroutine 并发到达。
- **修复**: 合并到同一个 Lock/Unlock 区间。

### High

**H-1: DingTalk 使用裸 `go` 而非 `safego.Go`**
- 文件: `internal/im/dingtalk_adapter.go`, 行 146, 169
- 其他所有适配器都用 `safego.Go`，DingTalk 的 `run()` 或 `tokenRefresher()` panic 会崩溃整个进程。

**H-2: DingTalk 使用 `http.DefaultClient`，无超时控制**
- 文件: `internal/im/dingtalk_adapter.go`, 行 496, 560, 656, 704
- 其他适配器都用带超时的自定义 client，DingTalk 可能永久阻塞。

**H-3: QQ `seen` map 仅写入时清理，无后台 GC**
- 文件: `internal/im/qq_adapter.go`, 行 1295-1305
- 长时间无消息时旧条目不清理，群聊场景可能持续增长。

**H-4: `internal/daemon/` 完全没有单元测试（~1,050 行源码）**

**H-5: Emitter 在 `context.Background()` 执行 Emit，无超时**
- 文件: `internal/im/emitter.go`, 行 56-58
- 如果适配器 `Send()` 阻塞，goroutine 永久挂起。

### Medium

**M-1: Feishu `_ = err` 死代码**
- 文件: `internal/im/feishu_adapter.go`, 行 115-120
- 无效 `webhook_port` 被静默忽略。

**M-2: 适配器间 backoff 策略不统一**
- DingTalk 最大 30s，Feishu 不重试，其他统一 60s。

**M-3: Discord WebSocket 写入无 mutex 保护**
- 文件: `internal/im/discord_adapter.go`, 行 170
- DingTalk 正确使用了 `writeMu`，Discord 没有。

**M-4: follow display `hiddenTools` map 每次调用重新创建**
- 文件: `internal/daemon/follow.go`, 行 247-277
- 应提升为包级别变量。

**M-5: `newPairingCode()` 偏向低位数字**
- 文件: `internal/im/runtime.go`, 行 1063-1069
- `uint16 % 10000` 导致 0-5535 出现概率略高。

### Low

**L-1: `formatSearchCount` 正则每次重新编译**
**L-2: `compactSingleLine` O(n^2) 实现**
**L-3: daemon `follow.go` 格式化和业务逻辑混合（824 行）**

---

## 五、Harness / Swarm / Subagent

### Critical

**C-1: swarm `Shutdown` 锁嵌套 + 与 `CancelAll` 策略不一致**
- 文件: `internal/swarm/manager.go`, 行 86-97 vs 311-330
- `Shutdown` 持有 `m.mu` 锁 `team.mu`，`CancelAll` 释放 `m.mu` 再逐个锁。锁序未来可能不一致导致死锁。

**C-2: swarm `emit()` 中 `m.mu` 非原子读写**
- 文件: `internal/swarm/manager.go`, 行 477-501
- 两次加锁之间 `Shutdown` 可能清除 `m.teams`，`onUpdate` 回调操作失效状态。
- **修复**: 合并为单次加锁。

**C-3: harness `SaveTask` 全局互斥锁 — 性能瓶颈**
- 文件: `internal/harness/task.go`, 行 18-19
- 所有任务保存共享一个 `sync.Mutex`，worker 每 200ms 更新会阻塞其他操作。

**C-4: subagent `Wait` 使用 100ms 忙等待轮询**
- 文件: `internal/subagent/runner.go`, 行 210-241
- 长时间运行时持续浪费 CPU 和锁竞争。
- **修复**: 改用 `sync.Cond` 或完成 channel。

### High

**H-1: swarm `ShutdownTeammate` TOCTOU — 释放 `m.mu` 后 teammate 可能被删除**
- 文件: `internal/swarm/manager.go`, 行 278-310

**H-2: swarm `idle_runner` 不做 panic recovery**
- 文件: `internal/swarm/idle_runner.go`
- `RunStream` panic 后 teammate 永久停在 Working 状态。

**H-3: harness `loadRecentEventsFromJSONL` 全量加载到内存**
- 文件: `internal/harness/events.go`, 行 307-360
- 长期运行的项目事件日志可能非常大。

**H-4: subagent `notifyUpdate` 节流逻辑不精确**
- 文件: `internal/subagent/manager.go`, 行 650-665
- 被丢弃的通知也更新 `lastNotify`，缩短了有效窗口。

### Medium

**M-1: swarm `results` map 无限增长**
- 文件: `internal/swarm/manager.go`, 行 39
- `Shutdown` 不清理 results，长期运行积累所有 teammate 结果。

**M-2: subagent `Statuses()` 持有 `m.mu` 再锁 `sa.mu`**
- 文件: `internal/subagent/manager.go`, 行 375-383
- 锁嵌套脆弱，应先收集引用释放 `m.mu` 再逐个获取。

**M-3: harness `ExecuteTask` 错误后不清理 worktree**
- 文件: `internal/harness/run.go`, 行 257-277

**M-4: harness `EnqueueTask` 未校验 `DependsOn` 引用有效性**
- 文件: `internal/harness/task.go`, 行 92-112
- 引用不存在的任务会永久 blocked。

**M-5: swarm `CreateTeam` ID 生成在锁外**
- 文件: `internal/swarm/manager.go`, 行 104-121

### Low

**L-1: harness `marshalSnapshotJSON` 无用包装函数**
**L-2: harness `truncateWorkerText` 可能破坏 UTF-8**
**L-3: swarm `BroadcastToTeam` 不报告投递失败**
**L-4: swarm teammate 系统提示硬编码英文**

---

## 六、MCP / A2A / Auth

### Critical

**C-1: JWT 验证绕过 — issuer/audience 检查失败后静默降级**
- 文件: `internal/auth/a2a_oauth.go`, 行 438-453
- 严格校验失败后自动降级为不验证 iss/aud 的解析。攻击者可签发 iss/aud 不匹配但签名正确的 JWT。
- **修复**: 移除降级逻辑，iss 不匹配直接拒绝。

**C-2: HMAC 签名使用公开的 `clientID` 作为密钥**
- 文件: `internal/auth/a2a_oauth.go`, 行 428-431
- `client_id` 在 Agent Card 中公开可见，攻击者可伪造 JWT。
- **修复**: 添加单独的 `hmacSecret` 配置字段，或禁用 HMAC 只允许 RS/ES。

**C-3: Token introspection 端点未认证**
- 文件: `internal/auth/a2a_oauth.go`, 行 651-656
- RFC 7662 要求客户端认证，当前实现无认证信息，生产 IdP 会拒绝。

### High

**H-1: `http.DefaultClient` 全局使用 — 无超时**
- 文件: `internal/auth/` 多处（copilot.go, a2a_oauth.go, claude_oauth.go）
- 多处 `http.DefaultClient.Do(req)` 无超时限制。

**H-2: auth `Store` 文件写入无互斥锁保护**
- 文件: `internal/auth/store.go`, 行 140-155
- 并发 `Save()` 存在 TOCTOU：两个 goroutine 读相同数据各自修改后覆盖。

**H-3: Push Notification URL 注入（SSRF）**
- 文件: `internal/a2a/server.go`, 行 603-616
- `cfg.URL` 完全由客户端控制，可指向 `169.254.169.254` 等内网地址。
- **修复**: 验证 URL scheme 为 `https://`，拒绝私有 IP。

**H-4: MCP OAuth debug 日志泄露 token**
- 文件: `internal/mcp/oauth.go`, 行 623
- Token exchange 完整响应体（含 access_token）写入 debug 日志。

**H-5: PKCE 回调服务器未验证请求来源**
- 文件: `internal/auth/a2a_oauth.go`, 行 93-119
- 虽有 `state` 保护，但建议添加 Origin/Referer 检查。

### Medium

**M-1: JWKS 刷新持锁时间过长**
- 文件: `internal/auth/a2a_oauth.go`, 行 473-494
- HTTP 请求在 `sync.Mutex` 内执行，慢服务器阻塞所有 JWT 验证。

**M-2: Copilot RefreshToken 设为与 AccessToken 相同**
- 文件: `internal/auth/copilot.go`, 行 182-188
- 无 refresh token 时应留空。

**M-3: A2A Client bearerToken/refreshToken 无并发保护**
- 文件: `internal/a2a/client.go`, 行 26-38
- 多 goroutine 并发使用同一 client 时数据竞态。

**M-4: TokenCache 文件名截断到 12 字符可能碰撞**
- 文件: `internal/auth/a2a_token_cache.go`, 行 130-139

**M-5: MCP Client `sendRequest` 全局互斥锁阻塞并发**
- 文件: `internal/mcp/client.go`, 行 299-335
- 每个 MCP 请求持有全局锁包括整个 HTTP 周期。

**M-6: A2A Server 缺少 CORS 头**
- 文件: `internal/a2a/server.go`, 行 178-183
- Agent Card 端点应允许跨域。

**M-7: Device Flow `interval` 无上限保护**
- 文件: `internal/auth/a2a_oauth.go`, 行 274-277

### Low

**L-1: MCP Client `env` 注入可能覆盖 PATH 等敏感变量**
**L-2: A2A Server `json.NewEncoder(w).Encode()` 忽略错误**
**L-3: TokenCache.Load() 注释与行为不符**
**L-4: `stringsJoin` 手动实现，性能 O(n^2)**
**L-5: `openBrowser` 缺少 URL scheme 校验**

---

## 最高优先级修复建议（Top 10）

| # | 问题 | 模块 | 影响 |
|---|------|------|------|
| 1 | JWT 验证降级绕过 | Auth | 安全 — 伪造 JWT 可通过认证 |
| 2 | HMAC 用 clientID 做密钥 | Auth | 安全 — 公开信息可伪造 token |
| 3 | Push Notification SSRF | A2A | 安全 — 可探测内网 |
| 4 | Tunnel/流回调竞态 | TUI | 稳定性 — 生产环境可能 panic |
| 5 | IMEmitter goroutine 泄漏 | IM | 资源 — 重建 Manager 时泄漏 |
| 6 | DaemonBridge TOCTOU | IM | 稳定性 — 并发 IM 消息竞态 |
| 7 | DingTalk 裸 goroutine + 无超时 HTTP | IM | 稳定性 — panic/挂起整个进程 |
| 8 | swarm emit() 非原子锁操作 | Swarm | 稳定性 — 并发状态不一致 |
| 9 | Anthropic Chat 缺少重试 | Provider | 可靠性 — 瞬时错误不重试 |
| 10 | probeLoaded 竞态（改用 sync.Once） | Provider | 稳定性 — 多次加载缓存 |

---

## 架构亮点

1. **模块化良好**: 每个子系统有清晰边界，接口设计合理（Provider、Tool、ChatBridge、Runner 等）
2. **取消传播完整**: ctrl+c 通过 `CancelAll` 级联到 subagent 和 swarm teammates
3. **原子写入**: harness 和 config 关键数据使用 write-to-temp + rename
4. **事件溯源**: harness 的 JSONL 事件日志 + 快照双存储
5. **测试覆盖**: 大部分核心模块有充分测试，包含并发和竞态测试
6. **safego 模式**: 全局统一的 goroutine panic 保护（除 DingTalk）

## 测试覆盖空白

| 模块 | 缺失测试 |
|------|---------|
| `internal/daemon/` | 完全没有单元测试 |
| `internal/im/bindings.go` | 0 测试 |
| `internal/im/pairing.go` | 0 测试 |
| `internal/im/emitter.go` | 0 单元测试 |
| `internal/provider/context_probe.go` | 分层探测无单元测试 |
| `internal/tui/commands_slash_admin.go` | 无独立测试文件 |
| `internal/a2a/authenticate()` | 各种认证组合无单元测试 |
