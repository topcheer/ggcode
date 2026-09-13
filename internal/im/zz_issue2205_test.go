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
