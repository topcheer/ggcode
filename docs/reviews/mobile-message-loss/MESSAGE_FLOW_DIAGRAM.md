# Message Flow Diagram: Where Messages Are Lost

## Normal Flow (No Authority Epoch Change)

```
┌─────────────────────────────────────────────────────────────────────┐
│ User sends message                                                   │
└─────────────────┬───────────────────────────────────────────────────┘
                  │
                  ▼
┌─────────────────────────────────────────────────────────────────────┐
│ Desktop: Agent processes message, creates "text" event              │
│ Event ID: event:123, Type: "text", Content: "Hello"                │
└─────────────────┬───────────────────────────────────────────────────┘
                  │
                  ▼
┌─────────────────────────────────────────────────────────────────────┐
│ Desktop broadcasts event to relay via tunnel                        │
│ (internal/tunnel/broker.go - broadcastToRelay)                     │
└─────────────────┬───────────────────────────────────────────────────┘
                  │
                  ▼
┌─────────────────────────────────────────────────────────────────────┐
│ Relay: handleServerBroadcast()                                      │
│ relay.go:440-454                                                    │
│                                                                      │
│ ev := roomEvent{                                                    │
│   eventID: "event:123",                                            │
│   typ: "encrypted",                                                │
│   raw: [...message data...],                                       │
│ }                                                                   │
│ p.room.appendEvent(ev)  ← STORED IN room.history                   │
└─────────────────┬───────────────────────────────────────────────────┘
                  │
                  ▼
┌─────────────────────────────────────────────────────────────────────┐
│ Relay: Broadcast to all connected clients (including mobile)        │
│ Mobile receives "text" event                                        │
│ Updates UI, stores in local cache                                  │
│ Sets _lastAppliedEventId = "event:123"                             │
└─────────────────┬───────────────────────────────────────────────────┘
                  │
                  ▼
          ✅ MESSAGE VISIBLE ON MOBILE
                 (cached locally)
                 (also in relay.room.history)
```

---

## Problematic Flow: Token Renewal with Authority Epoch Mismatch

```
┌─────────────────────────────────────────────────────────────────────┐
│ Token expires and is renewed                                        │
│ New token has DIFFERENT authority epoch                             │
│ (was epoch:42, now epoch:43)                                        │
└─────────────────┬───────────────────────────────────────────────────┘
                  │
                  ▼
┌─────────────────────────────────────────────────────────────────────┐
│ Mobile disconnects (old token invalid)                              │
│ Local cache STILL HAS: event:123 (text message)                    │
│ _lastAppliedEventId = "event:123"                                  │
└─────────────────┬───────────────────────────────────────────────────┘
                  │
                  ▼
┌─────────────────────────────────────────────────────────────────────┐
│ Desktop client with NEW token connects to broker                    │
│ Broker authority epoch updated to epoch:43                          │
│ Calls relayRecoveryPlan() with relay's info:                        │
│   - relay.AuthorityEpoch = epoch:42 (old)                          │
│   - relay.HistoryCount = 1 (has event:123)                         │
└─────────────────┬───────────────────────────────────────────────────┘
                  │
                  ▼
┌─────────────────────────────────────────────────────────────────────┐
│ CRITICAL: broker.go:869-873 - relayRecoveryPlan()                 │
│                                                                     │
│ currentAuthority = epoch:43                                        │
│ if info.AuthorityEpoch != currentAuthority {  ✅ TRUE              │
│     if len(events) == 0 && info.HistoryCount == 0 {                │
│         return relayRecoveryPlan{trusted: true}                    │
│     }                                                               │
│     return relayRecoveryPlan{                                       │
│         reset: info.HistoryCount > 0,  ✅ TRUE (has 1 event)      │
│         replayFrom: 0                                              │
│     }                                                              │
│ }                                                                  │
│                                                                     │
│ ⚠️  DECISION: reset = true                                         │
└─────────────────┬───────────────────────────────────────────────────┘
                  │
                  ▼
┌─────────────────────────────────────────────────────────────────────┐
│ broker.go:752-753 - handleRelayConnected()                         │
│                                                                     │
│ if plan.reset {                                                    │
│     b.sendActiveSessionWithMode(                                   │
│         currentSessionID,                                          │
│         ActiveSessionModeReplaceHistory  ← TRIGGERS WIPEOUT        │
│     )                                                              │
│ }                                                                  │
└─────────────────┬───────────────────────────────────────────────────┘
                  │
                  ▼
┌─────────────────────────────────────────────────────────────────────┐
│ Relay receives active_session with mode="replace_history"          │
│ relay.go:501 - onActiveSession()                                   │
│                                                                     │
│ authorityEpoch, changed, _, _ := p.bindRoomSession(               │
│     sessionID,                                                     │
│     msg.AuthorityEpoch,                                            │
│     msg.ResumeMode == activeSessionModeReplace  ← TRUE             │
│ )                                                                  │
└─────────────────┬───────────────────────────────────────────────────┘
                  │
                  ▼
┌─────────────────────────────────────────────────────────────────────┐
│ relay.go:693-694 - bindRoomSession()                               │
│                                                                     │
│ if changed || replaceHistory || authorityChanged {                 │
│     p.room.clearHistoryLocked()  ✅ TRUE, CALLS CLEAR              │
│ }                                                                  │
│                                                                     │
│ ⚠️  BOOM! Entering clearHistoryLocked()...                        │
└─────────────────┬───────────────────────────────────────────────────┘
                  │
                  ▼
┌──────────────────────────────────────────────────────────────────────┐
│ 💣 EXPLOSION! relay.go:110-114 - clearHistoryLocked()             │
│                                                                      │
│ func (r *room) clearHistoryLocked() {                              │
│     r.history = nil         ← EVENT:123 DELETED!!!                │
│     r.bootstrap = make(map[string]roomEvent)                       │
│     r.lastEventAt = time.Time{}                                    │
│ }                                                                  │
│                                                                      │
│ RESULT: room.history is now COMPLETELY EMPTY                       │
└──────────────────┬───────────────────────────────────────────────────┘
                   │
                   ▼
┌──────────────────────────────────────────────────────────────────────┐
│ Mobile user reconnects with NEW token (epoch:43)                    │
│ Sends: resume_from(cursor: "event:123", authority_epoch: epoch:43) │
│                                                                      │
│ Relay receives resume_hello                                         │
│ Client requests replay from event:123 onward                        │
└──────────────────┬───────────────────────────────────────────────────┘
                   │
                   ▼
┌──────────────────────────────────────────────────────────────────────┐
│ relay.go:598 - prepareResumeLocked()                               │
│                                                                      │
│ replay := p.room.eventsAfter(p.cursor)                             │
│                                                                      │
│ Looks up: room.history[after "event:123"]                          │
│ But room.history = nil (was cleared!)                              │
│                                                                      │
│ Result: replay = []  (EMPTY LIST)                                  │
│                                                                      │
│ len(replay) = 0                                                    │
└──────────────────┬───────────────────────────────────────────────────┘
                   │
                   ▼
┌──────────────────────────────────────────────────────────────────────┐
│ relay.go:607-613 - Resume ACK sent to mobile                       │
│                                                                      │
│ ack := relayMessage{                                               │
│     Type: "resume_ack",                                            │
│     Data: mustJSON(map[string]interface{}{                         │
│         "resume_mode": "incremental",                              │
│         "replay_count": 0  ← ZERO! NO HISTORY                      │
│     }),                                                            │
│ }                                                                  │
└──────────────────┬───────────────────────────────────────────────────┘
                   │
                   ▼
┌──────────────────────────────────────────────────────────────────────┐
│ Mobile: connection_provider.dart:434-452 - resume_ack handler      │
│                                                                      │
│ final replayCount = (msg.data?['replay_count'] as num?)?.toInt()   │
│                      = 0  ✅ MATCHES WHAT RELAY SENT                │
│                                                                      │
│ _beginResumeReplaySync(                                            │
│     replayCount: 0,  ← ZERO EVENTS EXPECTED                        │
│     resumeMode: "incremental",                                     │
│ )                                                                  │
│                                                                      │
│ Mobile expects 0 events to be replayed                             │
│ User sees BLANK CHAT                                               │
│                                                                      │
│ But local cache HAS: event:123!                                    │
│ (_lastAppliedEventId = "event:123" already applied)                │
│                                                                      │
│ connection_provider.dart:1229-1233 - _shouldApplyEvent()           │
│ When UI tries to re-show event:123:                                │
│     final ord = _parseEventOrdinal("event:123")                   │
│     final last = _parseEventOrdinal("event:123")                   │
│     if ord <= last { return false }  ✅ SKIP (already seen)         │
│                                                                      │
│ ❌ MESSAGE NOT RE-APPLIED TO UI                                    │
└──────────────────────────────────────────────────────────────────────┘
```

---

## The Gap: Local Cache vs Relay History

```
MOBILE LOCAL CACHE          RELAY HISTORY               UI DISPLAY
─────────────────────       ──────────────────          ──────────

Has: event:123              Has: event:123
      └─ Text message             └─ Text message       ✅ VISIBLE
      (cached from                  (stored)              (before disconnect)
       before disconnect)

[DISCONNECT & RECONNECT WITH NEW TOKEN]

Still has: event:123        ❌ DELETED! (WIPEOUT)       ❌ BLANK
           └─ Stale           └─ GONE FOREVER            └─ Lost
           (not re-applied)     (cleared by broker)       message

[MOBILE TRIES TO APPLY CACHED EVENT]

event:123 dedup check:
    "event:123" <= "event:123" → SKIP (already seen)
    
    ✗ Event not re-rendered
    ✗ Cache is never cleared
    ✗ UI state becomes inconsistent with cache state
```

---

## Root Cause Path

```
┌─────────────────────────┐
│  Token renewal          │
│  (authority epoch ↑)    │
└────────────┬────────────┘
             │
             ▼
┌─────────────────────────────────────────────────────┐
│ broker.go:869-873                                   │
│ relayRecoveryPlan() detects authority mismatch     │
│ Sets reset = true (TOO AGGRESSIVE)                 │
└────────────┬────────────────────────────────────────┘
             │
             ▼
┌─────────────────────────────────────────────────────┐
│ broker.go:752-753                                   │
│ Sends mode=replace_history to relay                │
└────────────┬────────────────────────────────────────┘
             │
             ▼
┌─────────────────────────────────────────────────────┐
│ relay.go:501                                        │
│ onActiveSession() receives replace_history         │
│ Passes replaceHistory=true to bindRoomSession      │
└────────────┬────────────────────────────────────────┘
             │
             ▼
┌─────────────────────────────────────────────────────┐
│ relay.go:693-694                                    │
│ bindRoomSession() checks: replaceHistory=true      │
│ Calls clearHistoryLocked()                         │
└────────────┬────────────────────────────────────────┘
             │
             ▼
┌─────────────────────────────────────────────────────┐
│ relay.go:110-114                                    │
│ clearHistoryLocked()                               │
│ r.history = nil  ← COMPLETE WIPEOUT                │
└─────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────┐
│  Mobile reconnects                                  │
│  Relay has no history to replay                    │
│  Mobile gets replay_count=0                        │
│  Message loss                                      │
└─────────────────────────────────────────────────────┘
```

---

## Evidence: Messages ARE Stored Before Wipeout

### Before Wipeout (relay.go:440-454)
```
room.history = [
    { eventID: "event:123", type: "encrypted", raw: [bytes...] },
    { eventID: "event:124", type: "encrypted", raw: [bytes...] },
    ...
]
```

### After Wipeout (relay.go:110-114)
```
room.history = nil  ← GONE!
```

**Key insight**: Storage is working fine. The problem is **deletion**, not filtering or selection.

---

## Why The Fix Is Simple

The decision to reset history (broker.go:873) is **too broad**.

**Current logic**: 
- Authority mismatch + relay has history = **RESET EVERYTHING**

**Better logic**:
- Authority mismatch alone = **CHECK OTHER EVIDENCE**
- Only reset if there's actual inconsistency (hash mismatch, corruption evidence, etc.)

**Impact**:
- Preserves valid message history
- Mobile gets replay
- Users see their messages
- No data loss

---

## Timeline to Implement Fix

1. **Analyze**: Understand why the reset was added originally (ask in PR/commit history)
2. **Modify**: Change broker.go:869-873 to NOT reset on authority mismatch alone
3. **Test**: Add unit tests for token renewal scenario
4. **Verify**: Manual testing with mobile reconnect after token renewal
5. **Deploy**: Roll out fix with monitoring

**Estimated time**: 2-4 hours (analysis + implementation + testing)

**Risk**: Low (only affects history reset logic, not message flow or relay connections)
