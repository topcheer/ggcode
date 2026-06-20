# Architecture Diagram: Message Flow and Loss Points

> Updated: 2026-06-20. All line numbers verified against current codebase.
> **Key change since original**: Relay now has SQLite persistence layer (`store.go`) that
> can rehydrate `room.history` after `clearHistoryLocked()`. This partially mitigates the
> wipeout bug — see "SQLite Rehydration" sections below.

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
    │  Lines: 948-987                 │
    │                                 │
    │  DECISION POINT:                │
    │  authority_epoch mismatch?      │
    │  Line 954:                      │
    │    if epoch==0 || epoch!=cur:   │
    │      Line 955-956: guard        │
    │        (both empty → trusted)   │
    │      Line 958:                  │
    │        reset = HistoryCount>0   │
    └──────┬───────────────┬──────────┘
           │               │
           │ replace_history
           │ mode (line 837)
           │
           ▼
    ┌─────────────────────────────────┐
    │     RELAY (Message Hub)         │
    │  ggcode-relay/relay.go          │
    │                                 │
    │  STORAGE LAYERS:                │
    │  1. In-memory:                  │
    │     room.history (slice)        │
    │     appendEvent() line 98       │
    │     Dedup: tail-50 check        │
    │     line 103-107                │
    │                                 │
    │  2. SQLite (persistent):        │
    │     store.go                    │
    │     persistEvent() line 227     │
    │     loadRoom() line 174         │
    │     Tables: relay_rooms,        │
    │      relay_sessions,            │
    │      relay_events               │
    │     Dedup: UPSERT by            │
    │      (token, session, event)    │
    │                                 │
    │  WIPEOUT FUNCTION:              │
    │  bindRoomSession()              │
    │  Line 734:                      │
    │    if changed||replace||authChg:│
    │      clearHistoryLocked()       │
    │                                 │
    │  DELETION FUNCTION:             │
    │  clearHistoryLocked()           │
    │  Lines 113-117:                 │
    │    r.history = nil   ← MEM LOSS │
    │    r.bootstrap = {}             │
    │    r.lastEventAt = zero         │
    │    (SQLite NOT cleared)         │
    │                                 │
    │  HYDRATION FUNCTION:            │
    │  hydrateLocked()                │
    │  Lines 119-140:                 │
    │    Only fires if history empty  │
    │    AND sessionID empty          │
    │    Restores from SQLite         │
    │    Guard: line 120-121          │
    │      skips if sessionID set     │
    │                                 │
    │  REPLAY FUNCTION:               │
    │  eventsAfter()                  │
    │  Lines 201-218                  │
    │  prepareResumeLocked()          │
    │  Lines 638-659                  │
    │    Returns resume_ack with      │
    │    replay_count=len(events)     │
    └──────────────┬────────────┬─────┘
                   │            │
                   │ resume_ack │
                   │ count=N    │
                   ▼            ▼
            ┌─────────────────────────────┐
            │  MOBILE APP                 │
            │  connection_provider.dart   │
            │                             │
            │  HANDLER:                   │
            │  resume_ack                 │
            │  Lines: 590-630             │
            │    replayCount parsed       │
            │    line 598                 │
            │    If 0 + resumeFrom set:   │
            │      marks sessionReady     │
            │      line 614-618           │
            │                             │
            │  DEDUP CHECK:               │
            │  _shouldApplyEvent()        │
            │  Lines: 1404-1470           │
            │    _recentEventSet check    │
            │    line 1422                │
            │    Ordinal <= last check    │
            │    line 1437                │
            │    Gap detection            │
            │    line 1452                │
            │                             │
            │  RESULT (if history lost):  │
            │  ❌ Blank chat history      │
            └─────────────────────────────┘
```

---

## SQLite Persistence Layer (NEW — not in original diagram)

```
┌──────────────────────────────────────────────────────────────────┐
│                    RELAY SQLite STORE                            │
│                    ggcode-relay/store.go                         │
└──────────────────────────────────────────────────────────────────┘

  EVENT FLOW (write path):

  Desktop → relayMessage → relayToOthers()
                             │
                             ├─ broadcast to peers (in-memory)
                             │
                             └─ persistEvent()           [store.go:227]
                                  │
                                  ▼
                              SQLite WRITE (WAL mode)
                              ┌──────────────────────┐
                              │ relay_rooms          │
                              │  (token_hash,        │
                              │   session_id,        │
                              │   authority_epoch)   │
                              ├──────────────────────┤
                              │ relay_sessions       │
                              │  (token_hash,        │
                              │   session_id,        │
                              │   last_event_at)     │
                              ├──────────────────────┤
                              │ relay_events         │
                              │  (token_hash,        │
                              │   session_id,        │
                              │   event_id,          │
                              │   type, raw JSON)    │
                              │  UPSERT dedup        │
                              └──────────────────────┘

  EVENT FLOW (read/hydrate path):

  New client connects → bindRoomSession()
                           │
                           ├─ clearHistoryLocked() [if triggered]
                           │  (wipes in-memory only)
                           │
                           └─ hydrateRoomFromStore()  [relay.go:742]
                                │
                                ▼
                            store.loadRoom()         [store.go:174]
                                │
                                ▼
                            hydrateLocked()           [relay.go:119]
                                │
                                ├─ Guard: skip if sessionID != ""
                                │  (line 120-121)
                                │
                                └─ Restores history from SQLite
                                   into room.history
```

**Critical insight**: `clearHistoryLocked()` only nils the in-memory slice.
SQLite records are NOT deleted. BUT `hydrateLocked()` (line 120-121) refuses
to fire if `r.sessionID != ""` — and `bindRoomSession()` sets
`r.sessionID` BEFORE clearing. So the hydrate guard blocks recovery:

```go
// relay.go:731-736 — bindRoomSession sets session BEFORE clearing
p.room.sessionID = sessionID              // line 733
if changed || replaceHistory || authorityChanged {
    p.room.clearHistoryLocked()           // line 735 — wipes memory
}
// ↑ After this, sessionID is set, so hydrateLocked() guard at
//   line 120 returns false → SQLite data is NOT loaded back.
```

---

## Decision Tree: When Reset Happens

```
┌─────────────────────────────────────────────────────────────────┐
│ Broker calls relayRecoveryPlan(relay_info, sessionID)          │
│ internal/tunnel/broker.go:948-987                              │
└───────────────────────┬─────────────────────────────────────────┘
                        │
                        ▼
            ┌───────────────────────────┐
            │ canonicalReplayState()     │
            │ available?                 │
            │ line 949                   │
            └───────────┬───────────┬───┘
                        │           │
                   NO   │           │ YES
                        │           │
                        ▼           ▼
    ┌──────────────────────┐  ┌──────────────────────────┐
    │ Line 951:            │  │ Continue...              │
    │ reset =              │  └─────────┬────────────────┘
    │   HistoryCount > 0   │            │
    │ (no local events     │            ▼
    │  to compare)         │  ┌──────────────────────────────┐
    └──────────────────────┘  │ Line 954:                   │
                              │ epoch==0 || epoch!=current? │
                              └────────┬──────────┬─────────┘
                                       │          │
                                  NO   │          │ YES
                                       │          │
                    ┌──────────────────┘          └──┐
                    │                                │
                    ▼                                ▼
    ┌────────────────────────────┐    ┌───────────────────────────┐
    │ Lines 960-961:             │    │ Line 955-956: GUARD        │
    │ SessionID mismatch?        │    │ Both events & count empty? │
    │ reset = HistoryCount > 0   │    └─────┬─────────────────┬───┘
    └────────────────────────────┘          │                 │
                                        YES │            NO   │
                                            │                 │
                                            ▼                 ▼
                                    ┌──────────────┐  ┌───────────────────┐
                                    │ trusted=true │  │ Line 958:         │
                                    │ (no reset)   │  │ reset =           │
                                    └──────────────┘  │  HistoryCount > 0 │
                                                      │ replayFrom = 0    │
                                                      │ ← RESET TRIGGERED │
                                                      └───────┬───────────┘
                                                              │
                                                      (also if epoch==0
                                                       with events,
                                                       same path)
                                                              │
                                                              ▼
                                       ┌──────────────────────────┐
                                       │ Lines 963-967:           │
                                       │ Events empty, count > 0? │
                                       │ → reset = true           │
                                       │                          │
                                       │ Lines 969-970:           │
                                       │ Events exist, count == 0?│
                                       │ → replay all (no reset)  │
                                       │                          │
                                       │ Lines 972-986:           │
                                       │ Hash/ID/count validation │
                                       │ Mismatch → reset = true  │
                                       │ Match → trusted or       │
                                       │   incremental replay     │
                                       └──────────────────────────┘
```

---

## Component Interaction: Token Renewal Scenario

```
TIME    DESKTOP             BROKER                 RELAY              MOBILE
────    ──────────          ──────                 ─────              ──────
 0      Connected           Running                Running            Connected
        epoch=42            epoch=42               epoch=42           lastEventId=evt:5
                                                   mem: [evt:1-5]    cache: [evt:1-5]
                                                   sqlite: [evt:1-5]

 1      User sends          Broadcasts to          Stores in          Receives
        message             relay                  mem + sqlite       Applies to UI
        text event          (evt:6)                (evt:6 added)      lastEventId=evt:6

 2                          Token refreshes        Still running       Connected
                            epoch=43               epoch=42           (old token)

 3      Reconnects          Authority epoch        Idle                Disconnects
        with new token      updated to 43
        auth=43

 4                          Calls relayRecovery
                            Plan() [line 948]

 5                          info.AuthorityEpoch=42
                            currentAuth=43
                            ❌ MISMATCH!
                            (line 954)

 6                          Line 955-956: guard
                            events empty? NO
                            (has 6 events)
                            → falls through

 7                          Line 958:
                            reset = HistoryCount > 0
                            → reset = TRUE

 8                          Sends active_session
                            mode=replace_history
                            (line 837)

 9                                                 Receives active_session
                                                   line 527:
                                                   bindRoomSession(
                                                     sid, epoch=43,
                                                     replace=true)

 10                                                Line 733: room.sessionID = sid
                                                   Line 734: authorityChanged = true
                                                   Line 735: clearHistoryLocked()
                                                   mem history = nil ❌
                                                   [evt:1-6 DELETED from RAM]
                                                   SQLite: still has [evt:1-6] ✓

 11                                                hydrateLocked() CANNOT fire
                                                   because sessionID != ""
                                                   (line 120 guard)
                                                   → SQLite data stranded

 12                                                                    Reconnects
                                                                      auth=43
                                                                      cursor=evt:6

 13                                                prepareResumeLocked()
                                                   line 638-639:
                                                   eventsAfter(evt:6)
                                                   Queries nil history
                                                   Returns []

 14                                                Sends resume_ack
                                                   replay_count=0
                                                   line 653

 15                         Receives              resume_ack
        [Desktop            resume_ack           received
        has local copy]     (no replay expected)

 16                                                                    resume_ack
                                                                      replayCount=0
                                                                      line 598
                                                                      Marks ready
                                                                      line 614-618

 17                                                                    Dedup check:
                                                                      _shouldApplyEvent
                                                                      line 1437:
                                                                      ord <= last → skip

 18     Message visible     [Lost state]          SQLite has data   ❌ BLANK
        (from local)        (reset worked)        but can't load    CHAT
                                                   (hydrate guard)
```

---

## The Bug: Three Layers, One Outcome

```go
// Layer 1: broker.go:954-958 — overly aggressive reset trigger
if info.AuthorityEpoch == 0 || info.AuthorityEpoch != currentAuthority {
    if len(events) == 0 && info.HistoryCount == 0 {
        return relayRecoveryPlan{trusted: true}, events  // line 956: guard for empty
    }
    return relayRecoveryPlan{reset: info.HistoryCount > 0, replayFrom: 0}, events  // line 958
}
// ^ Authority epoch mismatch alone triggers reset when HistoryCount > 0

// Layer 2: relay.go:733-735 — clearHistoryLocked sets session BEFORE wiping
p.room.sessionID = sessionID              // line 733 — set first!
if changed || replaceHistory || authorityChanged {
    p.room.clearHistoryLocked()           // line 735 — wipe in-memory
}
// ^ sessionID is now set, blocking hydrateLocked()

// Layer 3: relay.go:120-121 — hydrate guard prevents SQLite recovery
func (r *room) hydrateLocked(state persistedRoomState) (bool, int) {
    if r.sessionID != "" || len(r.history) != 0 {
        return false, 0  // ← blocks recovery! sessionID already set
    }
    // ...
}
// ^ SQLite has the data but hydrateLocked refuses to load it

// Layer 4: relay.go:113-116 — the actual memory wipe
func (r *room) clearHistoryLocked() {
    r.history = nil          // ← RAM cleared, SQLite untouched
    r.bootstrap = make(map[string]roomEvent)
    r.lastEventAt = time.Time{}
}
```

**Why the SQLite layer doesn't save us**: Even though `persistEvent()` faithfully
writes every event to SQLite, and `loadRoom()` can read them back, the
`hydrateLocked()` function at line 120 guards against rehydration when
`sessionID != ""`. Since `bindRoomSession()` sets `sessionID` at line 733
*before* calling `clearHistoryLocked()` at line 735, the hydrate guard always
blocks recovery.

---

## Layers That Work Correctly

```
✅ WORKING CORRECTLY:

1. STORAGE — SQLite (store.go:227-280)
   └─ All message types persisted to relay_events table
   └─ UPSERT dedup by (token_hash, session_id, event_id)
   └─ Survives relay restart
   └─ NOT cleared by clearHistoryLocked()

2. STORAGE — In-memory (relay.go:98-111)
   └─ appendEvent() with tail-50 dedup check
   └─ All event types stored
   └─ Cleared by clearHistoryLocked() (but SQLite survives)

3. TRANSMISSION (relay.go:778-791)
   └─ relayToOthers() broadcasts to all peers
   └─ Delivery is best-effort with write deadline

4. MOBILE RECEIPT (connection_provider.dart)
   └─ Events received and applied to UI
   └─ Dedup logic working (line 1404-1470)
   └─ Ordinal gap detection (line 1452)
   └─ Events persisted to local cache

5. MOBILE RECONNECT
   └─ Resume token preserved across reconnect
   └─ resume_from sent correctly
   └─ Large gap handling (line 1447-1451)


❌ BROKEN:

1. DECISION LOGIC (broker.go:954-958)
   └─ Authority mismatch → reset flag
   └─ Too aggressive: normal token renewal triggers it
   └─ Guard at line 955 only covers the empty-empty case

2. DELETION LOGIC (relay.go:733-735)
   └─ clearHistoryLocked wipes in-memory history
   └─ Triggered by authorityChanged OR replaceHistory

3. HYDRATION GUARD (relay.go:120-121)
   └─ SQLite has the data but can't load it
   └─ Guard checks sessionID, which was already set
   └─ Prevents recovery from SQLite after wipeout
```

---

## Fix Options

```
┌─────────────────────────────────────────────────────────────────┐
│ FIX OPTION A: Don't trigger reset on epoch mismatch alone      │
│                                                                 │
│ broker.go:954-958                                               │
│ Remove epoch mismatch as sole trigger for reset.                │
│ Require additional evidence (hash/count mismatch).              │
│                                                                 │
│ Risk: Low — other validation paths still active                 │
│ Impact: Prevents the wipeout entirely                           │
└─────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────┐
│ FIX OPTION B: Allow hydrate after clearHistoryLocked            │
│                                                                 │
│ relay.go:120-121 — relax the guard:                             │
│   Allow hydrateLocked() to fire when history is nil,            │
│   even if sessionID is already set (post-clear state).          │
│                                                                 │
│ relay.go:733-735 — reorder:                                     │
│   Clear history AFTER (or instead of) setting session,          │
│   or add explicit rehydrate call after clear.                   │
│                                                                 │
│ Risk: Medium — must ensure no double-hydrate race              │
│ Impact: SQLite becomes the recovery source after wipeout        │
└─────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────┐
│ FIX OPTION C: Don't clear in-memory history at all             │
│                                                                 │
│ relay.go:734-736 — remove clearHistoryLocked() call             │
│   when authorityChanged.                                        │
│   The epoch update alone doesn't invalidate the events.         │
│                                                                 │
│ Risk: Low-Medium — stale events from old session?              │
│ Impact: History always available for replay                     │
└─────────────────────────────────────────────────────────────────┘

RECOMMENDED: Option A (root cause) + Option B (defense in depth)
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
│ CURRENT:                            │
│ - reset = true (line 958) ✗         │
│ - history WIPED ✗                   │
│                                     │
│ AFTER FIX A:                        │
│ - reset = false ✓                   │
│ - history preserved ✓               │
└─────────────────────────────────────┘

TEST 2: Hash Mismatch Still Resets
┌─────────────────────────────────────┐
│ 1. Relay has history: [evt1-5]      │
│ 2. Authority epoch SAME             │
│ 3. Projection hash DIFFERS          │
│ 4. Broker calls relayRecoveryPlan() │
│                                     │
│ EXPECTED (all scenarios):           │
│ - reset = true (line 977) ✓         │
│ - history WIPED                     │
│ (Still works for real corruptions)  │
└─────────────────────────────────────┘

TEST 3: SQLite Recovery After Wipeout
┌─────────────────────────────────────┐
│ 1. Events persisted to SQLite       │
│ 2. clearHistoryLocked() fires       │
│ 3. New client connects              │
│ 4. hydrateLocked() called           │
│                                     │
│ CURRENT:                            │
│ - Guard blocks (sessionID set) ✗    │
│ - Returns empty history ✗           │
│                                     │
│ AFTER FIX B:                        │
│ - Guard relaxed for nil history ✓   │
│ - SQLite data loaded ✓              │
│ - replay_count > 0 ✓                │
└─────────────────────────────────────┘

TEST 4: Mobile Replay After Renewal
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
│ AFTER FIX A+B:                      │
│ - replay_count > 0 ✓                │
│ - Message visible ✓                 │
└─────────────────────────────────────┘
```

---

## Line Number Reference (verified 2026-06-20)

| Component | File | Function | Line |
|-----------|------|----------|------|
| **Broker** | `internal/tunnel/broker.go` | `relayRecoveryPlan()` | 948-987 |
| | | epoch mismatch check | 954 |
| | | empty-empty guard | 955-956 |
| | | reset trigger | 958 |
| | | hash mismatch → reset | 977 |
| | | sendActiveSessionWithMode(replace) | 837 |
| | | `relayRecoveryPlan` struct | 918 |
| **Relay** | `ggcode-relay/relay.go` | `appendEvent()` (dedup) | 98-111 |
| | | `clearHistoryLocked()` | 113-117 |
| | | `hydrateLocked()` | 119-140 |
| | | hydrate guard (sessionID check) | 120-121 |
| | | `eventsAfter()` | 201-218 |
| | | active_session handler | 527 |
| | | `prepareResumeLocked()` | 638-659 |
| | | `bindRoomSession()` | 710-740 |
| | | session set before clear | 733 |
| | | clearHistoryLocked call | 735 |
| | | `hydrateRoomFromStore()` | 742-754 |
| **Store** | `ggcode-relay/store.go` | `loadRoom()` | 174-225 |
| | | `persistEvent()` | 227-280 |
| | | `persistActiveSession()` | 282+ |
| **Mobile** | `connection_provider.dart` | resume_ack handler | 590-630 |
| | | replayCount parse | 598 |
| | | zero-replay ready check | 614-618 |
| | | `_shouldApplyEvent()` | 1404-1470 |
| | | dedup: recentEventSet | 1422 |
| | | dedup: ordinal check | 1437 |
| | | gap detection | 1452 |
| | | `_pendingReplayCount` set | 2086-2091 |
