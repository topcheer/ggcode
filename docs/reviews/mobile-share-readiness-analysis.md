# 移动端 Share 消息流 Readiness 分析

**目标**: 找出移动端不能正常工作的根因 — 追踪完整消息流中缺失或时序错误的控制/应答消息。

---

## 端到端时序追踪 (V3 首次连接)

### A. Host 端启动 (Desktop/Wails 为例)

```
app.StartShare()
  ├─ sess := tunnel.NewSession(relayURL)
  ├─ info, err := sess.Start(ctx)                    ← 同步, 阻塞
  │    ├─ POST /share/session → roomID + tickets + key pair
  │    ├─ RelayClient.Connect()
  │    │    ├─ dial WebSocket                         ← 异步 goroutine 启动
  │    │    └─ go rc.run(conn)
  │    │         └─ readPump goroutine 开始读消息
  │    ├─ 注册 client.OnConnected → session.onConn (此时 == nil)
  │    ├─ 注册 client.OnMessage → session.onMsg
  │    └─ 返回 SessionInfo (含 QR/ConnectURL)
  │
  ├─ broker = tunnel.NewBroker(sess)
  │    ├─ sess.OnConnected → broker.handleRelayConnected   ← 覆盖 session.onConn
  │    ├─ sess.OnMessage → broker.onCommand                ← 覆盖 session.onMsg
  │    ├─ go broker.senderLoop()
  │    └─ go broker.textFlushLoop()
  │
  ├─ broker.OnRelayConnected(→ emit Wails event)
  ├─ chat.BindShareCommands(broker)                ← broker.OnCommand → RouteTunnelCommand
  ├─ chat.PrepareShareBroker(broker, snapshotFn)
  │    ├─ PublishShareState(broker, sessionID, snapshot, events, reset=true)
  │    │    └─ broker.SwitchSession(sessionID)
  │    │         ├─ broker.BindSession(sessionID)
  │    │         ├─ sendActiveSession(sessionID)    ← 发 "active_session" 到 relay
  │    │         ├─ resetProjectionAndEnqueue(true) ← 发 "snapshot_reset"
  │    │         └─ markRelayReady()                ← 发 "server_ready"
  │    ├─ broker.SetSnapshotProvider(snapshotFn)   ← ★ 设置 snapshot provider
  │    └─ chat.AttachTunnelBroker(broker)
  │         ├─ tunnelHost.AttachOnlineBroker(broker)
  │         ├─ tunnelHost.BindSession(currentSes, store)
  │         └─ tunnelHost.PrepareOnlineShare(broker)
  │              ├─ broker.SetEventRecorder(nil)
  │              ├─ broker.BindSession(ses.ID)     ← 第二次 BindSession (无变化,跳过)
  │              ├─ broker.SetReplayProvider(...)
  │              ├─ broker.SetAuthorityEpoch(epoch)
  │              ├─ broker.ReplayEvents(replay, false) ← 发 replay 事件
  │              └─ broker.AnnounceActiveSession(ses.ID)
  │                   ├─ sendActiveSession          ← 第二次 "active_session"
  │                   └─ markRelayReady             ← 第二次 "server_ready"
  │
  └─ return ShareInfo{ConnectURL, QRCode}  → 前端显示 QR
```

### B. Relay 端处理

```
Server WS 连接:
  1. 验证 auth_ticket → bind room → server = peer
  2. 发 "connected" → server (含 history 状态)
  3. room.serverReady = false

收到 server 的 "active_session":
  → bindRoomSession → 如 sessionID 变了则清空 history → 转发所有 clients

收到 server 的 "server_ready":
  → room.serverReady = true
  → 如有等待中的 client → 允许连接

Client 连接 (扫码):
  1. 验证 auth_ticket → 检查 room 存在 + server 在线 + serverReady
  2. 发 "connected" → client (含 history 状态)
  3. 发 "connected" (role=client) → server 通知
  4. 等待 client 的 key_offer
```

### C. Mobile 端连接 (Flutter)

```
connection_provider.connect(url)
  ├─ descriptor = ShareConnectionDescriptor.parse(url)
  │    → 从 URL 解析: proto, room_id, kx_pub, auth_ticket
  │
  ├─ _restoreProjectionFromCache()         ← 恢复本地缓存的 snapshot
  ├─ _beginRelaySyncWaiting()              ← relaySync = RelaySyncPhase.waiting
  ├─ _bindService(localService, generation, url)
  │    ├─ 监听 statusStream
  │    │    └─ on connected: sendResumeHello()  ← 发 "resume_hello"
  │    ├─ 监听 messageStream → _dispatchMessage
  │    ├─ 监听 ackStream
  │    └─ 监听 metadataStream
  │
  └─ localService.connect()
       ├─ WebSocket.connect(runtimeUrl)     ← 同步等待握手
       ├─ on message "connected":
       │    ├─ 更新 descriptor (renewToken, serverPublicKey)
       │    ├─ _statusController.add(connected) → 触发 statusStream listener
       │    │    → sendResumeHello()         ← 发 "resume_hello" (仅发一次)
       │    ├─ _metadataController.add(metadata)
       │    └─ _flushPendingResumeHello()   ← 已发过, 跳过
       │
       ├─ on message "active_session":
       │    ├─ if V3 && !_keyOfferSent: _beginKeyExchange()  ← ★ 关键!
       │    │    ├─ 生成 client X25519 key pair
       │    │    └─ send key_offer {client_public_key}
       │    └─ _messageController.add(active_session) → _dispatchMessage
       │         └─ _acceptAuthorityEpoch → _beginRelaySyncWaiting
       │
       ├─ on message "key_accept":
       │    ├─ ECDH(client_private, server_public) → shared_secret
       │    ├─ SHA256("ggcode-share-v3" + shared + roomID + clientID) → wrapKey
       │    ├─ AES-GCM-Open(wrapKey, nonce, ciphertext) → roomKey
       │    ├─ _crypto = TunnelCrypto(roomKey)
       │    └─ send key_ready
       │
       ├─ on "resume_ack":
       │    └─ _dispatchMessage:
       │         └─ _beginResumeReplaySync(replayCount)
       │              → relaySync = RelaySyncPhase.replaying
       │
       ├─ on "encrypted":
       │    ├─ if _crypto == null: 报错 "key exchange still pending" → return!
       │    └─ decrypt → _messageController.add(msg)
       │
       └─ 所有 data 消息经 _dispatchMessage:
            ├─ _shouldApplyEvent(msg)  ← 检查 dedup + ordinal
            │    ├─ authority epoch 检查
            │    ├─ sessionID 变化检查 → clearUiProjection
            │    └─ event ordinal > _lastAppliedEventId?
            ├─ 处理具体事件类型
            └─ _markEventApplied → 更新 _lastAppliedEventId, _recentEventSet
```

---

## 发现的 Bug

### [BUG-1] ★★★★ Critical: Mobile 收到 `active_session` 触发 key exchange, 但 relay 的 resume 流程也发 `active_session`, 造成双重 key exchange 触发

**Mobile `connection_service.dart:603`**:
```dart
case 'active_session':
  if (_descriptor.isV3 && !_keyOfferSent) {
    await _beginKeyExchange();  // 仅在 _keyOfferSent == false 时触发
  }
```

**Relay 的 `onResume` (`relay.go`)**: 在收到 client 的 `resume_hello` 后, relay 先发 `active_session` 给 client, 等待 `key_ready`:

```go
func (r *room) onResume(p *peer, ...) {
    // 1. 发 active_session 给 client
    r.sendActiveSessionToClient(...)
    // 2. 等待 key_ready
}
```

**但问题是**: Host 的 `PublishShareState` 已经发过 `active_session` 了。如果 relay 缓存了这个 `active_session`, 会在 client 连接时 replay 给 client。**Relay 同时在 `finishResume` 中又发一次 `active_session`**。

时序:
```
Host → Relay: active_session (PublishShareState)
Mobile → Relay: resume_hello
Relay → Mobile: active_session (onResume, 从 history replay)
Relay → Mobile: active_session (finishResume 中, 再次发)
```

Mobile 收到第一个 `active_session` → `_beginKeyExchange()` → 发 `key_offer`。
Mobile 收到第二个 `active_session` → `_keyOfferSent == true`, 跳过。OK, 这个是安全的。

**但真正的问题是**: 如果 Host 的 `active_session` 先到达 relay, relay 缓存在 history 中。Client 连接后 relay 做 Replay, 会把这个旧的 `active_session` 一起 replay。如果这个 `active_session` 的 sessionID 和当前不一致 (比如之前 session 的残留), Mobile 会清空 UI projection。

**结论**: 时序上可能 OK, 但存在风险。不是根因。

### [BUG-2] ★★★★ Critical: Host 端 `connected` 消息丢失 — `sess.Start()` 和 `NewBroker(sess)` 之间的竞态

**时序**:
```
Thread 1 (同步):
  sess.Start()
    → client.Connect() → go rc.run(conn)    // goroutine 启动
    → client.OnConnected(fn → s.onConn)      // 注册到 Session
    → return info                            // sess.Start() 返回

  // readPump goroutine 此时已经在跑
  // relay 立刻发 "connected"
  // readPump 处理 "connected" → client.onConnected → s.onConn
  // 但此时 s.onConn == nil (NewBroker 还没调用!)

  broker = NewBroker(sess)
    → sess.OnConnected(broker.handleRelayConnected)  // 太晚了!
```

**`session.go` 中的间接调用模式**:
```go
// Session.Start 中:
client.OnConnected(func(info RelayConnectedState) {
    s.mu.RLock()
    fn := s.onConn    // 如果 NewBroker 还没调, fn == nil
    s.mu.RUnlock()
    if fn != nil {    // 跳过! connected 消息丢失!
        fn(info)
    }
})
```

**后果**:
- `broker.handleRelayConnected` **永远不会被调用** (首次连接)
- `relayRecoveryPlan` 永远不执行
- broker 不知道 relay 的 history 状态 (projectionHash, lastEventId, count)
- 后续的所有事件推送可能和 relay history 冲突

**三个 Host 都受影响**: Desktop/Wails, TUI, Daemon — 都用相同的 `sess.Start()` → `NewBroker(sess)` 模式。

### [BUG-3] ★★★★ Critical: Mobile 端 `sessionReady` 永远不变为 true 的路径

**`connection_provider.dart:1715`**:
```dart
void _syncSessionReady() {
    final ready = state.status == ConnectionStatus.connected &&
        !_awaitingSnapshotProjection &&
        state.relaySync == null;   // ← relaySync 必须为 null!
    if (state.sessionReady != ready) {
      state = state.copyWith(sessionReady: ready);
    }
}
```

**`sessionReady = true` 的条件**:
1. `status == connected`
2. `!_awaitingSnapshotProjection`
3. `state.relaySync == null`

`relaySync` 在以下情况清空 (`_clearRelaySyncState`):
- `resume_ack` 处理后, `_beginResumeReplaySync` 中如果 `replayCount <= 0`
- `_noteReplayProgress` 中当 `_pendingReplayCount` 减到 0
- `_handlePermanentRoomFailure`
- `disconnect`
- `server_offline`

**但 `relaySync` 在 `active_session` 中被设为 waiting**:
```dart
case 'active_session':
    ...
    if (state.relaySync == null) {
      _beginRelaySyncWaiting(hasLocalState: _hasLocalSessionState());
    }
```

**问题**: 如果 Mobile 收到 `active_session` 但**永远收不到 `resume_ack`**, 则 `relaySync` 永远是 `waiting`, `sessionReady` 永远是 `false`。

**什么时候会收不到 `resume_ack`?**

看 relay 端:
```go
func (r *room) onResume(p *peer, ...) {
    p.ready = false
    p.waitingForKeyReady = true
    // 发 active_session 给 client
    // 等待 key_ready
}

func (r *room) onKeyReady(p *peer, ...) {
    r.finishResume(...)
    // 发 resume_ack + replay events
}
```

**如果 key exchange 失败** (例如 Host 没有正确处理 `key_offer`), 则 relay 永远不会收到 `key_ready`, `resume_ack` 永远不会发给 Mobile。

### [BUG-4] ★★★ Critical: Host 收不到 client 的 key_offer — relay 转发逻辑问题

**Relay 收到 `key_offer` 后的处理** (`relay.go`):
```go
case "key_offer":
    r.mu.Lock()
    srv := r.server
    r.mu.Unlock()
    if srv != nil {
        srv.sendRaw(raw)    // 转发给 server (Host)
    }
```

**Host 的 `RelayClient.readPump`** 处理消息:
```go
default:
    // 不识别的消息类型, 当成 encrypted 处理? 不, 直接送入 onMessage
```

让我检查 Host 是怎么处理 `key_offer` 的... 

**`relay_client.go` 中的 `readPump`**: 
- `case "connected"`: 处理
- `case "encrypted"`: 解密, 送 onMessage
- `case "relay_ack"`: 处理
- **其他类型**: 不处理, 不转发!

**`relay_client.go:handleKeyOffer`**: 
```go
func (rc *RelayClient) handleKeyOffer(msg relayMessage) {
    // 处理 key_offer
    // 发 key_accept 回去
}
```

但 `readPump` 中怎么路由到 `handleKeyOffer`? 让我看...

```go
case "key_offer":
    rc.handleKeyOffer(msg)
```

OK, `relay_client.go` 的 `readPump` 确实有 `case "key_offer"`, 会调 `handleKeyOffer`。这个路径是 OK 的。

### [BUG-5] ★★★★★ Critical: `connected` 消息丢失导致 `handleRelayConnected` 不执行 → `markRelayReady` 只走了 PublishShareState 的路径

回到 BUG-2。如果 `connected` 消息丢失:

`broker.handleRelayConnected` 不执行 → 没有 `relayRecoveryPlan` → broker 不知道 relay 的 history 状态。

但 `markRelayReady()` 在 `PublishShareState` → `SwitchSession` 中被调用了:
```go
func (b *Broker) SwitchSession(sessionID string) bool {
    ...
    sendActiveSession(sessionID)
    resetProjectionAndEnqueue(true)   // 发 snapshot_reset
    markRelayReady()                   // 发 server_ready
}
```

**然后 `PrepareOnlineShare` 又调了一次**:
```go
func (h *TunnelHost) PrepareOnlineShare(broker *Broker) {
    broker.ReplayEvents(replay, false)
    broker.AnnounceActiveSession(ses.ID)  // → sendActiveSession + markRelayReady
}
```

**`markRelayReady`**:
```go
func (b *Broker) markRelayReady() {
    if b == nil || b.session == nil { return }
    _ = b.session.SendServerReady(b.AuthorityEpoch())
}
```

它发 `server_ready` 到 relay。Relay 收到后设 `room.serverReady = true`, 允许 client 连接。

**所以 Host 端虽然有 connected 消息丢失的问题, 但 `server_ready` 通过 `PublishShareState` 已经发了。** Client 应该能连上 relay。

### [BUG-6] ★★★★★ Critical: Mobile 端 `resume_hello` 发送时序 — 可能在 key exchange 之前或之后

**Mobile 连接后的事件顺序**:

```
Mobile → Relay: WS handshake
Relay → Mobile: "connected" (status=connected)
Mobile: statusStream listener → sendResumeHello() → 发 "resume_hello"
Relay: 收到 resume_hello
  → onResume:
    1. 发 "active_session" 给 client (从 relay 的 room state)
    2. 等待 "key_ready"

Mobile: 收到 "active_session"
  → 如果 V3 && !_keyOfferSent:
    → _beginKeyExchange() → 发 "key_offer"
  → _messageController.add → _dispatchMessage("active_session")

Relay: 收到 "key_offer"
  → 转发给 server (Host)

Host: 收到 "key_offer"
  → handleKeyOffer → ECDH + wrap roomKey → 发 "key_accept"

Relay: 收到 "key_accept"
  → 转发给 client

Mobile: 收到 "key_accept"
  → _handleKeyAccept → unwrap roomKey → _crypto ready
  → 发 "key_ready"

Relay: 收到 "key_ready"
  → onKeyReady → finishResume
    → 发 "resume_ack" + replay events

Mobile: 收到 "resume_ack"
  → _beginResumeReplaySync(replayCount)
  → 处理 replay events
  → _noteReplayProgress → 计数减到 0 → _clearRelaySyncState
  → _syncSessionReady() → sessionReady = true ✓
```

**这个流程看起来是正确的!** 但是...

### [BUG-7] ★★★★★ Critical: Relay 的 `onResume` 可能不触发 — 因为 `resume_hello` 在 `connected` 之前被发送

看 Mobile 端:

```dart
// connection_provider.dart:1351
localService.statusStream.listen((status) {
    ...
    if (status == ConnectionStatus.connected) {
      localService.sendResumeHello(...)   // 发 resume_hello
    }
});
```

但 `ConnectionService` 在处理 relay 的 "connected" 消息时:
```dart
// connection_service.dart:589
case 'connected':
    ...
    _statusController.add(ConnectionStatus.connected);  // 触发 statusStream
    _flushPendingResumeHello();                          // 也发 resume_hello
```

**Mobile 发了两次 resume_hello?**

看 `_flushPendingResumeHello`:
```dart
void _flushPendingResumeHello() {
    if (_resumeHelloSent || ...) return;   // 已经发过则跳过
    sendResumeHello(...);
}
```

和 `statusStream` listener:
```dart
if (status == ConnectionStatus.connected) {
    localService.sendResumeHello(...)      // 调的是 sendResumeHello
}
```

`sendResumeHello`:
```dart
void sendResumeHello({...}) {
    if (_resumeHelloSent) return;   // 只发一次
    _resumeHelloSent = true;
    ...
}
```

OK, 只有一个会成功 (因为 `_resumeHelloSent` flag)。不会重复发。

**但顺序是什么?** `_statusController.add(connected)` 是同步的, 会触发 statusStream listener, listener 中调 `sendResumeHello`, 设 `_resumeHelloSent = true`。然后 `_flushPendingResumeHello` 检测到已发, 跳过。

所以 `resume_hello` 是在 `_handleRelayMessage` 处理 "connected" 时**同步发出**的。

### [BUG-8] ★★★★★ Critical — 这才是根因: Relay 在 `onResume` 中发 `active_session`, 但这个 `active_session` 可能**先于** Host 通过 PublishShareState 发的 `active_session` 到达 relay

**时序竞争**:

```
Thread 1 (Host):
  sess.Start() → relay 发 "connected" → readPump goroutine 运行
  NewBroker(sess) → PublishShareState → SwitchSession → sendActiveSession → 发 "active_session"
  (此时 Host 还没收到 relay 的 "connected" 回复, 因为 onConn 可能是 nil — BUG-2)

Thread 2 (Relay readPump):
  处理 relay "connected" → handleRelayConnected → relayRecoveryPlan

Mobile:
  扫码 → 连接 relay → 发 resume_hello

Relay:
  收到 resume_hello
  → 检查 room serverReady
```

**问题**: `serverReady` 什么时候变成 true?

Host 发 `server_ready` 是在 `markRelayReady()` 中, 这个在 `PublishShareState` → `SwitchSession` 中调用。

如果 Mobile 扫码**非常快** (比如从历史记录中恢复), 在 Host 的 `PublishShareState` 执行之前就连上了 relay:
- `room.serverReady = false`
- relay 拒绝 client 连接! → 返回 "server_offline" 或 "error"

**Mobile 会收到 `server_offline`**:
```dart
case 'server_offline':
    ...
    _beginRelaySyncWaitingForHost(recoveryState: ..., hasLocalState: ...)
    state = state.copyWith(status: ConnectionStatus.connecting, sessionReady: false)
```

然后 Mobile 等 60 秒重连。如果 60 秒内 Host 完成了 `PublishShareState`, 下次重连就 OK。

但如果 Host 也在 60 秒内重新连接了呢? 又一轮竞态...

### [BUG-9] ★★★★ Critical: Host `PublishShareState` 发的事件可能还没到 relay, Mobile 就已经连上了

即使 `serverReady = true`, Host 发的事件序列:
```
active_session → snapshot_reset → server_ready → replay events → active_session(2) → server_ready(2)
```

这些事件通过 `senderLoop` → `RelayClient.Send` → 加密 → 发到 relay。

如果 Mobile 在 Host 发完 `active_session` 但还没发 `snapshot_reset` 之前就连上了:
- relay 的 history 可能只有部分事件
- relay 给 Mobile 做 resume, replay 的事件不完整
- Mobile 收到的状态不完整

### [BUG-10] ★★★★★ Critical: `session_info` 事件缺失 — Mobile 不知道 workspace/model 信息

**Mobile `_dispatchMessage` 中**:
```dart
case 'session_info':
    ...
    ref.read(sessionInfoProvider.notifier).set(data);
    ...
    _markProjectionAuthoritative();
    if (_pendingReplayCount == 0) {
      _clearRelaySyncState();   // ← 只有这里或 _noteReplayProgress 减到 0 才清 relaySync
    }
```

**如果 Host 从不发 `session_info` 事件** (或者发了但被 relay 丢弃), Mobile 的 `sessionInfoProvider` 永远是 null。

**Host 什么时候发 `session_info`?**

在 `PublishShareState` → `broker.SwitchSession` 中:
```go
func (b *Broker) SwitchSession(sessionID string) bool {
    ...
    resetProjectionAndEnqueue(true)   // 发 snapshot_reset
    markRelayReady()                   // 发 server_ready
}
```

**不发 `session_info`!** `session_info` 是通过 snapshot 中的 `SessionInfo` 字段发的。

看 `SendSnapshot`:
```go
func (b *Broker) SendSnapshot(snap BrokerSnapshot) {
    if snap.SessionInfo != nil {
        b.PushSessionInfo(*snap.SessionInfo)    // 发 session_info
    }
    ...
}
```

但在 `PublishShareState` 中:
```go
func PublishShareState(broker *tunnel.Broker, sessionID string, snapshot BrokerSnapshot, events []GatewayMessage, reset bool) bool {
    if len(events) > 0 {
        broker.SwitchSession(sessionID)    // 不发 session_info
        broker.ReplayEvents(events, false) // 不发 session_info
        return false
    }
    broker.SwitchSession(sessionID)        // 不发 session_info
    broker.SendSnapshot(snapshot)          // 发 session_info (如果有)
    return false
}
```

**如果有 replay events (len(events) > 0)**, 走第一个分支, `SendSnapshot` 不被调用, `session_info` 不发!

**TUI 的 `bootstrapTunnelShare`**:
```go
if len(events) == 0 {
    broker.SendSnapshot(m.tunnelSnapshot())    // 发 snapshot (含 session_info)
} else {
    m.ensureProjectionBootstrap(broker, events)  // 不发 snapshot!
}
```

**Daemon 的 `PrepareBroker`**:
```go
agentruntime.PublishShareState(broker, sessionID, c.Snapshot(), replay, true)
```
如果有 replay, `SendSnapshot` 不调用, `session_info` 丢失!

**Desktop/Wails 的 `PrepareShareBroker`**:
```go
agentruntime.PublishShareState(broker, sessionID, snapshot, nil, true)
```
传了 `nil` 作为 events, 所以走 `SendSnapshot` 分支, `session_info` 会发。OK。

但然后 `PrepareOnlineShare`:
```go
events := store.Replay(sessionID)
broker.ReplayEvents(events, false)
broker.AnnounceActiveSession(ses.ID)
```
如果 `PrepareOnlineShare` 有 replay events, 这些 events 中可能包含 `session_info`, 也可能不包含。

### [BUG-11] ★★★★★ Critical — 最可能的根因: `session_info` 不在 replay events 中

**Host 端 `tunnel_host.go` 的 `PrepareOnlineShare`**:
```go
func (h *TunnelHost) PrepareOnlineShare(broker *Broker) {
    events, _ := h.store.Replay(h.currentSessionID)
    broker.ReplayEvents(events, false)
    broker.AnnounceActiveSession(h.currentSessionID)
}
```

`store.Replay` 返回的是 projection store 中存储的所有事件。`session_info` 事件是否被记录到 projection store?

**`tunnel_host.go` 中 `PushStreamEvent`**:
```go
func (h *TunnelHost) PushStreamEvent(ev provider.StreamEvent) {
    switch ev.Type {
    case provider.StreamEventSessionInfo:
        if h.broker != nil {
            data := ...
            h.broker.PushSessionInfo(data)
        }
        // 不走 event recorder! session_info 不会写入 projection store!
    ...
    }
}
```

**关键**: `PushSessionInfo` 调的是 `enqueueWithStream`, 它会触发 event recorder:
```go
func (b *Broker) PushSessionInfo(data SessionInfoData) {
    ...
    b.enqueueWithStream("session_info", ..., true, true)  // record=true!
}
```

OK, `session_info` 会被记录到 projection store。那 `store.Replay` 会返回它。

**但如果 `PushSessionInfo` 在 `AttachOnlineBroker` 之前被调用** (即 agent 在 share 启动前就发了 session_info), 那这个事件通过 event recorder 写入了 projection store, 但同时 online broker 还没 attach, 事件不会推到 relay。

然后 `PrepareOnlineShare` → `ReplayEvents` 会把这个 `session_info` 发出去。OK, 这条路径是通的。

### [BUG-12] ★★★★★ Critical — 真正的根因: Host 的 `connected` 消息丢失导致 relay recovery 不执行, Host 和 relay 状态不一致

回到 BUG-2, 这是整个链条的关键断裂点。

如果 `broker.handleRelayConnected` 没有被调用:

1. broker 不知道 relay 的 `projectionHash`、`lastEventId`、`count`
2. broker 在 `PublishShareState` 中发了 `snapshot_reset` + replay events + `active_session` + `server_ready`
3. relay 收到这些事件, 存入 history
4. 但 relay 同时也在等待 server 的 `connected` 回复中的信息来建立 room state

等等, relay 不等 server 的回复 — relay 在 server 连接时**立刻**发 `connected` 给 server, server 不需要回复。

所以 relay 的状态是:
- server 连接 → `connected` → room 创建, server attached
- 收到 server 的 `active_session` → bindRoomSession (可能清空 history)
- 收到 server 的 `server_ready` → serverReady = true
- 收到 server 的 encrypted events → 存入 history, 转发给 clients

这个流程不依赖 server 是否处理了 relay 的 `connected` 消息!

**但问题是**: 如果 server 处理了 `connected`, 会执行 `relayRecoveryPlan`, 可能会发现 relay 的 history 和 server 的 projection store 不一致, 触发额外的 snapshot/replay。如果 `connected` 丢失, 这步不执行, 就不会做额外的恢复。

**在首次连接场景中**, relay 的 history 是空的 (新 room), server 的 projection store 也是空的或刚初始化的。所以 history 一致, 不需要额外恢复。`connected` 丢失的影响较小。

**在重连场景中** (server 断网重连), relay 的 history 可能有旧数据, server 的 projection store 也有数据。如果不做 `relayRecoveryPlan`, 两边状态可能不一致。

---

## 综合结论

### 最可能导致移动端不工作的根因

#### 1. [★★★★★] Host 端 `connected` 消息丢失 (BUG-2)

**影响**: 重连时 server 和 relay 状态不一致, relay replay 给 mobile 的数据可能过时或缺失。

**修复**: 在 `NewBroker` 中检查 relay client 是否已有缓存的状态, 并 replay。

#### 2. [★★★★★] `session_info` 可能不在 replay events 中 (BUG-10)

**影响**: Mobile 的 `sessionInfoProvider` 为 null, `relaySync` 可能永远不清除 (取决于 replay_count), UI 不显示 workspace/model 信息。

**具体场景**: TUI 和 Daemon 走 replay events 分支 (有 tunnel events), 不走 `SendSnapshot` 分支。如果 replay events 中没有 `session_info` 事件 (因为 session_info 是在 share 启动前发的, 只存在于 projection store 中):

**TUI `handleTunnelShareBootstrapMsg`**:
```go
if events := m.currentSessionTunnelReplayEvents(); len(events) > 0 {
    m.tunnelBroker.ReplayEvents(events, false)   // replay tunnel ledger events
}
// ...
if !replayed {
    m.tunnelBroker.SendSnapshot(m.tunnelSnapshot())  // 只有没 replay 才发 snapshot
}
```

如果 tunnel ledger 有 events, `replayed = true`, 不调 `SendSnapshot`。如果 ledger 中没有 `session_info`, mobile 收不到。

**修复**: 在 `handleTunnelShareBootstrapMsg` 中, 即使 replayed=true, 也要确保发 `session_info`。

#### 3. [★★★★] Mobile `sessionReady` 依赖 `relaySync == null` — 如果 resume 流程不完整, 永远不 ready (BUG-3)

**影响**: Mobile UI 永远显示 loading/connecting 状态。

**具体场景**: 如果 relay 的 `resume_ack` 中 `replay_count` 和实际发的事件数不匹配 (例如有些事件被 relay 过滤了), `_pendingReplayCount` 永远减不到 0, `relaySync` 永远不清除。

或者: 如果 `snapshot_reset` 到达, `_beginSnapshotSync()` 设 relaySync=snapshot, 但后续的 snapshot 事件中不包含足够的事件来清除 snapshot sync。

#### 4. [★★★★] `snapshot_reset` → `_awaitingSnapshotProjection = true` → 只在 `session_info` 时才清除 (BUG-3 补充)

```dart
case 'snapshot_reset':
    ...
    _awaitingSnapshotProjection = true;   // 设为 true
    _beginSnapshotSync();
    ...

case 'session_info':
    ...
    if (_awaitingSnapshotProjection) {
      _awaitingSnapshotProjection = false;  // 只有这里清除!
    }
    if (_pendingReplayCount == 0) {
      _clearRelaySyncState();  // 清除 relaySync
    }
```

**如果 Host 发了 `snapshot_reset` 但不发 `session_info`** (BUG-10), 则:
- `_awaitingSnapshotProjection` 永远 true
- `_syncSessionReady`: `!_awaitingSnapshotProjection` 为 false
- `sessionReady` 永远 false

**Mobile UI 永远卡在 loading!**

---

## 修复建议

### Fix 1: 解决 Host `connected` 消息丢失

在 `Session.Start()` 中缓存 `connected` 状态:

```go
type Session struct {
    ...
    lastConnectedState *RelayConnectedState
}

func (s *Session) OnConnected(fn func(RelayConnectedState)) {
    s.mu.Lock()
    s.onConn = fn
    cached := s.lastConnectedState  // 如果之前收到过
    s.mu.Unlock()
    if cached != nil && fn != nil {
        go fn(*cached)  // replay
    }
}
```

或者在 `RelayClient` 中缓存:
```go
client.OnConnected(func(info RelayConnectedState) {
    s.mu.Lock()
    s.lastConnected = &info  // 缓存
    fn := s.onConn
    s.mu.Unlock()
    if fn != nil { fn(info) }
})
```

### Fix 2: 确保所有 Host 都发 `session_info`

在 `PublishShareState` 中, 即使走 replay events 分支, 也要在最后确保发一次 `session_info`:

```go
func PublishShareState(broker *tunnel.Broker, sessionID string, snapshot BrokerSnapshot, events []GatewayMessage, reset bool) bool {
    if len(events) > 0 {
        broker.SwitchSession(sessionID)
        broker.ReplayEvents(events, false)
    } else {
        broker.SwitchSession(sessionID)
        broker.SendSnapshot(snapshot)
    }
    // ★ 始终确保 session_info 被发送
    if snapshot.SessionInfo != nil {
        broker.PushSessionInfo(*snapshot.SessionInfo)
    }
    return false
}
```

或者在 TUI/Daemon 的 bootstrap 完成后, 显式发一次 `PushSessionInfo`。

### Fix 3: Mobile 端 `sessionReady` 增加超时兜底

如果 `_awaitingSnapshotProjection` 持续超过 N 秒, 强制清除:

```dart
_awaitingSnapshotProjectionTimer = Timer(Duration(seconds: 10), () {
    if (_awaitingSnapshotProjection) {
        _awaitingSnapshotProjection = false;
        _syncSessionReady();
    }
});
```

### Fix 4: Mobile 端对 `snapshot_reset` 不严格要求 `session_info` 才能清除

```dart
case 'snapshot_reset':
    ...
    _awaitingSnapshotProjection = true;
    _beginSnapshotSync();
    // 增加超时保护
    _snapshotProjectionTimer?.cancel();
    _snapshotProjectionTimer = Timer(Duration(seconds: 15), () {
        if (_awaitingSnapshotProjection) {
            _awaitingSnapshotProjection = false;
            _syncSessionReady();
        }
    });
```

---

## 影响矩阵

| Bug | Desktop/Wails | TUI | Daemon | 严重程度 |
|-----|:---:|:---:|:---:|:---:|
| BUG-2: connected 丢失 | ✓ | ✓ | ✓ | ★★★★★ |
| BUG-10: session_info 缺失 | 部分* | ✓ | ✓ | ★★★★★ |
| BUG-3: sessionReady 永远 false | Mobile | Mobile | Mobile | ★★★★ |
| BUG-6: resume_hello 时序 | Mobile | Mobile | Mobile | ★★★ |

*Desktop/Wails 传 nil events, 走 SendSnapshot 分支, session_info 会发。但 PrepareOnlineShare 可能有 replay events 覆盖。
