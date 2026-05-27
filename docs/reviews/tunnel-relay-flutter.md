# Tunnel/Relay 全量审查报告 — Flutter 端

**审查日期**: 2026-05-26
**审查范围**: `mobile/flutter/lib/` 全部 Dart 文件

## Critical (6)

### FL-001 | StreamSubscription 泄漏
- **文件**: `session_provider.dart:203-250`
- `_connectImpl` 每次连接创建 4 个 `.listen()` 但返回的 subscription 未存储。dispose 时无法取消。
- **修复**: 存储为成员变量，在 `ref.onDispose` 和新连接前取消。

### FL-002 | `addUserMessage` 的 5 秒延迟回调不检查 `ref.mounted`
- **文件**: `session_provider.dart:1507-1521`
- `Future.delayed(5s)` 回调内直接访问 ref，provider 已 dispose 时会异常。
- **修复**: 回调开头加 `if (!ref.mounted) return;`。

### FL-003 | `subagent_complete` 的 5 秒延迟移除不检查 `ref.mounted`
- **文件**: `session_provider.dart:756-766`
- 同 FL-002。
- **修复**: 同上。

### FL-004 | 解密失败静默吞没错误
- **文件**: `connection_service.dart:262-284`
- `crypto.decryptData` 异常被 catch 块完全吞没，用户无感知，UI 停留在旧状态。
- **修复**: 通过 `errorController` 通知上层，加计数器连续失败后触发重连。

### FL-005 | `connect` TOCTOU 竞态
- **文件**: `session_provider.dart:131-147`
- 两个不同 URL 的并发 `connect()` 不会被去重，可能互相覆盖 service。
- **修复**: `_connectImpl` 开头检查 service 是否已被另一个并发调用创建。

### FL-006 | `_shouldApplyEvent` session 切换不清除 `_awaitingSnapshotProjection`
- **文件**: `session_provider.dart:961-1008`
- session 切换时清除了 UI projection 和 pending events，但没清 `_awaitingSnapshotProjection`，导致新 session 事件可能被 `_beginReplayRecovery` 的守卫跳过。
- **修复**: 在 session 切换逻辑中加 `_awaitingSnapshotProjection = false;`。

## High (9)

### FL-007 | 异步缓存恢复竞态
- **文件**: `session_provider.dart:1231-1248`
- 两个并发的 `_restoreSessionProjectionIfAvailable` 可能同时通过检查，第二个覆盖第一个。
- **修复**: `_restoreProjectionFromCache` 开头也检查 `_hasAuthoritativeProjection`。

### FL-008 | `disconnect()` 不清理 `_hasAuthoritativeProjection` 和 stream subscriptions
- **文件**: `session_provider.dart:272-277`
- 重连时旧的 `_hasAuthoritativeProjection=true` 可能阻止缓存恢复。
- **修复**: 在 `disconnect()` 中重置。

### FL-009 | `_drainBufferedReplayEvents` 递归调用可能导致栈溢出
- **文件**: `session_provider.dart:1047-1061`
- `_drainBufferedReplayEvents` → `_dispatchMessage` → `_markEventApplied` → `_drainBufferedReplayEvents` 形成同步递归。1000+ 连续事件可能栈溢出。
- **修复**: 改为迭代或 `scheduleMicrotask`。

### FL-010 | `_reconnectAfterReplayFailure` 无最大重试限制
- **文件**: `session_provider.dart:1151-1154`
- 连接-失败-replay-重连可能无限循环。
- **修复**: 添加全局重试计数器。

### FL-011 | `ConnectionService._queue` Future 链异常中断
- **文件**: `connection_service.dart:66, 104-108`
- `jsonDecode` 失败时 Future 链断裂，后续消息全部无法处理。
- **修复**: 加 `.catchError()` 或在 `_handleRelayMessage` 中包裹 try-catch。

### FL-012 | `dispose()` 后 stream controller 仍可能收到消息
- **文件**: `connection_service.dart:382-389`
- pending microtask 可能向已关闭的 controller 添加事件。
- **修复**: 所有 `add` 前检查 `_disposed`。

### FL-013 | `_loadResumeState` 状态覆盖
- **文件**: `session_provider.dart:175, 921-931`
- 两个并发 `_connectImpl` 的 `_loadResumeState` 互相覆盖。
- **修复**: resume state 存为局部变量。

### FL-014 | `WsMessage.fromJson` 缺 null 保护
- **文件**: `protocol.dart:37`
- `map['type'] as String` 对 null 值抛 TypeError。
- **修复**: `map['type'] as String? ?? ''`。

### FL-015 | `snapshotFor` 可能触发 Provider 重建循环
- **文件**: `session_provider.dart:3333-3348`
- 依赖链：widget → snapshotFor → state change → widget rebuild → snapshotFor。
- **修复**: 用 `ref.read` 而非 `ref.watch` 在非响应式场景。

## Medium (10)

| ID | 文件 | 问题 |
|----|------|------|
| FL-016 | session_provider.dart:203-254 | stream listeners 在 connect() 前就绑定，脆弱设计 |
| FL-017 | session_provider.dart:849-859 | `_clearUiProjection` 不清除 `_awaitingSnapshotProjection` |
| FL-018 | session_provider.dart:272-277 | `disconnect()` 不 dispose service，stream controllers 泄漏 |
| FL-019 | crypto.dart:14-22 | 短密钥零填充，密码学不安全 |
| FL-020 | session_provider.dart:1029-1032 | `_recentEventIds` LRU 1000 可能不够 |
| FL-021 | session_provider.dart:3357-3363 | 同步 SQLite 写大快照阻塞 UI isolate |
| FL-022 | session_provider.dart:182-196 | 信息项：`_clearUiProjection` 已重置，无需额外修改 |
| FL-023 | session_provider.dart:515-517 | 时间戳 fallback ID 可能与 replay 冲突 |
| FL-024 | connection_service.dart:303-313 | 心跳定时器在快速 connect/disconnect 中可能泄漏 |
| FL-025 | session_provider.dart:1565-1583 | `bindRemoteUserMessage` 用 text 匹配可能误匹配重复消息 |

## Low (10)

| ID | 文件 | 问题 |
|----|------|------|
| FL-026 | session_provider.dart:872-883,3581-3592 | 重复的 `_normalizeAgentStatus` 函数 |
| FL-027 | session_provider.dart:51-57 | `copyWith` 不支持清除 error |
| FL-028 | connection_service.dart:136-152 | 退避实际是线性的不是指数的 |
| FL-029 | protocol.dart:21-28 | `toJson` 序列化 null 值 |
| FL-030 | main.dart:55-65 | `addPostFrameCallback` 可能在 Provider 初始化前触发 |
| FL-031 | session_provider.dart:1390-1424 | `copyWith` 不支持修改 sourceId/toolId 等字段 |
| FL-032 | session_provider.dart:2561-2916 | 同步 SQLite 操作阻塞 UI |
| FL-033 | connection_service.dart:258-280 | encrypted case 缺少 `message_id` fallback |
| FL-034 | chat_screen.dart | 流式输出时滚动频繁触发抖动 |
| FL-035 | status_bar.dart:95-112 | 截断长度硬编码 80 字符 |
