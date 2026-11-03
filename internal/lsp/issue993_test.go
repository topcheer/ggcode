package lsp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	fake993InitResult = `{"jsonrpc":"2.0","id":1,"result":{"capabilities":{},"serverInfo":{"name":"fake993"}}}`
	fake993PingResult = `{"jsonrpc":"2.0","id":2,"result":{}}`
	fake993ReadyLog   = `{"jsonrpc":"2.0","method":"window/logMessage","params":{"type":3,"message":"Finished loading solution"}}`
)

func fake993Frame(body string) string {
	return fmt.Sprintf("printf 'Content-Length: %d\\r\\n\\r\\n%s'\n", len(body), body)
}

func fake993KillScript(t *testing.T, dir string) string {
	t.Helper()
	script := filepath.Join(dir, "fake993-kill.sh")
	body := "#!/bin/sh\n" +
		fake993Frame(fake993InitResult) +
		"read line\nread line\nread line\n" +
		"read line\n" +
		"exit 1\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatalf("write kill script: %v", err)
	}
	return script
}

func fake993EarlyNotifyScript(t *testing.T, dir, name string) string {
	t.Helper()
	script := filepath.Join(dir, name)
	body := "#!/bin/sh\n" +
		fake993Frame(fake993ReadyLog) +
		fake993Frame(fake993InitResult) +
		"read line\nread line\nread line\n" +
		"while read line; do\n" +
		fake993Frame(fake993PingResult) +
		"done\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatalf("write early-notify script: %v", err)
	}
	return script
}

func fake993Resolved(binary string, languageID string) ResolvedServer {
	return ResolvedServer{
		LanguageID:  languageID,
		DisplayName: "fake993",
		Binary:      binary,
		Args:        []string{},
	}
}

func withIsolatedManager(t *testing.T, fn func()) {
	t.Helper()
	prev := globalSessions
	fresh := &sessionManager{sessions: make(map[string]*sessionClient), stopCh: make(chan struct{})}
	globalSessions = fresh
	defer func() {
		close(fresh.stopCh)
		globalSessions = prev
	}()
	fn()
}

func fake993Workspace(t *testing.T, dir string) string {
	t.Helper()
	ws := filepath.Join(dir, "ws")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	return ws
}

func TestIssue993IsFailedSemantics(t *testing.T) {
	var nilClient *stdioClient
	if !nilClient.isFailed() {
		t.Fatal("nil client must report isFailed=true")
	}
	fresh := &stdioClient{}
	if fresh.isFailed() {
		t.Fatal("fresh client must not be failed")
	}
	fresh.failMu.Lock()
	fresh.failed = true
	fresh.failMu.Unlock()
	if !fresh.isFailed() {
		t.Fatal("client with failed=true must report isFailed")
	}
}

func TestIssue993AcquireRebuildsAfterServerCrash(t *testing.T) {
	dir := t.TempDir()
	script := fake993KillScript(t, dir)
	resolved := fake993Resolved(script, "bash")
	workspace := fake993Workspace(t, dir)

	withIsolatedManager(t, func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()

		first, err := globalSessions.acquire(ctx, workspace, resolved)
		if err != nil {
			t.Fatalf("first acquire: %v", err)
		}
		firstPID := first.client.cmd.Process.Pid

		_ = first.client.notify(ctx, "x/crash", map[string]any{})

		deadline := time.Now().Add(10 * time.Second)
		for !first.client.isFailed() {
			if time.Now().After(deadline) {
				t.Fatal("client never reported failed after server exit")
			}
			time.Sleep(20 * time.Millisecond)
		}

		second, err := globalSessions.acquire(ctx, workspace, resolved)
		if err != nil {
			t.Fatalf("second acquire after crash: %v", err)
		}
		if second == first {
			t.Fatal("acquire returned the dead cached session instead of rebuilding")
		}
		if second.client == nil || second.client.isFailed() {
			t.Fatal("rebuilt session must have a live client")
		}
		if second.client.cmd.Process.Pid == firstPID {
			t.Fatal("rebuilt session reuses the dead process")
		}
		if got := len(globalSessions.sessions); got != 1 {
			t.Fatalf("sessions cache size = %d, want 1", got)
		}
		second.close()
	})
}

func TestIssue993HandlerInstalledBeforeHandshake(t *testing.T) {
	dir := t.TempDir()
	script := fake993EarlyNotifyScript(t, dir, "fake993-early.sh")
	resolved := fake993Resolved(script, "bash")
	workspace := fake993Workspace(t, dir)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	seen := make(chan string, 1)
	handler := func(method string, params json.RawMessage) {
		select {
		case seen <- method + ":" + string(params):
		default:
		}
	}
	client, err := startClient(ctx, workspace, resolved, handler)
	if err != nil {
		t.Fatalf("startClient: %v", err)
	}
	defer client.close()

	var raw json.RawMessage
	if err := client.call(ctx, "x/ping", map[string]any{}, &raw); err != nil {
		t.Fatalf("ping: %v", err)
	}
	select {
	case got := <-seen:
		if !strings.Contains(got, "window/logMessage") ||
			!strings.Contains(got, "Finished loading solution") {
			t.Fatalf("unexpected notification captured: %s", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("handshake-window notification was dropped (handler not installed before handshake)")
	}
}

func TestIssue993SessionReadySignalSurvivesFastServer(t *testing.T) {
	dir := t.TempDir()

	script := fake993EarlyNotifyScript(t, dir, "csharp-ls")
	resolved := fake993Resolved(script, "csharp")
	if base := binaryBaseName(resolved.Binary); base != "csharp-ls" {
		t.Fatalf("test setup: binary base name = %q, want csharp-ls", base)
	}
	workspace := fake993Workspace(t, dir)

	withIsolatedManager(t, func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()

		session, err := globalSessions.acquire(ctx, workspace, resolved)
		if err != nil {
			t.Fatalf("acquire: %v", err)
		}
		defer session.close()

		if !session.shouldRetryEmptyResults() {
			t.Fatal("test setup: expected csharp-ls retry semantics")
		}
		readyCtx, readyCancel := context.WithTimeout(ctx, 5*time.Second)
		defer readyCancel()
		if err := session.awaitProjectReady(readyCtx); err != nil {
			t.Fatalf("awaitProjectReady did not resolve from handshake-window notification: %v", err)
		}
	})
}
