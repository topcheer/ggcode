# Tunnel/Relay 全量审查报告 — Go 端

**审查日期**: 2026-05-26
**审查范围**: `internal/tunnel/`, `ggcode-relay/`, `internal/tui/model.go`, `internal/tui/tunnel.go`

## Critical (4)

### GO-001 | Broker `outbound` 队列无界，可导致 OOM
- **文件**: `internal/tunnel/broker.go:44, 1152-1157`
- `outbound []GatewayMessage` 是无限制增长的 slice。`enqueueOut` 永不阻塞，relay 长时间断连时所有事件持续堆积。
- **修复**: 增加上限（如 10000），超出时丢弃最旧或压缩。

### GO-002 | `senderLoop` 在 `outDone` 关闭时可能丢失未发送的消息
- **文件**: `internal/tunnel/broker.go:154-177`
- `Stop()` 调用时 outbound 中待发送消息被直接丢弃（非 graceful shutdown 路径）。
- **修复**: 退出前排空队列。

### GO-003 | Relay Client `enqueueRaw` 在 `sendCh` 满时阻塞调用方 goroutine
- **文件**: `internal/tunnel/relay_client.go:491-504`
- relay 断连时 `sendCh`（256）耗尽后，阻塞 `senderLoop`，可能连锁导致 projection sync 死锁。
- **修复**: 增加超时或非阻塞发送。

### GO-004 | `textFlushLoop` 与 `waitProjectionSync` 可能死锁
- **文件**: `internal/tunnel/broker.go:180-190, 1140-1146`
- `sync.Cond` 无超时。如果 `endProjectionSync` 因 panic 未执行，`textFlushLoop` 永久阻塞。
- **修复**: 用 context+channel 替代 `sync.Cond`，加超时。

## High (6)

### GO-005 | Relay `readPump` 持有 `room.mu` 时调用 `sendRaw`，可能长时间持锁
- **文件**: `ggcode-relay/main.go:341-381`
- `sendCh` 满（10000）时 `sendRaw` 阻塞，导致 `room.mu` 被长时间持有。
- **修复**: 在锁外发送。

### GO-006 | `senderLoop` 发送失败后无重试/暂停
- **文件**: `internal/tunnel/broker.go:170-173`
- 连续发送失败仅打日志，无 backoff，可能刷满日志。
- **修复**: 连续失败阈值后暂停。

### GO-007 | Relay server 无 graceful shutdown
- **文件**: `ggcode-relay/main.go:927-929`
- SIGTERM 直接终止，WebSocket 连接粗暴断开，SQLite 事务可能中断。
- **修复**: `signal.NotifyContext` + `srv.Shutdown`。

### GO-008 | `retry_after_ms`(60s) 与 grace period(90s) 窗口竞态
- **文件**: `ggcode-relay/main.go:664-694`
- 客户端在第 61-90 秒重连时 room 可能已过期但 server 未重连。
- **修复**: 确保 `retry_after_ms < grace period - margin`。

### GO-009 | SQLite 未设 `MaxIdleConns`/`ConnMaxLifetime`
- **文件**: `ggcode-relay/store.go:44`
- 长时间运行可能文件句柄问题，高频事务瓶颈。
- **修复**: 添加连接池配置。

### GO-010 | 短密钥使用固定零 salt 派生
- **文件**: `internal/tunnel/crypto.go:29-32`
- salt 全零，相同短 token 始终产生相同 AES 密钥。
- **修复**: 使用 token 本身作为 salt 的一部分。

## Medium (8)

| ID | 文件 | 问题 |
|----|------|------|
| GO-011 | broker.go:505-526 | 第二个 client 连接时被 `clientReplayInFlight` 跳过，可能收不到初始数据 |
| GO-012 | broker.go:1291-1313 | relay 重连后 event ID 可能跳跃 |
| GO-013 | relay_client.go:234-241 | `sendCh` 中旧消息在重连后可能已过期 |
| GO-014 | main.go:831 | peer sendCh 10000，slow consumer 阻塞 room 广播 |
| GO-015 | store.go:349-416 | `cleanupExpired` 大量 DELETE 可能长时间锁 SQLite |
| GO-016 | broker.go:943-995 | `SeedHistory` 通过公共 Push 方法触发不必要的 waitProjectionSync |
| GO-017 | relay_client.go:290-381 | readPump 未处理 `resume_ack`/`snapshot_reset` 明文消息 |
| GO-018 | main.go:150,207-234 | readPump+writePump 双重 Close（幂等但非文档保证） |

## Low (9)

| ID | 文件 | 问题 |
|----|------|------|
| GO-019 | broker.go:646-654 | activeText 在 agent 异常退出时不清理 |
| GO-020 | session.go:95-97 | QR code 生成错误被静默忽略 |
| GO-021 | store.go:16,418-426 | 默认 DB 路径 `/db` 在非容器环境可能不可写 |
| GO-022 | broker.go:997-1008 | `fallbackToolDisplayName` 空输入返回空字符串 |
| GO-023 | relay_client.go:524-558 | `CloseGracefully` timeout 后 `Close()` 可能丢失最后几条消息 |
| GO-024 | main.go:552-571 | `resumePlan` 线性扫描 history |
| GO-025 | broker.go:1214-1248 | `newMessage` 失败时静默丢弃事件 |
| GO-026 | main.go:786 | `CheckOrigin` 始终 true，CSRF 风险 |
| GO-027 | broker.go:1192-1211 | `sendWaiters` map 在 panic 时可能泄漏 |
