# Tunnel/Relay 跨端一致性审查报告

**审查日期**: 2026-05-26
**审查范围**: Go 端 (`internal/tunnel/`) + Relay Server (`ggcode-relay/`) + Flutter 端 (`mobile/flutter/lib/core/`)

## Critical (3)

### XC-001 | 密钥派生不一致导致加密互操作失败
- **文件**: Go `internal/tunnel/crypto.go:20-35` / Flutter `crypto.dart:11-22`
- Token 长度 < 16 字节时，Go 用 Argon2id 派生，Flutter 用零填充 → AES 密钥完全不同。
- Token 长度 17-31（非 16/24）时，Go 直接传入 `aes.NewCipher` 会报错，Flutter 补零到 32。
- **修复**: 统一两端密钥派生逻辑。

### XC-002 | Flutter → Relay → Go: language_change/theme_change 数据丢失
- **文件**: Flutter `connection_service.dart:347-358` / Relay `main.go:283-322`
- Flutter 发送 `{"type":"language_change","language":"en"}`，`language` 字段不在 relay 的 `relayMessage` 结构体中，被 JSON 反序列化静默丢弃。Relay 转发 `Data: nil`。
- **修复**: Flutter 将数据放入 `data` 字段，或在 relay 结构体中添加字段。

### XC-003 | Relay `destroyRoom` 不清理全局事件表
- **文件**: Relay `store.go:320-347`
- `destroyRoom()` 只清理 `relay_events`/`relay_sessions`/`relay_rooms`，不清 `relay_global_events`/`relay_global_sessions`。
- **修复**: 在 destroyRoom 中增加全局表清理。

## High (4)

| ID | 文件 | 问题 |
|----|------|------|
| XC-004 | protocol.dart:4-41 | `WsMessage` 缺少 `resume_mode`/`last_event_id`/`client_id` 正式字段，通过 data 间接访问 |
| XC-005 | main.go:516-530, session_provider.dart:400-420 | `snapshot_required` 模式 replay 事件可能在 `session_info` 到来前直接应用，与语义矛盾 |
| XC-006 | main.go:382-391 | SQLite 持久化失败仅记录日志，客户端 resume 时 replay 不完整 |
| XC-007 | store.go:44, main.go:418-483 | `MaxOpenConns(1)` + `room.mu` 可能在未来并发场景死锁 |

## Medium (5)

| ID | 文件 | 问题 |
|----|------|------|
| XC-008 | connection_service.dart:282-284 | 解密错误静默丢弃，XC-001 导致的密钥不一致无法诊断 |
| XC-009 | protocol.go:53 | `EventDisconnected` 已定义但未使用（死代码） |
| XC-010 | main.go:517-520, session_provider.dart:375-419 | `snapshot_required` 三段消息（resume_miss → snapshot_reset → replay）时序依赖 `_queue` 链健壮性 |
| XC-011 | crypto.go:28-35, crypto.dart:14-22 | 16/24 字节密钥作为 AES-128/192 传入 `AesGcm.with256bits()` 可能运行时报错 |
| XC-012 | protocol.go:67 | `CmdModeChange` 已定义但 Go 未发送、Flutter 未处理 |

## Low (4)

| ID | 文件 | 问题 |
|----|------|------|
| XC-013 | main.go:490, connection_service.dart:177-286 | relay 的 `error` 类型明文消息 Flutter 不处理 |
| XC-014 | main.go:87-96 | `retry_after_ms`(60s) < grace period(90s)，客户端可能过早重连 |
| XC-015 | session_provider.dart:77-88 | event_id 排序依赖字符串比较和 `ev-%09d` 格式约定 |
| XC-016 | main.go:150-203 | `sendCh` "never closed" 依赖注释而非类型系统保证 |
