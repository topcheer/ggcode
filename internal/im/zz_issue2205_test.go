package im

import (
	"context"
	"strings"
	"testing"
)

func TestIssue2205DaemonShellPassthroughGated(t *testing.T) {
	em := &captureEmitter{}
	b := &DaemonBridge{emitTextOverride: em.EmitText}

	if err := b.SubmitInboundMessage(context.Background(), InboundMessage{Text: "$ printf leak-2205"}); err != nil {
		t.Fatalf("SubmitInboundMessage: %v", err)
	}

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
				return
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

	if (&DaemonBridge{}).remoteDangerousAllowed() {
		t.Fatal("nil config must fail closed")
	}

	err := (&DaemonBridge{}).SwitchMode("not-a-mode")
	if err == nil || !strings.Contains(err.Error(), "unknown permission mode") {
		t.Fatalf("garbage mode name must error with validation message, got %v", err)
	}
}

func TestIssue2185DaemonSwitchModeEscalationGate(t *testing.T) {

	err := (&DaemonBridge{}).SwitchMode("bypass")
	if err == nil || !strings.Contains(err.Error(), "remote_dangerous_commands") {
		t.Fatalf("escalation without opt-in must hit the gate, got %v", err)
	}

	allow := true
	err = (&DaemonBridge{remoteDangerousOverride: &allow}).SwitchMode("bypass")
	if err == nil || !strings.Contains(err.Error(), "no agent attached") {
		t.Fatalf("opt-in must open the gate and reach the agent lookup, got %v", err)
	}

	err = (&DaemonBridge{}).SwitchMode("auto")
	if err == nil || !strings.Contains(err.Error(), "no agent attached") {
		t.Fatalf("non-escalation switch must not be gated, got %v", err)
	}
}
