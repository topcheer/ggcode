package runfile

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteAndRead(t *testing.T) {
	sessionID := "test-session-001"
	pf := PortFile{
		Addr:      "127.0.0.1:12345",
		Token:     "test-token-abc",
		PID:       os.Getpid(),
		SessionID: sessionID,
		Workspace: "/tmp/workspace",
		Mode:      "supervised",
	}

	if err := Write(pf); err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	t.Cleanup(func() { Remove(sessionID) })

	got, err := Read(sessionID)
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if got.Addr != pf.Addr {
		t.Errorf("Addr = %q, want %q", got.Addr, pf.Addr)
	}
	if got.Token != pf.Token {
		t.Errorf("Token = %q, want %q", got.Token, pf.Token)
	}
	if got.PID != pf.PID {
		t.Errorf("PID = %d, want %d", got.PID, pf.PID)
	}
	if got.SessionID != pf.SessionID {
		t.Errorf("SessionID = %q, want %q", got.SessionID, pf.SessionID)
	}
	if got.Workspace != pf.Workspace {
		t.Errorf("Workspace = %q, want %q", got.Workspace, pf.Workspace)
	}
	if got.Mode != pf.Mode {
		t.Errorf("Mode = %q, want %q", got.Mode, pf.Mode)
	}
}

func TestWriteRequiresSessionID(t *testing.T) {
	pf := PortFile{
		Addr: "127.0.0.1:0",
		PID:  os.Getpid(),
	}
	err := Write(pf)
	if err == nil {
		t.Fatal("expected error for empty session ID")
	}
}

func TestMultipleSessionsSameWorkspace(t *testing.T) {
	ws := "/tmp/shared-workspace"

	pf1 := PortFile{Addr: "127.0.0.1:1", Token: "t1", PID: os.Getpid(), SessionID: "sess-a", Workspace: ws, Mode: "auto"}
	pf2 := PortFile{Addr: "127.0.0.1:2", Token: "t2", PID: os.Getpid(), SessionID: "sess-b", Workspace: ws, Mode: "bypass"}

	if err := Write(pf1); err != nil {
		t.Fatalf("Write pf1: %v", err)
	}
	if err := Write(pf2); err != nil {
		t.Fatalf("Write pf2: %v", err)
	}
	t.Cleanup(func() { Remove("sess-a"); Remove("sess-b") })

	// Both files should exist independently
	got1, err := Read("sess-a")
	if err != nil {
		t.Fatalf("Read sess-a: %v", err)
	}
	if got1.Addr != "127.0.0.1:1" {
		t.Errorf("sess-a Addr = %q, want 127.0.0.1:1", got1.Addr)
	}

	got2, err := Read("sess-b")
	if err != nil {
		t.Fatalf("Read sess-b: %v", err)
	}
	if got2.Addr != "127.0.0.1:2" {
		t.Errorf("sess-b Addr = %q, want 127.0.0.1:2", got2.Addr)
	}
}

func TestReadForWorkspace(t *testing.T) {
	ws := "/tmp/test-rfw-ws"

	pf1 := PortFile{Addr: "127.0.0.1:1", Token: "t1", PID: os.Getpid(), SessionID: "rfw-a", Workspace: ws, Mode: "auto"}
	pf2 := PortFile{Addr: "127.0.0.1:2", Token: "t2", PID: os.Getpid(), SessionID: "rfw-b", Workspace: ws, Mode: "auto"}
	pf3 := PortFile{Addr: "127.0.0.1:3", Token: "t3", PID: os.Getpid(), SessionID: "rfw-c", Workspace: "/other", Mode: "auto"}

	Write(pf1)
	Write(pf2)
	Write(pf3)
	t.Cleanup(func() { Remove("rfw-a"); Remove("rfw-b"); Remove("rfw-c") })

	results, err := ReadForWorkspace(ws)
	if err != nil {
		t.Fatalf("ReadForWorkspace: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 instances for workspace, got %d", len(results))
	}
}

func TestReadStalePID(t *testing.T) {
	sessionID := "test-session-stale"
	pf := PortFile{
		Addr:      "127.0.0.1:12345",
		Token:     "test-token",
		PID:       999999,
		SessionID: sessionID,
		Workspace: "/tmp/ws",
		Mode:      "auto",
	}

	if err := Write(pf); err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	t.Cleanup(func() { Remove(sessionID) })

	_, err := Read(sessionID)
	if err == nil {
		t.Fatal("expected error for stale PID, got nil")
	}
}

func TestReadMissing(t *testing.T) {
	_, err := Read("nonexistent-session")
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

func TestReadAll(t *testing.T) {
	Write(PortFile{Addr: "127.0.0.1:1", Token: "tall1", PID: os.Getpid(), SessionID: "all-a", Workspace: "/tmp/w1", Mode: "auto"})
	Write(PortFile{Addr: "127.0.0.1:2", Token: "tall2", PID: os.Getpid(), SessionID: "all-b", Workspace: "/tmp/w2", Mode: "bypass"})
	t.Cleanup(func() { Remove("all-a"); Remove("all-b") })

	all, err := ReadAll()
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	found := 0
	for _, p := range all {
		if p.Token == "tall1" || p.Token == "tall2" {
			found++
		}
	}
	if found < 2 {
		t.Errorf("expected at least 2 matching port files, found %d in %d total", found, len(all))
	}
}

func TestRemove(t *testing.T) {
	sessionID := "test-remove-sess"
	Write(PortFile{Addr: "127.0.0.1:0", Token: "x", PID: os.Getpid(), SessionID: sessionID, Workspace: "/tmp/ws", Mode: "supervised"})
	Remove(sessionID)
	_, err := Read(sessionID)
	if err == nil {
		t.Error("expected error after Remove")
	}
}

func TestProcessExists(t *testing.T) {
	if !processExists(os.Getpid()) {
		t.Error("expected current process to exist")
	}
	if processExists(0) {
		t.Error("PID 0 should not exist")
	}
	if processExists(-1) {
		t.Error("PID -1 should not exist")
	}
}

func TestReadAllAutoCleansStaleAndLegacy(t *testing.T) {
	// Write a stale-PID file
	stale := PortFile{Addr: "127.0.0.1:1", Token: "stale", PID: 999999, SessionID: "auto-stale", Workspace: "/tmp/ws", Mode: "auto"}
	Write(stale)

	// Write a legacy file (no session_id) manually
	home := homeDir()
	legacyPath := filepath.Join(home, ".ggcode", "run", "legacy-no-session.json")
	os.WriteFile(legacyPath, []byte(`{"addr":"127.0.0.1:2","token":"legacy","pid":`+fmt.Sprintf("%d", os.Getpid())+`,"workspace":"/tmp","mode":"auto"}`), 0o600)

	// Write a valid file
	valid := PortFile{Addr: "127.0.0.1:3", Token: "valid", PID: os.Getpid(), SessionID: "auto-valid", Workspace: "/tmp/ws", Mode: "auto"}
	Write(valid)
	t.Cleanup(func() {
		Remove("auto-stale")
		Remove("auto-valid")
		os.Remove(legacyPath)
	})

	all, err := ReadAll()
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	// The valid one should be present
	foundValid := false
	for _, pf := range all {
		if pf.SessionID == "auto-valid" {
			foundValid = true
		}
	}
	if !foundValid {
		t.Error("expected valid port file in ReadAll results")
	}
	// The stale file should have been auto-removed
	if _, err := os.Stat(path("auto-stale")); err == nil {
		t.Error("stale port file should have been auto-removed")
	}
	// The legacy file should have been auto-removed
	if _, err := os.Stat(legacyPath); err == nil {
		t.Error("legacy port file should have been auto-removed")
	}
}
