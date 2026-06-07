# Mobile Share 端到端逻辑深度 Review

**日期**: 2025-06-09
**范围**: Desktop Share 发起 → Relay 认证/密钥交换 → Mobile 连接 → 加密消息传输 → 控制消息 → 断线恢复

---

## 一、架构概览

```
Desktop (Wails)          Relay Server             Mobile Client
     │                        │                        │
     │  1. POST /share/session │                        │
     │ ───────────────────►   │                        │
     │  ◄───── issue ──────── │                        │
     │  (roomID, tickets,     │                        │
     │   key pair)            │                        │
     │                        │                        │
     │  2. WS /ws (server)    │                        │
     │ ═══════════════════►   │                        │
     │  ◄── connected ────── │                        │
     │                        │                        │
     │  3. encrypted events   │   扫码获得 ConnectURL   │
     │ ═══════════════════►   │ ◄──── QR code ──────── │
     │                        │                        │
     │                        │  4. WS /ws (client)    │
     │                        │ ◄═════════════════════ │
     │                        │ ──── connected ──────► │
     │  ◄── connected(client)│                        │
     │                        │                        │
     │  5. key_offer/kx_pub   │  key_offer ──────►    │
     │  ◄── key_accept ────── │ ◄──── key_accept ──── │
     │                        │                        │
     │                        │  6. resume_hello       │
     │                        │ ◄──────────────────── │
     │                        │ ── active_session ──► │
     │                        │ ── resume_ack ──────► │
     │                        │ ── replay events ──►  │
     │                        │                        │
     │  7. encrypted msgs     │                        │
     │ ═══════════════════►   │ ── forward ────────►  │
     │                        │                        │
     │                        │  8. encrypted cmds     │
     │                        │ ◄──────────────────── │
     │  ◄── forward ──────── │                        │
```

### 关键源文件

| 层 | 文件 | 职责 |
|---|---|---|
| 协议定义 | `internal/tunnel/protocol.go` | GatewayMessage, 所有 Data 类型, 命令常量 |
| Share 发证 | `internal/tunnel/share_protocol.go` | V3 ticket issuance, descriptor 分裂, URL 构建 |
| 加密 | `internal/tunnel/crypto.go` | AES-256-GCM 加解密 |
| 密钥交换 | `internal/tunnel/key_exchange.go` | X25519 ECDH + SHA256 派生 wrap key |
| Broker | `internal/tunnel/broker.go` | 事件录制, replay, snapshot, 文本批处理 |
| Relay Client | `internal/tunnel/relay_client.go` | Desktop ↔ Relay WebSocket, 加解密, key exchange 处理 |
| Session | `internal/tunnel/session.go` | Tunnel session 生命周期 (Start, Stop, RefreshInvite) |
| Reasoning | `internal/tunnel/reasoning.go` | Redacted thinking sentinel 处理 |
| Projection Store | `internal/tunnel/projection_store.go` | 本地事件持久化, replay 查询, authority epoch |
| Projection Hash | `internal/tunnel/projection_hash.go` | 事件流完整性校验 (SHA256) |
| Relay Server | `ggcode-relay/relay.go` | WebSocket hub, room 管理, peer 路由, resume/replay |
| Share Auth | `ggcode-relay/share_auth.go` | HMAC ticket 签发/验证, renew token |
| TunnelHost | `internal/agentruntime/tunnel_host.go` | 统一 stream 事件推送, projection/online broker 协调 |
| Share Actions | `internal/agentruntime/share_actions.go` | PublishShareState, StopSharedTunnelGracefully |
| Tunnel Attach | `internal/agentruntime/tunnel_attach.go` | AttachTunnelBroker 辅助 |
| Tunnel Commands | `internal/agentruntime/tunnel_commands.go` | RouteTunnelCommand, 命令路由 + hooks |
| Wails App | `desktop/ggcode-desktop-wails/app.go` | StartShare/StopShare, tunnel 状态管理 |
| Wails ChatBridge | `desktop/wailskit/chat.go` | BindShareCommands, PrepareShareBroker, 命令处理 |

---

## 二、协议版本与认证流程

### 2.1 Share Session Issuance (V3)

**Desktop -> Relay**: `POST /share/session?proto=3`

**Relay 返回**:
```json
{
  "protocol_version": 3,
  "share_mode": "v3",
  "room_id": "<random 32-hex-chars>",
  "server_auth_ticket": "<HMAC-signed ticket>",
  "client_auth_ticket": "<HMAC-signed ticket>",
  "server_renew_token": "<HMAC-signed renew token>",
  "auth_expires_at": "2025-06-08T12:00:00Z",
  "renew_expires_at": "2025-07-08T12:00:00Z"
}
```

**Ticket 格式**: `base64url(JSON{room_id, role, kind, exp, v}).base64url(HMAC-SHA256(payload))`

两种 ticket:
- `connect` ticket: 短 TTL (默认 15 分钟), 用于首次连接
- `renew` token: 长 TTL (默认 30 天), 仅 server 持有, 用于刷新 client ticket

**观察**:
1. **[Low] Renew token 只发给 server**: `refreshIssuedShareSession` 要求 `server_renew_token`, 只有 server 角色能刷新。设计是故意的 — server 端控制 room 生命周期。如果 server 断开, client 无法刷新自己的 ticket。
2. **[OK] Secret 通过环境变量 `GGCODE_SHARE_V2_SECRET` 配置**: 没有默认值, relay 启动时如果没有设置则 share 功能返回 503。

### 2.2 Desktop 连接 Relay (Server 角色)

Desktop 调 `Session.Start()` -> `requestIssuedShareSession()` -> 拿到 server descriptor -> 本地生成:
- `cryptoKey`: `randomHex(32)` = 64 hex chars = 32 bytes (AES-256 key)
- `serverPublicKey` / `serverPrivateKey`: X25519 ECDH key pair

**Descriptor 分裂** (`publicShareDescriptorFromServer`):
- **server** descriptor: 保留 `CryptoKey`, `ServerPrivateKey`, `AuthTicket`, `RenewToken`
- **client** descriptor (公开): 只有 `ServerPublicKey`, `AuthTicket` (client auth ticket)。V3 中 `CryptoKey` 和 `ServerPrivateKey` 被清除。

**构建 ConnectURL**:
```
wss://gateway.ggcode.dev/ws?
  role=client
  &proto=3
  &room_id=<roomID>
  &kx_pub=<serverPublicKey>
  &auth_ticket=<clientAuthTicket>
```

**V3 改进**: URL 中不含 `crypto_key`, 密钥只通过 ECDH key exchange 交换。

### 2.3 Mobile 连接 Relay (Client 角色)

Mobile 扫码拿到 ConnectURL, 向 Relay 发起 WebSocket 连接。

**Relay 的 `validateShareHandshake` 验证步骤**:
1. `role` 必须是 `server` 或 `client`
2. 不允许 `token` (legacy V1 参数)
3. `protocol_version` 必须 == 3
4. `room_id` 不能为空
5. `auth_ticket` 或 `renew_token` 必须有且只有一个
6. 验证 HMAC-SHA256 签名
7. 验证 expiry (connect ticket 15 min, renew token 30 day)
8. 检查 `tunnel_messages_v1` capability
9. Mint 新的 renew token 返回给连接方

**[Critical] CheckOrigin 全放行**: `upgrader = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}`。Round 6/7 Review 均标记为 Critical, 至今未修复。

---

## 三、加密体系

### 3.1 V3 Key Exchange (X25519 ECDH)

```
Desktop (Server)                        Mobile (Client)
     │                                       │
     │  Server 生成 X25519 key pair          │
     │  kx_pub 通过 ConnectURL 传给 client   │
     │                                       │
     │                      Client 生成 X25519 key pair
     │                                       │
     │  ◄──── key_offer ──────────────────── │
     │  {client_public_key}                  │
     │                                       │
     │  ECDH(server_private, client_public)  │
     │  = shared_secret                      │
     │                                       │
     │  wrapKey = SHA256(                    │
     │    "ggcode-share-v3" \0               │
     │    hex(shared_secret) \0              │
     │    roomID \0                          │
     │    clientID                           │
     │  )                                    │
     │                                       │
     │  AES-GCM(wrapKey, roomKey)            │
     │                                       │
     │  ───── key_accept ────────────────►   │
     │  {nonce, ciphertext}                  │
     │                                       │
     │                      AES-GCM-Open(wrapKey) = roomKey
     │                                       │
     │                      ──── key_ready ──────► (to relay)
```

**Desktop 端 `handleKeyOffer`** (`relay_client.go:726-770`):
1. 校验 clientID、RoomID、CryptoKey、ServerPrivateKey 非空
2. `wrapShareRoomKey(roomKey, roomID, clientID, serverPrivateHex, clientPublicHex)` -> (nonce, ciphertext)
3. 发送 `key_accept` 给 Relay, Relay 转发给对应 client

**[OK] 密钥派生绑定 roomID + clientID**: `deriveShareWrapKey` 把 shared_secret、roomID、clientID 混合 SHA256, 防止跨 room/client 重放攻击。

**[OK] Relay 零信任**: Relay 只做 key_offer/key_accept 透传, 不接触 roomKey 或 shared_secret。

### 3.2 AES-GCM 加密 (消息层)

所有消息通过 AES-256-GCM 加密传输:

```go
// 加密 (RelayClient.Send)
plaintext = JSON(GatewayMessage)
nonce, ciphertext = AES-GCM-Seal(cryptoKey, plaintext)
relayMsg = {type: "encrypted", nonce, ciphertext, session_id, event_id, ...}

// 解密 (RelayClient.readPump - "encrypted" case)
plaintext = AES-GCM-Open(cryptoKey, nonce, ciphertext)
msg = JSON(plaintext) => GatewayMessage
```

**Crypto key 派生** (`crypto.go`):
- 64 hex chars (32 bytes) -> 直接用作 AES-256-GCM key
- 如果 key < 16 bytes -> Argon2id 派生 32 bytes (固定 salt, 实际不会触发)
- 如果 key > 32 bytes -> 截断到 32 bytes

**[OK] 每次 Encrypt 生成新 nonce**: `crypto/rand.Read` 生成 12-byte nonce, GCM nonce 不重复。

**[Low] Argon2id 的 salt 是全零固定值**:
```go
var salt [16]byte  // 全零
derived := argon2.IDKey(key, salt[:], 1, 64*1024, 4, 32)
```
实际场景中 key 总是 `randomHex(32)` = 64 hex chars, Argon2 路径不会被执行, 风险极低。

---

## 四、控制消息流程

### 4.1 Server 连接 Relay

1. Desktop 通过 `RelayClient.Connect()` dial WebSocket
2. Relay 返回 `connected` 消息:
   ```json
   {
     "type": "connected",
     "session_id": "",
     "authority_epoch": 1,
     "count": 0,
     "last_event_id": "",
     "projection_hash": "",
     "data": {
       "protocol_version": 3,
       "share_mode": "v3",
       "connect_mode": "auth_ticket",
       "room_id": "...",
       "renew_token": "...",
       "renew_expires_at": "...",
       "kx_pub": "..."
     }
   }
   ```
3. `RelayClient.readPump` 解析为 `RelayConnectedState` -> 触发 `onConnected` 回调 -> `Broker.handleRelayConnected(info)`

**[OK] renew_token 在 connected 消息中返回**: Relay 每次 handshake 都 mint 新 renew_token, Desktop 通过 `updateShareDescriptor` 更新本地存储。

### 4.2 Client 连接 Relay

1. Mobile 扫码 -> 连接 Relay (client 角色)
2. Relay 验证: room 存在, server 已连接, serverReady=true
3. 如果条件不满足: 返回 `error` (room not found) 或 `server_offline` (recovery state)
4. 如果满足: 返回 `connected` 消息, 包含 history 状态

**Relay 通知 Server client 加入**:
```json
{
  "type": "connected",
  "role": "client",
  "session_id": "...",
  "authority_epoch": 1,
  "count": 5,
  "last_event_id": "ev-000000005",
  "projection_hash": "abc123...",
  "data": {
    "protocol_version": 3,
    "resume_complete": false
  }
}
```

**[OK] Server 的 handleRelayConnected 有完整的状态判断**:
- 如果 relay history 与 projection store 一致 (sessionID + authorityEpoch + projectionHash + lastEventID + count 全匹配) -> `trusted`, 只 bump nextEvent
- 如果不一致 -> 根据差异计算 `relayRecoveryPlan`: trusted / incremental replay / full reset+snapshot

### 4.3 Key Exchange 流程

Client 连接后的 key exchange:

1. **Client -> Relay -> Server**: `key_offer` with `{client_public_key: "hex"}`
2. **Server** `handleKeyOffer`: ECDH -> derive wrapKey -> AES-GCM wrap roomKey -> `key_accept`
3. **Relay -> Client**: `key_accept` with `{client_id, data: {nonce, ciphertext}}`
4. **Client** unwraps: ECDH -> same wrapKey -> AES-GCM-Open -> roomKey
5. **Client -> Relay**: `key_ready` (告诉 relay key exchange 完成)

### 4.4 Resume 流程

Client 在 key_ready 后或重连时发送 `resume_hello`:
```json
{
  "type": "resume_hello",
  "client_id": "mobile-xxx",
  "session_id": "optional",
  "last_event_id": "ev-000000010"
}
```

**Relay 的 `onResume`**:
1. 设置 `p.ready = false`, `p.waitingForKeyReady = true`
2. 发送 `active_session` 给 client
3. 等待 `key_ready`

**Relay 的 `finishResume`** (被 `onKeyReady` 触发):
1. `prepareResumeLocked`: 计算 replay events (cursor 之后的事件), 确定 mode (incremental/full_history)
2. 发送 `resume_ack`:
   ```json
   {
     "type": "resume_ack",
     "session_id": "...",
     "client_id": "mobile-xxx",
     "authority_epoch": 1,
     "data": {"resume_mode": "incremental", "replay_count": 3}
   }
   ```
3. 逐个发送 replay events (原始 encrypted wire bytes)
4. 通知 Server client 已连接 (触发 Broker 的 snapshot/replay 逻辑)

**[OK] resume 有 client replay coalescing**: `beginClientReplaySync` / `endClientReplaySync` 防止并发的 client 连接导致重复 snapshot。

### 4.5 active_session 控制消息

Server 发 `active_session` 告诉 Relay/Client 当前活跃的 session:
```json
{
  "type": "active_session",
  "session_id": "sess-abc123",
  "authority_epoch": 1,
  "data": {"session_id": "sess-abc123"}
}
```

**Relay 处理 `onActiveSession`**:
1. `bindRoomSession`: 如果 sessionID 变了或 authorityEpoch 变了, 清空 history
2. 转发给所有 clients
3. 持久化到 SQLite (异步)

### 4.6 server_ready 控制消息

Server 发 `server_ready` 表示准备就绪:
```json
{"type": "server_ready", "authority_epoch": 1}
```

Relay 设置 `room.serverReady = true`。Client 连接时检查这个 flag — 如果 server 还没 ready, client 被拒绝。

### 4.7 snapshot_reset 控制消息

当 Broker reset session 时发 `snapshot_reset`:
```json
{"type": "snapshot_reset", "session_id": "sess-xxx"}
```

**特点**: 不消耗 eventID (`enqueueControl` 不调 `nextEvent.Add`)。Relay 不持久化 eventID 为空的事件。

### 4.8 sharing_stopped 控制消息

Server 停止分享时, Relay 销毁 room:
1. Server 调 `StopSharingGracefully` -> `sess.DestroyGracefully`
2. Relay 收到 server disconnect -> `DestroyRoom` -> 通知所有 clients `sharing_stopped`
3. Clients 收到后清理本地状态

### 4.9 完整控制消息类型清单

| 消息类型 | 方向 | 用途 |
|---------|------|------|
| `connected` | Relay → Peer | 连接确认, 包含 room 状态 |
| `server_ready` | Server → Relay | Server 准备就绪 |
| `key_offer` | Client → Server | ECDH 公钥交换 |
| `key_accept` | Server → Client | 加密后的 roomKey |
| `key_ready` | Client → Relay | Key exchange 完成 |
| `active_session` | Server → Relay → Client | 当前活跃 session |
| `resume_hello` | Client → Relay | 请求恢复/重连 |
| `resume_ack` | Relay → Client | 确认恢复, 附带模式 |
| `snapshot_reset` | Server → Relay → Client | 重置历史, 全量同步 |
| `sharing_stopped` | Relay → Client | Server 已断开 |
| `relay_ack` | Relay → Client | 确认收到 client 消息 |
| `server_ack` | Server → Client | 确认处理完 client 消息 |
| `ping` / `pong` | 双向 | 心跳保活 |
| `error` | Relay → Peer | 错误通知 |
| `encrypted` | 双向 | 加密数据消息 (透传) |

---

## 五、加密消息 (数据消息)

### 5.1 Server → Client (事件流)

**GatewayMessage 结构**:
```json
{
  "session_id": "sess-xxx",
  "event_id": "ev-000000001",
  "stream_id": "msg-1",
  "authority_epoch": 1,
  "type": "text",
  "data": {"id": "msg-1", "chunk": "Hello world"}
}
```

**Wire 格式** (加密后):
```json
{
  "type": "encrypted",
  "session_id": "sess-xxx",
  "event_id": "ev-000000001",
  "stream_id": "msg-1",
  "authority_epoch": 1,
  "event_hash": "sha256hex...",
  "nonce": "base64...",
  "ciphertext": "base64..."
}
```

**event_hash**: `ProjectionEventHash(msg)` = SHA256(sessionID + eventID + type + data JSON)。用于 projection hash 计算, 验证事件流完整性。

### 5.2 Client → Server (命令)

Mobile 发送的加密命令:

| 命令 | 数据结构 | 用途 |
|------|---------|------|
| `message` / `user_text` | `MessageData{text, message_id}` | 用户消息 |
| `interrupt` | 无 | 中断 agent |
| `approval_response` | `ApprovalResponseData{id, decision}` | Tool approval 决策 |
| `ask_user_response` | `AskUserResponseData{id, answers, notes}` | AskUser 回答 |
| `language_change` | `LanguageChangeData{language}` | 切换语言 |
| `theme_change` | `ThemeChangeData{theme}` | 切换主题 |
| `pong` | 无 | 心跳回应 |

**消息确认机制 (双重 ACK)**:
1. Client 发 message 带 `message_id` (格式 `user-*`)
2. Relay 收到后回 `relay_ack` (确认 relay 已接收)
3. Server 处理完后回 `server_ack` (确认 agent 已处理)

### 5.3 事件类型清单 (Server → Client)

| 事件类型 | 数据结构 | 说明 |
|---------|---------|------|
| `text` | `{id, chunk}` | 流式文本 (300ms 批处理) |
| `text_done` | `{id}` | 文本流结束 |
| `reasoning` | `{id, chunk}` | 推理过程 (不批处理) |
| `reasoning_done` | `{id}` | 推理流结束 |
| `tool_call` | `{id, toolName, toolArgs, ...}` | 工具调用开始 |
| `tool_result` | `{id, toolName, content, isError, summary, payload}` | 工具调用结果 |
| `error` | `{message}` | 错误消息 |
| `user_message` | `{text}` | 用户消息回显 |
| `status` | `{status, message}` | Agent 状态 (idle/busy) |
| `activity` | `{activity}` | 当前活动 (processing/approval/ask_user) |
| `session_info` | `{workspace, model, provider, mode, version, language}` | Session 元数据 |
| `active_session` | `{session_id}` | 活跃 session 通知 |
| `snapshot_reset` | `{session_id}` | 重置历史 |
| `run_done` | 无 | Agent 运行完成 |
| `subagent_*` | 多种 | Sub-agent 状态 |
| `swarm_*` | 多种 | Swarm teammate 状态 |
| `approval_request` | `{id, toolName, toolArgs, ...}` | 请求 tool approval |
| `ask_user_request` | `{id, questions, title}` | 请求用户输入 |
| `system_message` | `{text}` | 系统消息 |
| `server_ack` | `{message_id}` | 确认处理 client 消息 |

### 5.4 文本批处理

Text 事件有 300ms 批处理优化:
1. `PushText(id, chunk)` -> 写入 `textBuf[msgID]`
2. `textFlushLoop` 每 300ms tick -> `flushAllText` 把所有 buf 合并发送
3. `PushTextDone(id)` -> 立即 flush + 发 `text_done`

**[OK] Reasoning 不批处理**: `PushReasoning` 直接 `enqueueWithStream`, 每 chunk 一条消息。

---

## 六、发现的问题

### 6.1 [Critical] Relay CheckOrigin 全放行

**文件**: `ggcode-relay/relay.go`
```go
upgrader = websocket.Upgrader{
    CheckOrigin: func(r *http.Request) bool { return true },
}
```

**影响**: 允许任何来源的 WebSocket 连接, 包括恶意网页。虽然 V3 有 HMAC ticket 认证, 但 CheckOrigin 放行会让 CSRF 攻击成为可能 — 恶意网页可以利用已认证的用户的浏览器发起 WebSocket 连接。

**建议**: 限制为已知域名 (如 `*.ggcode.dev`, `localhost`)。

### 6.2 [Medium] Relay client → server 转发缺少 sendMu 保护

**文件**: `ggcode-relay/relay.go`, `onEncrypted` 方法

```go
func (p *peer) onEncrypted(raw []byte, msg relayMessage) {
    if p.role == "server" {
        p.handleServerBroadcast(raw, msg)  // sendMu protected
    } else {
        p.room.mu.RLock()
        srv := p.room.server
        p.room.mu.RUnlock()
        if srv != nil {
            srv.sendRaw(raw)  // 没有 sendMu 保护
        }
    }
}
```

Client → Server 的 encrypted 转发没拿 `sendMu`。由于 `sendCh` 是带缓冲的 channel (cap=10000), 写入操作是原子的, 不会导致数据竞争。但消息顺序可能不一致 (server 广播和 client 消息可能交叉)。

**影响**: 低。Client → Server 和 Server 广播是不同方向的消息, 顺序不一致不会影响语义。

### 6.3 [Medium] Server 重连时 projection sync 阻塞所有事件

**文件**: `internal/tunnel/broker.go`, `handleRelayConnected`

```go
go func() {
    defer b.endProjectionSync()
    // ... 长时间运行的恢复逻辑
    b.flushAllText()
    b.sendActiveSession(currentSessionID)
    events := b.canonicalReplayEvents()
    b.replayCanonicalEvents(true, events)
    // ... fallback snapshot
}()
```

在 `projectionSyncing=true` 期间, 所有 `PushText`, `PushStatus` 等调用会阻塞在 `sync.Cond.Wait()` 上。如果 snapshot provider 响应慢, agent 的事件回调会全部阻塞。

**影响**: 中。正常场景下恢复很快 (< 100ms), 但 snapshot provider 异常时可能导致 agent 卡住。

**建议**: 考虑给 `waitProjectionSync` 加超时 (如 5s), 超时后丢弃 sync 状态继续。

### 6.4 [Low] Tool 结果截断到 2000 runes

**文件**: `internal/agentruntime/tunnel_host.go`

```go
case provider.StreamEventToolResult:
    content := ev.Result
    if len([]rune(content)) > 2000 {
        content = string([]rune(content)[:1997]) + "\n..."
    }
```

截断发生在写入 projection store 之前, 持久化的事件也是截断后的。Mobile 端收到截断的 tool result。

**影响**: 低。Tool result 主要是给 mobile 显示用的, 2000 runes 足够; agent 的实际 tool result 不受影响 (直接通过 agent loop 传回 LLM)。

### 6.5 [Low] Argon2id 固定 salt

**文件**: `internal/tunnel/crypto.go`

```go
var salt [16]byte  // 全零
derived := argon2.IDKey(key, salt[:], 1, 64*1024, 4, 32)
```

实际不会触发 (key 总是 32 bytes), 但如果有人传入短 key 会使用固定 salt。

**建议**: 加个 `//nosec` 注释或用 random salt。

### 6.6 [OK] V3 不泄露密钥到 URL

`publicShareDescriptorFromServer` 在 V3 时清除 `CryptoKey` 和 `ServerPrivateKey`。ConnectURL 只包含 `kx_pub` (server public key) 和 `auth_ticket`。

### 6.7 [OK] Reasoning sentinel 处理

`NormalizeReasoningChunk` 把 `__redacted_thinking__` 替换为 "Reasoning hidden by model.", 防止模型 redacted thinking 泄露到 mobile。

### 6.8 [OK] snapshot_reset 不消耗 eventID

`enqueueControl` 不调 `nextEvent.Add`, 确保 event ID 序列连续。

### 6.9 [OK] event_hash 提供完整性校验

`ProjectionEventHash` 用 SHA256 混合 sessionID + eventID + type + data, 用于 projection hash 计算。

---

## 七、安全评估

| 方面 | 状态 | 说明 |
|------|------|------|
| Transport | **OK** | WSS (TLS) + AES-256-GCM 双层加密 |
| Key Exchange | **OK** | X25519 ECDH, Forward Secrecy (每次 share 新 key pair) |
| Authentication | **OK** | HMAC-SHA256 ticket, role scoping, expiry |
| Key Isolation | **OK** | 每 room 独立 cryptoKey, 不跨 room 共享 |
| Replay Protection | **OK** | AES-GCM nonce 每次随机, HMAC ticket 有 expiry |
| Integrity | **OK** | event_hash (SHA256) 验证事件流完整性 |
| CheckOrigin | **CRITICAL** | Relay 全放行, 允许 CSRF |
| Secret Management | **OK** | V3 不泄露 cryptoKey 到 URL, 只通过 ECDH 交换 |
| Zero-Trust Relay | **OK** | Relay 不接触加密内容, 只做透传和存储 |

---

## 八、断线恢复机制

### 8.1 Server 重连 (Desktop 断网恢复)

1. `RelayClient.readPump` 检测连接断开
2. `reconnectLoop` 指数退避重连 (1s → 2s → 4s → ... → 30s max)
3. 重连后 Relay 返回 `connected` + 当前 room 状态
4. `Broker.handleRelayConnected` 比较 projection store:
   - **Trusted**: relay history 与 local 一致 → 只 bump nextEvent
   - **Incremental**: replay 缺失事件 → 发 `resume_ack` + replay
   - **Full reset**: 清空 history → 发 snapshot_reset + 全量 snapshot

### 8.2 Client 重连 (Mobile 断网恢复)

1. Mobile 检测连接断开
2. 重连后发 `resume_hello` + `last_event_id`
3. Relay 计算 cursor 之后的事件 → incremental replay
4. 如果 client 离线太久 (history 已被清空) → full replay

### 8.3 Graceful Shutdown

1. Desktop 调 `StopSharingGracefully(timeout=2s)`
2. Broker 发 `sharing_stopped` 给所有 clients
3. Relay 调 `DestroyRoom` 清理 room
4. 通知所有 clients `sharing_stopped`

---

## 九、整体评价

Share 协议 V3 设计完善:

1. **分层清晰**: Share Protocol (ticket) → WebSocket (transport) → Key Exchange (ECDH) → Encrypted Channel (AES-GCM) → Application (GatewayMessage)
2. **V1/V2 已废弃**: 只支持 V3, 旧版本直接返回 error
3. **Relay 零信任**: 不接触加密内容, 只做透传和历史存储
4. **恢复机制完善**: `relayRecoveryPlan` 有 trusted/incremental/full 三种模式
5. **Graceful shutdown**: 有序通知所有 participants
6. **Projection store**: 本地持久化事件, 支持离线 replay 和重连恢复

**唯一 Critical 问题**: Relay CheckOrigin 全放行, 建议尽快修复。
