package tui

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// #5367: the restart fallback must not livelock behind a continuously
// active turn. Three behavior pins + the zero-arm guard.

func runFallbackMsg(t *testing.T, m Model) tea.Msg {
	t.Helper()
	out, cmd := m.handleRestartFallbackMsg(restartFallbackMsg{})
	if cmd == nil {
		t.Fatal("handleRestartFallbackMsg returned nil command")
	}
	_ = out
	return cmd()
}

func TestIssue5367FallbackReArmsWhileTurnActive(t *testing.T) {
	m := Model{}
	m.pendingRestart = true
	m.loading = true
	now := time.Now()
	m.restartArmedAt = now
	m.lastTurnActivityAt = now.Add(-time.Second) // active recently
	msg := runFallbackMsg(t, m)
	if _, ok := msg.(restartFallbackMsg); !ok {
		t.Fatalf("want re-arm restartFallbackMsg, got %T", msg)
	}
}

func TestIssue5367FallbackHardCapFiresBehindContinuousTurn(t *testing.T) {
	m := Model{}
	m.pendingRestart = true
	m.loading = true
	now := time.Now()
	m.restartArmedAt = now.Add(-restartArmHardCap - time.Minute) // armed long ago
	m.lastTurnActivityAt = now.Add(-time.Second)                 // turn still active
	msg := runFallbackMsg(t, m)
	if _, ok := msg.(remoteRestartMsg); !ok {
		t.Fatalf("want hard-cap remoteRestartMsg, got %T", msg)
	}
}

func TestIssue5367FallbackFiresWhenTurnIdle(t *testing.T) {
	m := Model{}
	m.pendingRestart = true
	m.loading = true
	now := time.Now()
	m.restartArmedAt = now.Add(-time.Minute)
	m.lastTurnActivityAt = now.Add(-2 * restartFallbackTimeout) // stalled turn
	msg := runFallbackMsg(t, m)
	if _, ok := msg.(remoteRestartMsg); !ok {
		t.Fatalf("want remoteRestartMsg on idle turn, got %T", msg)
	}
}

func TestIssue5367ZeroArmedAtDoesNotHardFire(t *testing.T) {
	// Legacy arm paths that never set restartArmedAt must keep the old
	// re-arm behavior, not instantly hard-fire off the zero value.
	m := Model{}
	m.pendingRestart = true
	m.loading = true
	m.restartArmedAt = time.Time{}
	m.lastTurnActivityAt = time.Now().Add(-time.Second)
	msg := runFallbackMsg(t, m)
	if _, ok := msg.(restartFallbackMsg); !ok {
		t.Fatalf("want re-arm for zero armedAt, got %T", msg)
	}
}

func TestIssue5367ArmSetsArmedAt(t *testing.T) {
	m := Model{}
	before := time.Now()
	m.handleArmRestartMsg(armRestartMsg{})
	if m.restartArmedAt.IsZero() || m.restartArmedAt.Before(before.Add(-time.Second)) {
		t.Fatalf("handleArmRestartMsg must stamp restartArmedAt, got %v", m.restartArmedAt)
	}
}
