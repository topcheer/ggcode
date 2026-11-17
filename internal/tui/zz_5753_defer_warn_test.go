package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/chat"
)

// #5753: the defer-rearm loop was silent (debug-only), so an armed restart
// behind an active turn looked like "OK: restart armed" while the exec never
// fired. The loop must surface a throttled restart-defer system line.

func deferLineCount(m *Model) int {
	n := 0
	for i := 0; i < m.chatList.Len(); i++ {
		if strings.Contains(stripAnsiCodes(m.chatList.ItemAt(i).Render(200)), "waiting for the active turn") {
			n++
		}
	}
	return n
}

func Test5753DeferWarnFiresOnceThenThrottles(t *testing.T) {
	m := &Model{
		loading:            true,
		pendingRestart:     true,
		restartArmedAt:     time.Now().Add(-45 * time.Second),
		restartDeferWarnAt: time.Time{},
		lastTurnActivityAt: time.Now(), // turn actively running
		chatList:           chat.NewList(80, 20),
	}

	m.handleRestartFallbackMsg(restartFallbackMsg{})

	if deferLineCount(m) != 1 {
		t.Fatalf("first defer must emit exactly one restart-defer line, got %d", deferLineCount(m))
	}

	// Second pass inside the throttle window: no new restart-defer line.
	m.handleRestartFallbackMsg(restartFallbackMsg{})
	if deferLineCount(m) != 1 {
		t.Fatalf("defer warning must be throttled within %s, got %d lines", restartDeferWarnEvery, deferLineCount(m))
	}

	// Outside the throttle window: emits again.
	m.restartDeferWarnAt = time.Now().Add(-restartDeferWarnEvery - time.Second)
	m.handleRestartFallbackMsg(restartFallbackMsg{})
	if deferLineCount(m) != 2 {
		t.Fatalf("defer warning must re-emit after the throttle window, got %d lines", deferLineCount(m))
	}
}

func Test5753IdleTurnStillFiresImmediately(t *testing.T) {
	m := &Model{
		loading:            false,
		pendingRestart:     true,
		restartArmedAt:     time.Now(),
		lastTurnActivityAt: time.Time{},
		chatList:           chat.NewList(80, 20),
	}
	_, cmd := m.handleRestartFallbackMsg(restartFallbackMsg{})
	if cmd == nil {
		t.Fatal("idle turn must fire the pending restart immediately (non-nil cmd)")
	}
}
