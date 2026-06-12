# Architecture Diagram: Message Flow and Loss Points

## System Architecture Overview

```
┌─────────────────────────────────────────────────────────────────────┐
│                        GGCODE MESSAGING SYSTEM                       │
└─────────────────────────────────────────────────────────────────────┘

┌──────────────────────┐          ┌──────────────────────┐
│   DESKTOP CLIENT     │          │   MOBILE CLIENT      │
│  (Wails Desktop)     │          │  (Flutter App)       │
└─────────┬────────────┘          └──────────┬───────────┘
          │                                   │
          │ Broadcasts events                │ Sends messages
          │ (text, tool_call, response)     │ Reconnects with token
          │                                 │
          ├─────────────────────┬───────────┘
          │                     │
          ▼                     ▼
    ┌─────────────────────────────────┐
    │     TUNNEL (Broker)             │
    │   internal/tunnel/broker.go     │
    │                                 │
    │  KEY FUNCTION:                  │
    │  relayRecoveryPlan()            │
    │  Lines: 863-902                 │
    │                                 │
    │  DECISION POINT:                │
    │  authority_epoch change?        │
    │  Line 869-873:                  │
    │    if auth != current:          │
    │      reset = true               │
    └──────┬───────────────┬──────────┘
           │               │
           │ replace_history
           │ mode (line 753)
           │
           ▼
    ┌─────────────────────────────────┐
    │     RELAY (Message Hub)         │
    │  ggcode-relay/relay.go          │
    │                                 │
    │  STORAGE LAYER:                 │
    │  Lines: 440-454                 │
    │    room.history (array)         │
    │    ├─ Text messages             │
    │    ├─ Tool calls                │
    │    ├─ Responses                 │
    │    └─ All as type="encrypted"   │
    │                                 │
    │  WIPEOUT FUNCTION:              │
    │  bindRoomSession()              │
    │  Lines: 693-694:                │
    │    if replaceHistory:           │
    │      clearHistoryLocked()       │
    │                                 │
    │  DELETION FUNCTION:             │
    │  clearHistoryLocked()           │
    │  Lines: 110-114:                │
    │    r.history = nil     ← LOSS   │
    │    r.bootstrap = {}             │
    │                                 │
    │  REPLAY FUNCTION:               │
    │  eventsAfter()                  │
    │  Line: 598                      │
    │    returns empty (history nil)  │
    └──────────────┬────────────┬─────┘
                   │            │
                   │ resume_ack │
                   │ count=0    │
                   ▼            ▼
            ┌─────────────────────────────┐
            │  MOBILE APP                 │
            │  connection_provider.dart   │
            │                             │
            │  HANDLER:                   │
            │  resume_ack                 │
            │  Lines: 434-452             │
            │    replayCount = 0          │
            │    ← No events expected     │
            │                             │
            │  DEDUP CHECK:               │
            │  _shouldApplyEvent()        │
            │  Lines: 1229-1233           │
            │    cached events already    │
            │    seen → skip              │
            │                             │
            │  RESULT:                    │
            │  ❌ Blank chat history      │
            └─────────────────────────────┘
```

---

## Decision Tree: When Reset Happens

```
┌─────────────────────────────────────────────────────────────────┐
│ Broker calls relayRecoveryPlan(relay_info, sessionID)          │
│ internal/tunnel/broker.go:863-902                             │
└───────────────────────┬─────────────────────────────────────────┘
                        │
                        ▼
            ┌───────────────────────────┐
            │ canonicalReplayState()     │
            │ available?                 │
            └───────────┬───────────┬───┘
                        │           │
                   NO   │           │ YES
                        │           │
                        ▼           ▼
        ┌──────────────────┐  ┌────────────────────┐
        │ EARLY RETURN:    │  │ Continue...        │
        │ reset=            │  └─────────┬──────────┘
        │   HistoryCount>0 │            │
        └──────────────────┘            ▼
                                ┌──────────────────────────┐
                                │ currentAuth = epoch()    │
                                │ Check: AuthorityEpoch==0?│
                                └────────┬─────────┬───────┘
                                         │         │
                                    NO   │         │ YES
                                         │         │
                        ┌────────────────┘         └──┐
                        │                             │
                        ▼                             ▼
    ┌──────────────────────────────────┐    ┌──────────────────────┐
    │ Check: Auth != currentAuth?       │    │ First time setup     │
    │ Line 869: THIS IS THE BUG! ✓ YES  │    │ epoch=0 case        │
    │ (happens on token renewal)        │    │ reset=               │
    │                                  │    │  HistoryCount > 0    │
    │ Line 870-871:                    │    │                      │
    │ if events_empty && count_empty:  │    └──────────────────────┘
    │   return trusted=true             │
    │ else:                             │
    │   return reset=HistoryCount>0     │ ← SET RESET FLAG!
    │          (Line 873)               │
    └──────────────────────────────────┘
             │ reset=true
             ▼
    ┌──────────────────────────────────┐
    │ Lines 890-896: Later validation   │
    │ Hash checks, count checks, etc.   │
    │ (But reset already decided!)      │
    └──────────────────────────────────┘
             │ if reset:
             ▼
    ┌──────────────────────────────────┐
    │ sendActiveSessionWithMode()       │
    │ Line 753                          │
    │ mode = replace_history            │
    │ ↓ TRIGGERS WIPEOUT ON RELAY      │
    └──────────────────────────────────┘
```

---

## Component Interaction: Token Renewal Scenario

```
TIME    DESKTOP             BROKER                 RELAY              MOBILE
────    ──────────          ──────                 ─────              ──────
 0      Connected           Running                Running            Connected
        epoch=42            epoch=42               epoch=42           _lastEventId=evt:5
                                                   history: [evt:1-5]

 1      User sends          Broadcasts to          Stores in          Receives
        message             relay                  history            Applies to UI
        text event          (evt:6)                (evt:6 added)      _lastEventId=evt:6

 2                          Token refreshes        ✓ Still running     ✓ Connected
                            epoch=43               epoch=42           (old token)

 3      Reconnects          Authority epoch        Idle                Disconnects
        with new token      updated to 43
        auth=43

 4                          Calls relayRecovery
                            Plan()

 5                          info.AuthorityEpoch=42
                            currentAuth=43
                            ❌ MISMATCH!
                            (Line 869)

 6                          Sets reset=true
                            (Line 873)
                            Because HistoryCount>0

 7                          Sends active_session
                            mode=replace_history
                            (Line 753)

 8                                                 Receives message
                                                   (Line 501)
                                                   replaceHistory=true
                                                   (Line 501)

 9                                                 Calls bindRoomSession()
                                                   (Line 693)
                                                   authorityChanged=true
                                                   (Line 691)

 10                                                Calls clearHistoryLocked()
                                                   (Line 694)
                                                   r.history = nil ❌
                                                   [evt:1-6 DELETED]

 11     [Desktop            [History wipeout       [Empty history]
        has local copy      complete]
        of events]

 12                                                                    Reconnects
                                                                      with new token
                                                                      auth=43
                                                                      cursor=evt:6

 13                                                Receives resume_from
                                                   Calls prepareResume()
                                                   (Line 598)

 14                                                eventsAfter(evt:6)
                                                   Queries nil history
                                                   Returns []
                                                   (empty!)

 15                                                Sends resume_ack
                                                   replay_count=0
                                                   (Line 612)

 16                         Receives              ✓ resume_ack
        [Replays             resume_ack           received
        from local           (doesn't expect      (replay_count=0)
        state]              replay)

 17                                                                    Receives
                                                                      resume_ack
                                                                      (Line 442)
                                                                      replayCount=0
                                                                      Expects NO events

 18                                                                    Dedup check:
                                                                      "evt:6" <= "evt:6"
                                                                      Skip (already seen)
                                                                      (Line 1231)

 19     ✓ Message           [Lost state]          ✓ Working          ❌ BLANK
        visible                                   correctly          CHAT
        (from local)                              (no events to       (message
                                                  send)              lost)
```

---

## Data Flow: Where Messages Live

```
USER ACTION: "Send message"
       │
       ▼
MESSAGE: { type: "text", content: "Hello", eventID: "evt:123" }
       │
       ├─────────────────┬──────────────────┬─────────────────────┐
       │                 │                  │                      │
       ▼                 ▼                  ▼                      ▼
  DESKTOP        TUNNEL CACHE        RELAY HISTORY        MOBILE CACHE
  (Ephemeral)    (Ephemeral)         (RAM, 1 copy)        (Local DB)
                 
  Has evt:123 ✓   Has evt:123 ✓      Has evt:123 ✓       Has evt:123 ✓
                 Keeps for replay    [Persistent         Displayed in
                 when new clients    during session]      UI
                 connect


ON TOKEN RENEWAL:

BEFORE WIPEOUT:
  Desktop cache        Relay history    Mobile cache
  evt:123 ✓            evt:123 ✓        evt:123 ✓ (in UI)
  evt:124 ✓            evt:124 ✓        evt:124 ✓
  evt:125 ✓            evt:125 ✓        evt:125 ✓

AUTHORITY EPOCH CHANGE TRIGGERS:
  ↓ clearHistoryLocked()

AFTER WIPEOUT:
  Desktop cache        Relay history    Mobile cache
  evt:123 ✓            ❌ DELETED       evt:123 ✓ (stale)
  evt:124 ✓            ❌ DELETED       evt:124 ✓ (stale)
  evt:125 ✓            ❌ DELETED       evt:125 ✓ (stale)

MOBILE RECONNECTS:
  Asks relay for replay from evt:125
  Relay: "No history (was cleared)"
  Mobile: "OK, expect 0 events"
  
DEDUP PREVENTS RE-RENDER:
  Mobile already has evt:125 in cache
  Dedup check: "this is old, skip"
  UI: Blank (cache not rendered, relay has nothing)
```

---

## Layers That Work Correctly

```
✅ WORKING CORRECTLY:

1. STORAGE (relay.go:440-454)
   └─ All message types stored
   └─ No discrimination
   └─ All events added to room.history

2. TRANSMISSION (relay.go:456-461)
   └─ Events broadcast to connected clients
   └─ Delivery confirmed

3. MOBILE RECEIPT (connection_provider.dart:613-635)
   └─ Text events received
   └─ Dedup logic working
   └─ Events applied to UI

4. MOBILE CACHE (Local DB)
   └─ Events persisted
   └─ survives reconnection
   └─ Can be queried


❌ BROKEN:

1. DECISION LOGIC (broker.go:869-873)
   └─ Authority mismatch → reset flag
   └─ Too aggressive
   └─ Doesn't validate actual inconsistency

2. DELETION LOGIC (relay.go:693-694, 110-114)
   └─ Entire history wiped
   └─ No discrimination
   └─ No recovery possible

3. REPLAY (relay.go:598)
   └─ Returns empty (history was cleared)
   └─ Mobile expects 0 events
   └─ No cache refresh mechanism
```

---

## The Bug in Three Lines

```go
// Line 869-873: broker.go
if info.AuthorityEpoch != currentAuthority {
    return relayRecoveryPlan{reset: info.HistoryCount > 0, replayFrom: 0}
}
// ^ This decision is TOO BROAD

// Line 693-694: relay.go
if changed || replaceHistory || authorityChanged {
    p.room.clearHistoryLocked()  // WIPEOUT
}
// ^ This executes the bad decision

// Line 111: relay.go
r.history = nil  // COMPLETE DELETION
// ^ This is the actual data loss
```

---

## Fix Impact

```
CHANGE: broker.go:869-873
  Authority epoch mismatch alone does NOT trigger reset
  Must have additional evidence (hash mismatch, count mismatch, etc.)

RESULT:

┌─────────────────────────────────────────────────────┐
│ BEFORE FIX:                                         │
│ Token renewal → epoch mismatch → history wipeout    │
│ Mobile reconnect → 0 replay → blank chat            │
│                                                     │
│ AFTER FIX:                                          │
│ Token renewal → epoch mismatch → no wipeout        │
│ Mobile reconnect → full replay → chat visible      │
└─────────────────────────────────────────────────────┘

✅ Mobile message history preserved
✅ Users see all messages after reconnect
✅ No silent data loss
✅ Same UX as desktop
```

---

## Testing Verification

```
TEST 1: Authority Mismatch Preservation
┌─────────────────────────────────────┐
│ 1. Relay has history: [evt1-5]      │
│ 2. Authority epoch changes          │
│ 3. Broker calls relayRecoveryPlan() │
│                                     │
│ EXPECTED:                           │
│ - reset = false                     │
│ - history preserved                 │
│                                     │
│ CURRENT:                            │
│ - reset = true ✗                    │
│ - history WIPED ✗                   │
│                                     │
│ AFTER FIX:                          │
│ - reset = false ✓                   │
│ - history preserved ✓               │
└─────────────────────────────────────┘

TEST 2: Hash Mismatch Still Resets
┌─────────────────────────────────────┐
│ 1. Relay has history: [evt1-5]      │
│ 2. Authority epoch SAME             │
│ 3. Projection hash DIFFERS           │
│ 4. Broker calls relayRecoveryPlan() │
│                                     │
│ EXPECTED:                           │
│ - reset = true (hash validation)    │
│ - history WIPED                     │
│                                     │
│ AFTER FIX:                          │
│ - reset = true ✓                    │
│ - history WIPED ✓                   │
│ (Still works for real corruptions)  │
└─────────────────────────────────────┘

TEST 3: Mobile Replay After Renewal
┌─────────────────────────────────────┐
│ 1. Send message (evt:123)           │
│ 2. Token renewal                    │
│ 3. Mobile reconnects                │
│ 4. Relay sends replay               │
│                                     │
│ CURRENT:                            │
│ - replay_count = 0 ✗                │
│ - Message lost ✗                    │
│                                     │
│ AFTER FIX:                          │
│ - replay_count > 0 ✓                │
│ - Message visible ✓                 │
└─────────────────────────────────────┘
```

---

## Summary

The mobile message loss is caused by an **overly aggressive decision algorithm** in the broker that wipes all relay history whenever the broker's authority epoch doesn't match the relay's stored epoch.

- **Problem**: Authority mismatch (normal during token renewal) triggers COMPLETE history wipeout
- **Impact**: Mobile clients reconnecting after token renewal see blank chat history
- **Root Cause**: Decision logic in `broker.go:869-873` has no safeguards against false positives
- **Fix**: Remove authority epoch mismatch alone as reset trigger; rely on hash validation
- **Risk**: Low (change isolated, other validation still active)
- **Verification**: Clear and testable across all four components
