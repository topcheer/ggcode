package tui

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/im"
)

// #4587: every path out of waitForResponse must return - no goroutine may
// block forever when the TUI program is gone and the caller holds a
// non-cancellable context.
func TestIssue4587_WaitForResponseDetachReturns(t *testing.T) {
	b := newTUIIMBridge(nil)
	resp := make(chan error, 1) // never written - simulates dead program

	b.detach()

	start := time.Now()
	err := b.waitForResponse(context.Background(), resp)
	if err == nil {
		t.Fatal("expected error from detached bridge, got nil")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("detach branch took %s; want immediate return", elapsed)
	}
}

func TestIssue4587_WaitForResponseRespWins(t *testing.T) {
	b := newTUIIMBridge(nil)
	resp := make(chan error, 1)
	sentinel := errors.New("handled")
	resp <- sentinel

	if err := b.waitForResponse(context.Background(), resp); err != sentinel {
		t.Fatalf("want sentinel error, got %v", err)
	}
}

func TestIssue4587_WaitForResponseTimeoutReturns(t *testing.T) {
	old := tuiBridgeSubmitTimeout
	tuiBridgeSubmitTimeout = 50 * time.Millisecond
	t.Cleanup(func() { tuiBridgeSubmitTimeout = old })

	b := newTUIIMBridge(nil)
	resp := make(chan error, 1) // never written

	start := time.Now()
	err := b.waitForResponse(context.Background(), resp)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("timeout branch took %s; want ~50ms", elapsed)
	}
}

func TestIssue4587_WaitForResponseCtxCancelReturns(t *testing.T) {
	b := newTUIIMBridge(nil)
	resp := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(30 * time.Millisecond)
		cancel()
	}()

	if err := b.waitForResponse(ctx, resp); !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
}

// A detached bridge must reject new submits before touching the program.
func TestIssue4587_DetachedBridgeRejectsSubmit(t *testing.T) {
	b := newTUIIMBridge(nil)
	b.detach()
	if err := b.SubmitInboundMessage(context.Background(), im.InboundMessage{}); err == nil {
		t.Fatal("detached bridge must return an error on submit")
	}
}

// Detach is idempotent: the second call must not panic (double close).
func TestIssue4587_DetachIdempotent(t *testing.T) {
	b := newTUIIMBridge(nil)
	b.detach()
	b.detach() // must not panic (double close)
	if !b.isDetached() {
		t.Fatal("bridge should report detached after detach()")
	}
}
