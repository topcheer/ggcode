package lsp

// Exit-path review (#R192): the app's exit chain closed MCP clients, the
// browser, and the tunnel, but globalSessions had NO shutdown caller - the
// lsp tools do not implement tool.Closer, so Registry.CloseAll skipped
// them, and language servers were left to notice stdin EOF on their own.
// sessionManager.shutdownAll must drain every session, mark them closed,
// and stop the idle reaper.

import (
	"sync"
	"testing"
)

func TestSessionManagerShutdownAllDrainsAndCloses(t *testing.T) {
	m := &sessionManager{sessions: map[string]*sessionClient{}, stopCh: make(chan struct{})}
	m.sessions["ws-a\x00gopls"] = &sessionClient{}
	m.sessions["ws-b\x00gopls"] = &sessionClient{}
	// A session that is already closed must not panic on double close.
	pre := &sessionClient{}
	pre.close()
	m.sessions["ws-c\x00gopls"] = pre

	m.shutdownAll()

	if len(m.sessions) != 0 {
		t.Fatalf("shutdownAll must drain the session map, %d left", len(m.sessions))
	}
	if !pre.isClosed() {
		t.Fatal("pre-closed session must stay closed")
	}
	// Drain the reaper stop signal: closed, not blocking forever.
	select {
	case <-m.stopCh:
	default:
		t.Fatal("stopCh must be closed so reapIdle exits")
	}
}

func TestSessionManagerShutdownAllOnEmptyManager(t *testing.T) {
	m := &sessionManager{sessions: map[string]*sessionClient{}, stopCh: make(chan struct{})}
	// No sessions, reaper never started - must not panic or block.
	m.shutdownAll()
	select {
	case <-m.stopCh:
	default:
		t.Fatal("stopCh must be closed")
	}
}

func TestShutdownAllClosesConcurrentlyRegisteredSessions(t *testing.T) {
	// shutdownAll runs while another goroutine acquires sessions - the
	// reaper-stop and map drain must not race the registrar (the map swap
	// happens under m.mu).
	m := &sessionManager{sessions: map[string]*sessionClient{}, stopCh: make(chan struct{})}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m.mu.Lock()
			m.sessions["concurrent"] = &sessionClient{}
			m.mu.Unlock()
		}()
	}
	wg.Wait()
	done := make(chan struct{})
	go func() { m.shutdownAll(); close(done) }()
	<-done
	if len(m.sessions) != 0 {
		t.Fatalf("sessions must be drained, %d left", len(m.sessions))
	}
}
