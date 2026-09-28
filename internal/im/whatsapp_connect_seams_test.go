package im

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/store/sqlstore"
)

// Pin tests for the r205 whatsapp connectAndServe lifecycle seams. They lock
// the split behavior: store-open failure reporting, client/sessionDone
// publication under a.mu, the orchestrator's first-gate error propagation,
// and the #974 close-container-before-unlink terminal path. Network paths
// (QR pairing, real connects) stay out of scope — the existing suite covers
// them via the fail-fast store-dir trick.

func TestWhatsAppOpenStoreContainerSeam(t *testing.T) {
	a := &whatsappAdapter{name: "t"}

	// Missing store dir -> open failure surfaces as an error (publishState
	// is a no-op without a manager).
	a.storeDir = filepath.Join(t.TempDir(), "missing")
	if c, err := a.openStoreContainer(context.Background()); err == nil {
		_ = c.Close()
		t.Fatal("openStoreContainer must fail for a missing store dir")
	}

	// Valid store dir -> container opens and closes cleanly.
	a.storeDir = t.TempDir()
	c, err := a.openStoreContainer(context.Background())
	if err != nil {
		t.Fatalf("openStoreContainer: %v", err)
	}
	if c == nil {
		t.Fatal("openStoreContainer must return a container on success")
	}
	if err := c.Close(); err != nil {
		t.Fatalf("container close: %v", err)
	}
}

func openTestWAStore(t *testing.T, dir string) *sqlstore.Container {
	t.Helper()
	container, err := sqlstore.New(context.Background(), "sqlite",
		"file:"+filepath.Join(dir, "whatsmeow.db")+"?_pragma=foreign_keys(1)",
		&waDebugLogger{prefix: "store"})
	if err != nil {
		t.Fatalf("sqlite store: %v", err)
	}
	return container
}

func TestWhatsAppNewSessionClientSeam(t *testing.T) {
	a := &whatsappAdapter{name: "t", storeDir: t.TempDir()}
	container := openTestWAStore(t, a.storeDir)
	defer container.Close()

	client, done, err := a.newSessionClient(context.Background(), container)
	if err != nil {
		t.Fatalf("newSessionClient: %v", err)
	}
	if client == nil || done == nil {
		t.Fatal("newSessionClient must return both client and done channel")
	}
	if cap(done) != 1 {
		t.Fatalf("done channel must stay buffered cap 1, got %d", cap(done))
	}
	a.mu.RLock()
	gotClient, gotDone := a.client, a.sessionDone
	a.mu.RUnlock()
	if gotClient != client {
		t.Fatal("a.client must be published under a.mu")
	}
	if gotDone != done {
		t.Fatal("a.sessionDone must be published under a.mu")
	}
	if client.Store.ID != nil {
		t.Fatal("fresh device must have no session ID (QR login path)")
	}
}

// TestWhatsAppConnectAndServeStoreFailureFastReturn pins the orchestrator's
// first gate: a store-open failure must return the error before any client
// is published or network is touched.
func TestWhatsAppConnectAndServeStoreFailureFastReturn(t *testing.T) {
	a := &whatsappAdapter{name: "t", seen: map[string]time.Time{}}
	blockingPath := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blockingPath, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	a.storeDir = blockingPath

	errCh := make(chan error, 1)
	go func() { errCh <- a.connectAndServe(context.Background()) }()
	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("connectAndServe must fail for an unusable store dir")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("connectAndServe did not return on store-open failure")
	}

	a.mu.RLock()
	gotClient, gotDone := a.client, a.sessionDone
	a.mu.RUnlock()
	if gotClient != nil || gotDone != nil {
		t.Fatal("no client/sessionDone may be published when the store fails to open")
	}
}

// TestWhatsAppAwaitSessionEndTerminal pins the #974 ordering: the terminal
// LoggedOut error closes the sqlite container BEFORE removeStoreDB deletes
// the files, and reports containerClosed so the orchestrator's close-once
// defer never double-closes.
func TestWhatsAppAwaitSessionEndTerminal(t *testing.T) {
	a := &whatsappAdapter{name: "t", storeDir: t.TempDir()}
	container := openTestWAStore(t, a.storeDir)
	done := make(chan error, 1)
	done <- errWhatsAppLoggedOut

	containerClosed := false
	got := a.awaitSessionEnd(context.Background(), container, &containerClosed, done)
	if !errors.Is(got, errWhatsAppLoggedOut) {
		t.Fatalf("terminal error must propagate, got %v", got)
	}
	if !containerClosed {
		t.Fatal("terminal path must mark containerClosed for the orchestrator defer")
	}
	dbPath := filepath.Join(a.storeDir, "whatsmeow.db")
	if _, err := os.Stat(dbPath); !os.IsNotExist(err) {
		t.Fatalf("store DB must be removed on terminal logout, stat err=%v", err)
	}
}

// TestWhatsAppAwaitSessionEndNonTerminal pins that a retryable session error
// propagates as-is, leaves the container open (the orchestrator defer closes
// it) and keeps the store DB on disk.
func TestWhatsAppAwaitSessionEndNonTerminal(t *testing.T) {
	a := &whatsappAdapter{name: "t", storeDir: t.TempDir()}
	container := openTestWAStore(t, a.storeDir)
	defer container.Close()
	done := make(chan error, 1)
	done <- errors.New("whatsapp disconnected")

	containerClosed := false
	got := a.awaitSessionEnd(context.Background(), container, &containerClosed, done)
	if got == nil || got.Error() != "whatsapp disconnected" {
		t.Fatalf("non-terminal error must propagate as-is, got %v", got)
	}
	if containerClosed {
		t.Fatal("non-terminal path must not mark containerClosed")
	}
	dbPath := filepath.Join(a.storeDir, "whatsmeow.db")
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("store DB must survive a non-terminal session error, stat err=%v", err)
	}
}

// TestWhatsAppAwaitSessionEndCtxCancel pins the ctx.Done branch: nil return,
// no store mutation, no container close.
func TestWhatsAppAwaitSessionEndCtxCancel(t *testing.T) {
	a := &whatsappAdapter{name: "t", storeDir: t.TempDir()}
	container := openTestWAStore(t, a.storeDir)
	defer container.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	containerClosed := false
	done := make(chan error, 1)
	if got := a.awaitSessionEnd(ctx, container, &containerClosed, done); got != nil {
		t.Fatalf("ctx cancellation must return nil, got %v", got)
	}
	if containerClosed {
		t.Fatal("ctx cancellation must not close the container")
	}
}
