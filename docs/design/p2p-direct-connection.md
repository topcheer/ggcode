# P2P Direct Connection

## Overview

Host (Go: CLI + Desktop) 与 Mobile (Flutter) 之间的消息传输支持 WebRTC P2P
DataChannel 直连。Relay 服务器保留用于信令交换和 TURN 中继回退。

**设计目标：**
- P2P 优先：移动端连接后自动尝试 P2P 升级
- 零消息丢失：P2P 失败时无缝回退到 relay，不丢任何消息
- 协议不变：`GatewayMessage` JSON 协议、session/event ID 排序、replay 语义不变
- 桌面端通用：CLI 和 Wails 桌面端共用同一套 `internal/` 代码，P2P 同样生效

## Architecture

```
                    ┌──────────────────────┐
                    │   Relay Server       │
                    │ (Railway deployment) │
                    │  signaling + TURN    │
                    └──┬───────────────┬───┘
          SDP/ICE sig  │               │  TURN relay (fallback)
                ┌──────┴──┐       ┌────┴──────┐
                │  Host   │       │           │
                │  (Go)   │═══════┤ P2P DC    │
                │ pion/v4 │ P2P   │ (direct)  │
                └─────────┘       └───────────┘
                                      │
                               ┌──────┴──────┐
                               │ Mobile (FL) │
                               │flutter_webrtc│
                               └─────────────┘

Host roles: CLI (ggcode serve) | Desktop (Wails)
Host P2P role: offerer (creates PeerConnection + SDP offer)
Mobile P2P role: answerer (receives offer, creates answer)
```

### 消息流向

```
Mobile connects → Relay WebSocket (WSS) → Key exchange
                                         ↓
                              P2P negotiation (offer/answer via relay signaling)
                                         ↓
                              ┌─── ICE connected? ───┐
                              │                      │
                           Yes                      No
                              │                      │
                     DataChannel opens         Stay on relay
                     Switch to P2P transport   TriggerReplayNow()
                     SyncP2PReplay()           (replay all projection events)
                     (send incremental gap)
                              │
                     All messages via P2P
                     (relay kept for signaling)
```

## Key Components

### 1. Transport Interface (`internal/tunnel/transport.go`)

WebSocket relay 和 WebRTC DataChannel 都实现此接口：

```go
type Transport interface {
    Send(data []byte) error
    OnMessage(handler func(data []byte))
    OnDisconnect(handler func())
    Close() error
    IsConnected() bool
}
```

Broker 通过 `p2pTransport` 字段管理当前活跃传输层。当 P2P transport 存在时，
所有出站消息走 DataChannel；不存在时走 relay WebSocket。

### 2. Broker (`internal/tunnel/broker.go`)

核心传输切换逻辑：

- **`p2pTransport`** — 当前 P2P transport（nil = 使用 relay）
- **`p2pNegotiating`** — atomic.Bool，P2P 协商进行中标志
- **`relayHistoryCount` / `relayLastEventID`** — 移动端连接时 relay 的历史状态快照

**关键方法：**

```go
// P2P 优先检查：协商中或已激活时跳过 relay recovery replay
func (b *Broker) p2pUpgradePending() bool

// P2P 建立后：通过 DataChannel 补发移动端缺失的增量消息
// 使用 relayLastEventID 在 projection events 中查找后缀
func (b *Broker) SyncP2PReplay()

// P2P 失败后：立即通过 relay 重放所有 projection events
// 移动端按 EventID 去重，不会收到重复消息
func (b *Broker) TriggerReplayNow()

// 发送消息：P2P 优先，失败自动回退 relay
func (b *Broker) sendViaTransport(msg GatewayMessage) bool
```

**senderLoop 发送逻辑：**
```go
// 1. 尝试 P2P transport（如果存在）
// 2. P2P 发送失败 → 返回 false → senderLoop 回退到 relay
// 3. 永远不会静默丢消息
```

### 3. UpgradeManager (`internal/tunnel/upgrade.go`)

P2P 升级状态机：

```
UpgradeIdle → UpgradeNegotiating → UpgradeActive (成功)
                                ↘ UpgradeFailed (超时/失败)
```

**关键设计：**
- **`Restart()` 带 5 秒 debounce** — 移动端重连时可能产生多个 `confirmed as client`
  事件，debounce 合并为一次 P2P 协商
- **每次 `Restart()` 创建新的 `signalCh`** — 避免旧 offer 的 ICE candidate 竞争
- **`p2pDone` channel** 替代 `ctx.Done()` — P2P 激活后 ICE timeout 不再触发，
  只有 DataChannel 断开才退出等待
- **ICE timeout: 25 秒** — 容忍 GFW/CGNAT 环境下的 UDP 丢包

**升级流程：**
```go
func (m *UpgradeManager) runUpgrade(signalCh chan SignalMessage) {
    // 1. 创建 PeerConnection + DataChannel
    // 2. 生成 SDP offer，通过 relay 发送给 mobile
    // 3. 等待 mobile 的 answer + ICE candidates
    // 4. ICE connectivity check (最长 25s)
    // 5. 成功: SetP2PTransport → SyncP2PReplay → UpgradeActive
    //    失败: TriggerReplayNow → UpgradeFailed
}
```

### 4. WebRTC Peer (`internal/webrtc/peer.go`)

使用 pion/webrtc v4（纯 Go 实现）。

**ICE 配置：**
```go
// STUN servers (Google public)
stun:stun.l.google.com:19302
stun:stun1.l.google.com:19302

// TURN server (self-hosted coturn)
turn:turn.allpayone.net:8443
// realm: turn.allpayone.net
```

**SettingEngine 调优：**
```go
settings.SetICETimeouts(15*time.Second, 30*time.Second, 700*time.Millisecond)
settings.LoggerFactory = &pionLoggerFactory{}  // 重定向到 debug.Log
```

**ICE Candidate 类型优先级（高→低）：**
1. `host` — 局域网直连（同一 WiFi 时最快）
2. `srflx` — STUN 反射地址（NAT 穿透直连）
3. `relay` — TURN 中继（任何网络都能通，但带宽受限）

ICE agent 并行检查所有候选对，选择第一个成功的。网络切换时自动 failover
到备用候选对，DataChannel 不中断。

### 5. Host Peer Factory (`internal/webrtc/host_factory.go`)

Host 端 WebRTC 协商入口：
- 创建 PeerConnection（offerer 角色）
- 生成 SDP offer
- 收集本地 ICE candidates（trickle ICE）
- 处理 mobile 的 answer 和 remote candidates

### 6. Mobile Integration (`mobile/flutter/lib/webrtc/`)

**文件：**
- `p2p_peer.dart` — flutter_webrtc 封装，`handleOffer` / `createPeerConnection`
- `p2p_upgrade_manager.dart` — 移动端 P2P 升级管理
- `connection_service.dart` — `_handleRTCSignal` 处理 rtc_offer/answer/candidate

**Mobile P2P 角色：answerer**
```dart
// 1. 收到 host 的 rtc_offer
// 2. 创建 RTCPeerConnection
// 3. setRemoteDescription(offer)
// 4. createAnswer → send rtc_answer via relay
// 5. 交换 ICE candidates
// 6. onDataChannel → 切换消息路由到 DataChannel
```

## P2P-Priority Protocol

移动端连接时的完整时序：

```
1. Mobile → Relay: connect (QR scan)
2. Relay → Mobile: confirmed as client (HistoryCount, LastEventID)
3. Host: SetP2PNegotiating(true) ← 在 handleRelayConnected 之前
4. Host: handleRelayConnected → 检查 p2pUpgradePending()
   → YES: 跳过 relay recovery replay，记录 relayLastEventID
5. Host: p2pMgr.Restart() → 5s debounce → 发送 SDP offer
6. Mobile: 收到 offer → 创建 answer → 交换 ICE candidates
7. ICE connected → DataChannel opens
8. Host: SetP2PTransport(dc) → SyncP2PReplay()
   → 通过 DataChannel 补发 relayLastEventID 之后的增量事件
9. 所有后续消息走 P2P DataChannel
```

**如果 P2P 失败（步骤 7 超时）：**
```
7'. ICE timeout (25s)
8'. Host: TriggerReplayNow()
    → 清除 p2pNegotiating 标志
    → 通过 relay 重放所有 projection events（移动端按 EventID 去重）
9'. 所有消息继续走 relay
```

## Message Loss Prevention

### 问题：Projection Store 容量限制

Projection store 有 1000 条事件的 cap（`ProjectionReplayLimit = 1000`），
但 relay 历史计数可能达到 2000+。不能用数字索引做 gap 检测。

### 解决方案

| 场景 | 方法 | 去重机制 |
|------|------|----------|
| P2P 成功 | `SyncP2PReplay` — 用 `relayLastEventID` 在 projection events 中查找后缀 | EventID 匹配 |
| P2P 失败 | `TriggerReplayNow` — 重放所有 projection events | 移动端按 EventID 去重 |
| P2P 发送失败 | `sendViaTransport` 返回 false → senderLoop 回退 relay | 不丢消息 |

### sendViaTransport 回退逻辑

```go
func (b *Broker) sendViaTransport(msg GatewayMessage) bool {
    t := b.currentP2PTransport()
    if t == nil {
        return false  // 没有 P2P，走 relay
    }
    if err := t.Send(data); err != nil {
        return false  // P2P 失败，回退 relay
    }
    return true  // P2P 成功
}
// 返回 false 时，senderLoop 将消息放入 relay 队列
```

## Signaling Protocol

WebRTC 信令（SDP + ICE candidates）通过现有 relay WebSocket 交换，
**不需要修改 relay 协议**。新增的 GatewayMessage 类型：

```go
const (
    EventRTCOffer     = "rtc_offer"      // Host → Mobile: SDP offer
    EventRTCAnswer    = "rtc_answer"     // Mobile → Host: SDP answer
    EventRTCCandidate = "rtc_candidate"  // 双向: ICE candidate
)
```

Relay 服务器将这些消息原样转发给同房间的对端。对于没有 EventID 的
transient signaling 消息，relay 会转发给所有已连接的 client（不只是 ready 状态）。

## Relay Server (`ggcode-relay/`)

部署在 Railway，**不需要为 P2P 做任何特殊修改**：
- 转发 `rtc_offer` / `rtc_answer` / `rtc_candidate` 消息
- transient 事件（无 EventID）转发给所有 client
- 支持 cursor-based replay recovery

**TURN 服务器**（独立部署）：
- coturn at `turn.allpayone.net:8443`
- realm: `turn.allpayone.net`
- credential 由 share session 提供

## Reconnection & Edge Cases

| 场景 | 行为 |
|------|------|
| P2P 建立后断开 | DataChannel OnDisconnect → 清除 p2pTransport → 回退 relay |
| Relay 断开但 P2P 活跃 | P2P 继续工作，后台重连 relay |
| 移动端切换网络 (WiFi→4G) | ICE 自动 failover 到备用 candidate，DataChannel 可能不中断 |
| ICE timeout (25s) | TriggerReplayNow → 留在 relay |
| 移动端后台 (iOS) | OS 可能杀死 DataChannel → 前台后重连 relay + 重新 P2P 升级 |
| 移动端杀掉重启 | 全新连接周期：relay first → P2P upgrade |

## Host Support

P2P 对 CLI 和桌面端（Wails）同样生效。两者都通过同一个入口：

```go
// internal/agentruntime/tunnel_host.go
func (h *TunnelHost) StartShare(cfg ShareConfig) (*ShareResult, error)
```

桌面端 `desktop/ggcode-desktop-wails/app.go` 调用相同的 `StartShare`，
共享 `internal/tunnel/`、`internal/webrtc/` 全部代码。

**角色限制：** Host 只做 offerer（发起 P2P），Mobile 只做 answerer（接收 offer）。

## File Manifest

**Host (Go) — 共享代码：**
- `internal/tunnel/transport.go` — Transport 接口定义
- `internal/tunnel/broker.go` — 传输切换、消息发送、P2P replay 同步
- `internal/tunnel/upgrade.go` — UpgradeManager 状态机
- `internal/tunnel/relay_client.go` — Relay WebSocket transport + RTC 信号路由
- `internal/webrtc/peer.go` — PeerConnection 封装、pion 日志重定向、ICE timeout
- `internal/webrtc/host_factory.go` — Host 端 offer 协商
- `internal/webrtc/signal.go` — SDP 编解码
- `internal/agentruntime/tunnel_host.go` — StartShare 入口、OnRelayConnected 回调

**Mobile (Flutter)：**
- `mobile/flutter/lib/webrtc/p2p_peer.dart` — flutter_webrtc 封装
- `mobile/flutter/lib/webrtc/p2p_upgrade_manager.dart` — 移动端升级管理
- `mobile/flutter/lib/core/connection_service.dart` — RTC 信号处理

**Relay：**
- `ggcode-relay/relay.go` — 消息转发（无 P2P 特殊逻辑，原样转发 signaling）

**TURN：**
- coturn config at `turn.allpayone.net:8443`

## Dependencies

**Host:**
- `github.com/pion/webrtc/v4` — 纯 Go WebRTC 实现

**Mobile:**
- `flutter_webrtc: ^0.12.0` — Flutter WebRTC 插件

## Configuration

ICE timeout 可通过 `UpgradeConfig` 配置：

```go
type UpgradeConfig struct {
    ICETimeout    time.Duration // 默认 25s
    ICETimeoutMax time.Duration // 默认 30s
    Keepalive     time.Duration // 默认 700ms
}
```

日志通过 `debug.Log("webrtc", ...)` 和 `debug.Log("tunnel", ...)` 输出到
内部 debug 系统，不污染 TUI。

## Debugging

查看 P2P 连接状态：

```bash
# 通过 debug_log 工具
debug_log(category="tunnel")

# 关键日志行：
# - "confirmed as client" → relay 连接确认
# - "skipping relay recovery replay (P2P upgrade pending)" → P2P 优先逻辑生效
# - "DataChannel ready, switching transport" → P2P 建立成功
# - "syncP2PReplay: sending N missed events via P2P" → 增量同步
# - "p2p send ... ok" → 消息通过 P2P 发送
# - "ICE timeout" → P2P 失败
# - "triggerReplayNow: replaying N projection events" → 回退 relay
```
