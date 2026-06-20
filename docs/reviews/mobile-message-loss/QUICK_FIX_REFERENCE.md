# Quick Fix Reference: Mobile Message Loss

## Bug Summary
- **Issue**: Mobile clients lose all message history when reconnecting after token renewal
- **Root Cause**: Authority epoch mismatch triggers aggressive history reset in relay
- **Status**: Root cause identified and verified

## Key Code Locations

### 1. THE DECISION (broker.go:869-873)
**File**: `internal/tunnel/broker.go`
**Function**: `relayRecoveryPlan()`
**Lines**: 869-873

```go
currentAuthority := b.AuthorityEpoch()
if info.AuthorityEpoch == 0 || info.AuthorityEpoch != currentAuthority {
    if len(events) == 0 && info.HistoryCount == 0 {
        return relayRecoveryPlan{trusted: true}, events
    }
    return relayRecoveryPlan{reset: info.HistoryCount > 0, replayFrom: 0}, events
    //                        ^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^
    //                        PROBLEM: Sets reset=true if relay has ANY history
}
```

**Problem**: When authority epoch changes (token renewal), automatically decides to reset entire relay history if it has ANY events.

**Decision**: `reset: true` → triggers history wipeout

### 2. THE TRIGGER (broker.go:752-753)
**File**: `internal/tunnel/broker.go`
**Function**: `handleRelayConnected()`
**Lines**: 752-753

```go
if plan.reset {
    b.sendActiveSessionWithMode(currentSessionID, ActiveSessionModeReplaceHistory)
} else {
    b.sendActiveSession(currentSessionID)
}
```

**What happens**: Broker sends `replace_history` mode to relay

### 3. THE EXECUTION (relay.go:693-694)
**File**: `ggcode-relay/relay.go`
**Function**: `bindRoomSession()`
**Lines**: 693-694

```go
if changed || replaceHistory || authorityChanged {
    p.room.clearHistoryLocked()  // ← WIPEOUT HAPPENS HERE
}
```

**What happens**: Relay clears entire message history

### 4. THE DELETION (relay.go:110-114)
**File**: `ggcode-relay/relay.go`
**Function**: `clearHistoryLocked()`
**Lines**: 110-114

```go
func (r *room) clearHistoryLocked() {
    r.history = nil  // ← ALL MESSAGES DELETED
    r.bootstrap = make(map[string]roomEvent)
    r.lastEventAt = time.Time{}
}
```

**What happens**: Complete history deletion

### 5. THE IMPACT (relay.go:598)
**File**: `ggcode-relay/relay.go`
**Function**: `prepareResumeLocked()`
**Lines**: 598

```go
replay := p.room.eventsAfter(p.cursor)
// Returns empty list because history was cleared
```

**What happens**: Mobile gets `replay_count: 0`

---

## Fix Strategy

### Primary Fix: Don't Reset on Authority Mismatch Alone

**Location**: `internal/tunnel/broker.go:869-873`

**Current behavior**:
```
authority_mismatch + relay_has_history → RESET
```

**Desired behavior**:
```
authority_mismatch_alone → don't reset
authority_mismatch + hash_mismatch → RESET
```

**Code change**:
1. Keep the `info.AuthorityEpoch == 0` case (first-time setup)
2. Remove authority epoch mismatch as sole reason for reset
3. Let normal validation (hash checking) decide reset

**Pseudo-code**:
```go
if info.AuthorityEpoch == 0 {
    // First connection, relay has no epoch
    if len(events) == 0 && info.HistoryCount == 0 {
        return relayRecoveryPlan{trusted: true}, events
    }
    return relayRecoveryPlan{reset: info.HistoryCount > 0, replayFrom: 0}, events
}
// ⚠️ CHANGE: Don't reset just because authority epoch differs
// Continue to other validation below (hash, count, etc.)
```

### Secondary Fix: Preserve Important Messages Before Wipeout

If primary fix is insufficient:
1. Before calling `clearHistoryLocked()`, snapshot last N messages
2. After wipeout, restore recent messages for replay
3. Ensures mobile always gets some message history

---

## Test Cases

### Unit Test 1: Authority Epoch Change Preserves History
```
Scenario:
  1. Relay has message history (e.g., 5 messages)
  2. Authority epoch changes (token renewal)
  3. Broker sends active_session with new authority epoch

Expected:
  - relay.room.history is NOT cleared
  - Mobile replay gets all 5 messages
  - replay_count = 5

Current behavior:
  - relay.room.history IS cleared
  - Mobile replay gets 0 messages
  - replay_count = 0
```

### Unit Test 2: Hash Mismatch Still Triggers Reset
```
Scenario:
  1. Relay has message history
  2. Authority epoch SAME (no change)
  3. Projection hash DIFFERS (actual inconsistency)

Expected:
  - relay.room.history IS cleared
  - reset: true is set
```

### Integration Test: Mobile Token Renewal
```
Steps:
  1. Mobile sends text message
  2. Desktop receives and broadcasts
  3. Relay stores message, mobile receives confirmation
  4. Token expires, user gets new token
  5. Mobile reconnects with new token
  
Expected:
  - Message visible in chat history after reconnect
  - No gaps in conversation
```

---

## Verification Checklist

- [ ] Read original PR/commit that added authority epoch reset logic
  - Why was it needed?
  - What problem did it solve?
  - Can that problem be solved differently?

- [ ] Confirm hash mismatch detection works independently
  - Verify lines 890-892 properly detect corruption
  - Verify reset works when hash differs

- [ ] Test token renewal scenario
  - Send message → token expires → reconnect → check message visibility

- [ ] Test projection hash scenario
  - Verify hash mismatch still triggers reset (independent of authority epoch)

- [ ] Load testing
  - Verify no performance impact from preserving history

---

## Related Code to Understand

### Projection Hash Validation (lines 890-892)
```go
prefixHash := ProjectionHashPrefix(events, info.HistoryCount)
if prefixHash == "" || prefixHash != strings.TrimSpace(info.ProjectionHash) {
    return relayRecoveryPlan{reset: true, replayFrom: 0}, events
}
```

**This is the real validation.** It already checks for actual inconsistency. The authority epoch check is **redundant** and **too aggressive**.

### Event Counting (lines 887-889)
```go
if info.HistoryCount > len(events) || info.LastEventID == "" {
    return relayRecoveryPlan{reset: true, replayFrom: 0}, events
}
```

**This also validates consistency.** Multiple independent checks already exist for detecting real corruption.

---

## Why This Fix Is Safe

1. **Authority epoch change alone doesn't prove corruption**
   - Token renewal just updates the authority
   - Doesn't invalidate existing message history
   - Relay's stored history is still valid and useful

2. **Hash validation already detects real corruption**
   - Lines 890-892 check projection hash
   - Lines 887-889 check history count consistency
   - Multiple safeguards exist

3. **Desktop doesn't have this problem**
   - Desktop has local copy of all events
   - Can replay them regardless of relay state
   - This fix makes mobile behavior match desktop

4. **Fallback: Hash mismatch still resets**
   - If there's REAL inconsistency, hash mismatch will catch it
   - Reset will still happen when actually needed
   - Only removes false positives

---

## Commands to Verify Fix

### Before Fix
```bash
# Send message on mobile
# Wait for delivery

# Simulate token renewal by restarting app with new token
# Check: Do you see the message in history?

# Expected (BROKEN): No message visible
```

### After Fix
```bash
# Same steps as above

# Expected (FIXED): Message is visible in history
```

---

## Risk Assessment

### Low Risk Areas
- Change is isolated to decision logic (2 lines)
- Other validation still active
- No changes to message storage or transmission
- No changes to relay protocol

### Testing Required
- Unit test for authority epoch handling
- Integration test for mobile reconnect
- Regression test for hash mismatch detection

### Rollback Plan
- Easy revert: restore original lines 869-873
- No database changes
- No protocol changes
- No client-side changes needed

---

## Implementation Checklist

- [ ] Locate and read original PR for authority epoch logic
- [ ] Create feature branch
- [ ] Implement fix in broker.go:869-873
- [ ] Write unit tests
- [ ] Write integration tests
- [ ] Test mobile reconnect scenario manually
- [ ] Run full test suite
- [ ] Code review
- [ ] Merge and deploy

---

## Success Criteria

✅ Mobile users can reconnect after token renewal and see message history  
✅ No regression in hash mismatch detection  
✅ No performance impact  
✅ New unit tests pass  
✅ Integration tests pass  
✅ Manual mobile test succeeds  

---

## Questions for Clarification

1. Why was authority epoch added as reset trigger in the first place?
2. What specific problem was it trying to solve?
3. Are there any edge cases where authority mismatch SHOULD trigger reset?
4. Is hash validation reliable enough to stand alone?

These answers will help refine the fix and ensure we don't reintroduce the original problem.

---

## Related Issues

- Mobile text reply loss during replay
- Session recovery doesn't restore message history
- Tool call rounds lose their text transcripts
- Multi-device reconnect loses history

All stem from the same root cause: aggressive history reset on authority mismatch.

---

## References

- **Root Cause Analysis**: `MOBILE_MESSAGE_LOSS_ROOT_CAUSE_ANALYSIS.md`
- **Message Flow Diagram**: `MESSAGE_FLOW_DIAGRAM.md`
- **Architecture Diagram**: `../../design/tunnel-relay-architecture.md`
- **Code Locations**: Lines referenced in this document

---

**Status**: Ready for implementation  
**Priority**: CRITICAL (data loss)  
**Estimated Effort**: 2-4 hours  
**Risk Level**: Low
