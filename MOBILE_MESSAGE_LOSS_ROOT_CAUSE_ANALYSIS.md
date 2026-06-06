# Mobile Message Loss Root Cause Analysis

## Executive Summary

**Critical Bug**: Mobile clients lose all live text messages when reconnecting after token renewal or session recovery.

**Root Cause**: Authority epoch mismatch between broker and relay triggers an aggressive "reset history" decision that **completely wipes all message history** in the relay, including live text messages that mobile clients need for display.

**Impact**: 
- Users see messages disappear when they reconnect
- Affects both token renewal and session recovery scenarios
- Happens ~100% when authority epoch changes
- Silent failure (no error indication to user)

**Severity**: CRITICAL - Data loss (message history)

---

## The Bug: Three-Part Cascade

### Part 1: Broker Detects Authority Epoch Mismatch (`internal/tunnel/broker.go:869-873`)

```go
func (b *Broker) relayRecoveryPlan(info RelayConnectedState, currentSessionID string) (relayRecoveryPlan, []GatewayMessage) {
    // ...
    currentAuthority := b.AuthorityEpoch()
    if info.AuthorityEpoch == 0 || info.AuthorityEpoch != currentAuthority {  // LINE 869
        if len(events) == 0 && info.HistoryCount == 0 {
            return relayRecoveryPlan{trusted: true}, events
        }
        return relayRecoveryPlan{reset: info.HistoryCount > 0, replayFrom: 0}, events  // LINE 873: SET RESET FLAG
    }
```

**What happens**: 
- Broker compares its authority epoch with relay's stored authority epoch
- On authority mismatch (happens during token renewal), sets `reset: true` if relay has ANY history
- The decision is **binary and irreversible**: either trust the relay completely, or wipe it entirely

**Why this triggers**:
- Token renewal creates a new authority epoch in the broker
- Relay still has the old epoch from previous session
- Comparison fails → reset flag set

### Part 2: Broker Sends "Replace History" Mode to Relay (`internal/tunnel/broker.go:752-753`)

```go
if plan.reset {
    b.sendActiveSessionWithMode(currentSessionID, ActiveSessionModeReplaceHistory)  // LINE 753
} else {
    b.sendActiveSession(currentSessionID)
}
```

**What happens**:
- Broker sends `active_session` message with mode `"replace_history"`
- Relay interprets this as: "The authoritative state has changed, clear your history"

### Part 3: Relay Executes History Wipeout (`ggcode-relay/relay.go:693-694`)

```go
func (p *peer) bindRoomSession(sessionID string, authorityEpoch uint64, replaceHistory bool) {
    // ...
    changed = p.room.sessionID != sessionID
    authorityChanged := p.room.authorityEpoch != 0 && p.room.authorityEpoch != authorityEpoch
    p.room.sessionID = sessionID
    if changed || replaceHistory || authorityChanged {  // LINE 693
        p.room.clearHistoryLocked()  // LINE 694: DELETES ALL EVENTS
    }
```

**What happens in `clearHistoryLocked()`** (relay.go:110-114):
```go
func (r *room) clearHistoryLocked() {
    r.history = nil              // ALL EVENTS DELETED
    r.bootstrap = make(map[string]roomEvent)  // Bootstrap events deleted
    r.lastEventAt = time.Time{}
}
```

- **Entire message history is deleted, including**:
  - Live text messages
  - Tool call transcripts
  - Agent responses
  - All metadata

---

## Timeline: How Users Lose Messages

1. **User is connected**: Mobile app receiving live messages, storing them in local cache
   - Text events flowing through relay
   - Mobile tracking `_lastAppliedEventId` (e.g., `event:123`)

2. **Token expires/refreshes**: New token issued with different authority epoch
   - Authority epoch changes from `epoch:42` to `epoch:43`
   - Desktop client connects with new epoch
   - Broker detects mismatch with relay's stored epoch

3. **Broker decides to reset** (broker.go:873)
   - Sets `reset: true` because relay has history
   - Broadcasts `active_session` with `mode: replace_history`

4. **Relay wipes history** (relay.go:694)
   - `clearHistoryLocked()` executes
   - `room.history = nil` - ALL EVENTS GONE
   - Room now has **zero events**

5. **Mobile reconnects with new token**: Sends `resume_from` with old cursor
   ```
   resume_from(client_id: X, cursor: event:123, authority_epoch: 43)
   ```

6. **Relay prepares replay** (relay.go:598)
   ```go
   replay := p.room.eventsAfter(p.cursor)  // Queries empty history
   ```
   - Calls `eventsAfter("event:123")`
   - History is empty → returns empty list
   - `replay_count` = 0

7. **Mobile receives zero replay** (connection_provider.dart:442)
   ```dart
   final replayCount = (msg.data?['replay_count'] as num?)?.toInt() ?? 0;
   ```
   - Client receives `resume_ack` with `replay_count: 0`
   - Expects no historical events
   - But local cache still has the old messages!

8. **Local cache becomes stale**: Messages from before reconnection are not re-applied
   - Dedup logic (connection_provider.dart:1231) prevents applying same event twice
   - Old messages are never replayed to UI
   - **User sees blank chat**

---

## Evidence of Message Storage

Live text messages ARE stored in relay (relay.go:440-454):

```go
func (p *peer) handleServerBroadcast(msg relayMessage) {
    // ...
    ev := roomEvent{
        sessionID: msg.SessionID,
        eventID:   msg.EventID,
        streamID:  msg.StreamID,
        eventHash: msg.EventHash,
        typ:       "encrypted",    // All broadcasts stored as "encrypted"
        raw:       append([]byte(nil), wire...),
    }
    p.room.appendEvent(ev)  // Added to history
    // ...
}
```

- **All message types** (text, tool call, response, etc.) are stored with type `"encrypted"`
- All stored in `room.history` array
- No filtering by message type occurs

**BUT**: When history is cleared (line 694), ALL events are deleted indiscriminately.

---

## The Decision Logic Problem

The real issue is in `relayRecoveryPlan()` (broker.go:869-873):

```go
if info.AuthorityEpoch == 0 || info.AuthorityEpoch != currentAuthority {
    // Authority mismatch detected
    if len(events) == 0 && info.HistoryCount == 0 {
        return relayRecoveryPlan{trusted: true}, events  // Both empty, trust relay
    }
    return relayRecoveryPlan{reset: info.HistoryCount > 0, replayFrom: 0}, events
    //                        ^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^
    //                        If relay has ANY history, WIPE IT ALL
}
```

**The problem**: This treats authority epoch mismatch as evidence of **complete projection inconsistency**, requiring total reset.

**But reality**: Authority epoch change doesn't mean the relay's history is wrong!
- It just means the authority changed (token renewal, session restart)
- Relay may have correct, valid history that's still useful
- **No verification** that the history is actually corrupted or inconsistent
- **No preservation** of live messages before wiping

---

## Why This Breaks Mobile Specifically

### Desktop (Gets Masked)
- Desktop has full replay of broker's canonical state
- After reset, desktop replays all its current events
- User sees seamless experience (no gaps)
- (broker.go:760-765 replays from broker's local copy)

### Mobile (Gets Nothing)
- Mobile doesn't have full session state
- Mobile relies entirely on relay replay
- After reset, relay has empty history
- Mobile's `resume_ack` says `replay_count: 0`
- **Mobile receives nothing to re-apply**
- Messages that were there before are still in local cache but not re-applied to UI

### Tool Call Rounds (Also Affected)
- Tool call text, results, and status updates are all stored as "encrypted" events
- When history is wiped, tool call transcripts are lost
- Mobile reconnecting mid-tool-call sees blank tool UI

---

## Where Messages Are NOT Being Filtered

### Selection (`eventsAfter()`)
- relay.go:598 - returns ALL stored events (no filtering)
- Issue is that history is already empty, not that it's being filtered

### Storage (`handleServerBroadcast()`)
- relay.go:454 - stores ALL message types with type="encrypted"
- No discrimination between text, tool, response, etc.
- Issue is deletion, not selective recording

### Transmission (`finishResume()`)
- relay.go:632-635 - sends ALL events in replay list
- Issue is list is empty (history was cleared), not transmission

**Conclusion**: This is NOT a filtering issue—it's a **wipeout issue**.

---

## Code Locations Summary

| Component | File | Lines | Function | Issue |
|-----------|------|-------|----------|-------|
| **Broker** | `internal/tunnel/broker.go` | 869-873 | `relayRecoveryPlan()` | Sets `reset=true` on authority epoch mismatch |
| **Broker** | `internal/tunnel/broker.go` | 752-753 | `handleRelayConnected()` | Sends `replace_history` mode when reset=true |
| **Relay** | `ggcode-relay/relay.go` | 501 | `onActiveSession()` | Passes `replaceHistory=true` to bindRoomSession |
| **Relay** | `ggcode-relay/relay.go` | 693-694 | `bindRoomSession()` | Calls `clearHistoryLocked()` when authorityChanged |
| **Relay** | `ggcode-relay/relay.go` | 110-114 | `clearHistoryLocked()` | **DELETES ALL EVENTS** |
| **Relay** | `ggcode-relay/relay.go` | 598 | `eventsAfter()` | Returns empty (history was cleared) |
| **Mobile** | `connection_provider.dart` | 412-419 | `active_session` handler | Clears local cursor on session change |
| **Mobile** | `connection_provider.dart` | 434-452 | `resume_ack` handler | Receives `replay_count: 0`, expects no replay |
| **Mobile** | `connection_provider.dart` | 1229-1233 | `_shouldApplyEvent()` | Dedup prevents re-applying old events |

---

## Why This Bug Is Hard to Detect

1. **Silent failure**: No error logged, no user indication
2. **Timing dependent**: Requires token renewal + reconnect sequence
3. **Works on desktop**: Desktop masks the problem with its own replay
4. **Partial success**: Mobile connects successfully, just has no message history
5. **Dedup hides it**: Dedup logic makes it LOOK like events are being processed normally

---

## Affected Scenarios

1. ✅ **Token renewal during active session**
   - Most common case
   - User disconnects/reconnects with new token
   - Authority epoch changes
   - Message history wiped

2. ✅ **Session recovery after app crash**
   - Mobile resumes with new token
   - Authority epoch changed since previous session
   - History lost

3. ✅ **Multi-device reconnect**
   - Switching from desktop to mobile
   - Different device has different authority epoch tracking
   - History cleared

4. ❌ **Fresh session start**
   - No authority mismatch (both start at 0)
   - History not cleared

5. ❌ **Same token, same epoch**
   - Authority match
   - History preserved

---

## Impact Assessment

### User-Visible Impact
- Lost message history on mobile reconnect
- Blank chat window after token renewal
- Tool call transcripts disappear mid-execution
- No error message (silent failure)

### Silent Data Loss
- All relay history deleted
- Cannot be recovered (stored in RAM, not persisted)
- Affects all mobile clients connected to that relay at that moment

### Frequency
- **High**: Every token renewal or session recovery triggers this code path
- **Chance of wipeout**: ~100% when authority epoch mismatches

---

## Fix Strategy Options

### Option 1: Don't Reset History on Authority Mismatch Alone (RECOMMENDED)
**Problem**: Authority epoch change doesn't prove history corruption

**Solution**: 
- Only set `reset: true` if there's actual projection hash mismatch
- Authority epoch mismatch alone shouldn't trigger wipeout
- Require explicit inconsistency evidence before clearing

**Code change**: broker.go:869-873
```go
// BEFORE
if info.AuthorityEpoch == 0 || info.AuthorityEpoch != currentAuthority {
    if len(events) == 0 && info.HistoryCount == 0 {
        return relayRecoveryPlan{trusted: true}, events
    }
    return relayRecoveryPlan{reset: info.HistoryCount > 0, replayFrom: 0}, events
}

// AFTER
if info.AuthorityEpoch == 0 {
    // First time: relay has no authority epoch yet
    if len(events) == 0 && info.HistoryCount == 0 {
        return relayRecoveryPlan{trusted: true}, events
    }
    return relayRecoveryPlan{reset: info.HistoryCount > 0, replayFrom: 0}, events
}
// Authority epoch mismatch alone doesn't prove corruption
// Don't reset unless we have other evidence (hash mismatch, etc.)
if info.AuthorityEpoch != currentAuthority {
    // Continue to normal validation below
    // Don't force reset
}
```

### Option 2: Preserve Messages Before Wipeout
**Problem**: Even with reset, users lose important messages

**Solution**:
- Before calling `clearHistoryLocked()`, save "recent" messages to a secondary buffer
- After reset, restore recent messages for replay
- Ensures mobile gets at least recent history

### Option 3: Separate "Text History" from "State History"
**Problem**: All events treated the same way

**Solution**:
- Keep live text messages in a persistent buffer
- Keep projection state separately
- Only reset projection state, preserve text for replay

### Option 4: Delayed Reset with Verification
**Problem**: Reset happens immediately without verification

**Solution**:
- On authority mismatch, don't reset immediately
- Request verification from all clients about their state
- Only reset if clients report inconsistency
- Adds latency but prevents false positives

---

## Testing Strategy

### Unit Tests Needed
1. `TestRelayRecoveryPlan_AuthorityEpochMismatchPreservesHistory`
   - Verify history not reset on epoch mismatch alone
   - Verify history reset only if hash mismatch detected

2. `TestBindRoomSession_AuthorityChangedPreservesMessages`
   - Verify clearHistoryLocked not called for epoch change
   - Verify messages persist across token renewal

3. `TestMobileReplayAfterTokenRenewal`
   - Mobile reconnects after token renewal
   - Verify replay_count > 0
   - Verify all recent messages replayed

### Integration Tests Needed
1. Simulate token renewal → verify message preservation
2. Simulate authority epoch change → verify no history loss
3. Simulate mobile reconnect → verify full replay

### Manual Verification
1. Mobile app: Send messages, disconnect, reconnect with new token
2. Verify: Messages still visible in chat history
3. Verify: No gaps in conversation flow

---

## Recommended Fix (Priority: CRITICAL)

**Fix location**: `internal/tunnel/broker.go:869-873`

**Change**: Don't reset history on authority epoch mismatch alone

**Implementation**:
```go
// Authority epoch mismatch is NOT grounds for history reset
// Only reset if:
// 1. Relay has no epoch (brand new)
// 2. Projection hash mismatch (actual inconsistency detected)

if info.AuthorityEpoch == 0 {
    // First connection: relay has no epoch yet
    if len(events) == 0 && info.HistoryCount == 0 {
        return relayRecoveryPlan{trusted: true}, events
    }
    return relayRecoveryPlan{reset: info.HistoryCount > 0, replayFrom: 0}, events
}

// Authority mismatch doesn't prove corruption
// Check projection hash below (lines 890-892)
// before deciding to reset
```

**Impact**:
- ✅ Mobile users keep message history on reconnect
- ✅ No silent data loss
- ✅ Better user experience
- ⚠️ Need to verify projection hash validation still works

---

## Summary

The mobile message loss is caused by an **overly aggressive history reset policy** that wipes all relay history whenever the broker's authority epoch doesn't match the relay's stored epoch. This happens ~100% during token renewal, affecting all mobile clients trying to reconnect.

The fix is to **remove authority epoch mismatch alone as a trigger for history wipeout**, and only reset history when there's actual evidence of corruption (projection hash mismatch).

**Status**: Root cause identified, fix strategy clear, ready for implementation
