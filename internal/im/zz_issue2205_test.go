package im

import (
	"context"
	"strings"
	"testing"
)

// #2205: the daemon bridge must mirror the TUI-side im.remote_dangerous_commands
// gate (#2185/PR #2203) - shell passthrough and mode escalation from a remote
// IM peer are hard-denied without the opt-in (fail closed).

func TestIssue2205DaemonShellPassthroughGated(t *testing.T) {
	em := &captureEmitter{}
	b := &DaemonBridge{emitTextOverride: em.EmitText} // no override: fail closed

	if err := b.SubmitInboundMessage(context.Background(), InboundMessage{Text: "$ printf leak-2205"}); err != nil {
		t.Fatalf("SubmitInboundMessage: %v", err)
	}
	// The refusal notice must be pushed; the command must NOT run.
	found := false
	for i := 0; i < 50 && !found; i++ {
		em.mu.Lock()
		texts := append([]string(nil), em.texts...)
		em.mu.Unlock()
		for _, tx := range texts {
			if strings.Contains(tx, "remote_dangerous_commands") {
				found = true
			}
			if strings.Contains(tx, "leak-2205") {
				t.Fatalf("gate closed: shell command executed anyway")
			}
		}
		sleepMs(20)
	}
	if !found {
		t.Fatal("gate closed: expected the opt-in refusal notice")
	}
}

func TestIssue2205DaemonShellPassthroughAllowedWithOptIn(t *testing.T) {
	em := &captureEmitter{}
	allow := true
	b := &DaemonBridge{emitTextOverride: em.EmitText, remoteDangerousOverride: &allow}

	if err := b.SubmitInboundMessage(context.Background(), InboundMessage{Text: "$ printf ok-2205"}); err != nil {
		t.Fatalf("SubmitInboundMessage: %v", err)
	}
	for i := 0; i < 150; i++ {
		em.mu.Lock()
		texts := append([]string(nil), em.texts...)
		em.mu.Unlock()
		for _, tx := range texts {
			if strings.Contains(tx, "ok-2205") {
				return // pass: executed under the opt-in
			}
			if strings.Contains(tx, "remote_dangerous_commands") {
				t.Fatal("gate open: refusal sent anyway")
			}
		}
		sleepMs(20)
	}
	t.Fatal("opt-in open: shell command never produced a result push")
}

func TestIssue2205RemoteDangerousAllowedSemantics(t *testing.T) {
	allow := true
	if !(&DaemonBridge{remoteDangerousOverride: &allow}).remoteDangerousAllowed() {
		t.Fatal("override true must allow")
	}
	deny := false
	if (&DaemonBridge{remoteDangerousOverride: &deny}).remoteDangerousAllowed() {
		t.Fatal("override false must deny")
	}
	// no override, workingDir without an instance config -> fail closed
	if (&DaemonBridge{}).remoteDangerousAllowed() {
		t.Fatal("nil config must fail closed")
	}
	// SwitchMode escalation gate is upstream of the agent lookup only in
	// the no-agent case; the mode-name validation runs first for garbage.
	err := (&DaemonBridge{}).SwitchMode("not-a-mode")
	if err == nil || !strings.Contains(err.Error(), "unknown permission mode") {
		t.Fatalf("garbage mode name must error with validation message, got %v", err)
	}
}

// R82 acceptance of #2185 flagged the missing direct cases: SwitchMode
// escalation to bypass refused (gate closed) and passed (opt-in), not
// just the garbage-name validation path above. The refusal happens
// before the agent lookup, so no live agent is needed.
func TestIssue2185DaemonSwitchModeEscalationGate(t *testing.T) {
	// Gate closed (no override, no instance config): valid escalation
	// target must be refused by the opt-in gate, not by "no agent".
	err := (&DaemonBridge{}).SwitchMode("bypass")
	if err == nil || !strings.Contains(err.Error(), "remote_dangerous_commands") {
		t.Fatalf("escalation without opt-in must hit the gate, got %v", err)
	}
	// Gate open (override true): the gate must pass - the call then fails
	// at the agent lookup with a DIFFERENT error, proving the gate let it
	// through.
	allow := true
	err = (&DaemonBridge{remoteDangerousOverride: &allow}).SwitchMode("bypass")
	if err == nil || !strings.Contains(err.Error(), "no agent attached") {
		t.Fatalf("opt-in must open the gate and reach the agent lookup, got %v", err)
	}
	// Ungated level switch: even with the gate closed, a non-escalation
	// mode must NOT be refused by the gate (same downstream error).
	err = (&DaemonBridge{}).SwitchMode("auto")
	if err == nil || !strings.Contains(err.Error(), "no agent attached") {
		t.Fatalf("non-escalation switch must not be gated, got %v", err)
	}
}
