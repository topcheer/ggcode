package acp

// #3483 probe: session/resume with a DIFFERENT CWD used to keep the old
// workspace-hash SaveDir (the only registration path that skipped
// SetSaveDir after a CWD change) - subsequent saves and compaction
// checkpoints landed in the OLD workspace's store.

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/tool"
)

func TestIssue3483_ResumeCWDMustReanchorSaveDir(t *testing.T) {
	oldCWD := t.TempDir()
	newCWD := t.TempDir()

	baseDir := t.TempDir()
	session := NewSession(oldCWD, nil)
	session.AddMessage("user", []ContentBlock{{Type: "text", Text: "hello"}})
	oldDir := workspaceSessionsDir(baseDir, oldCWD)
	os.MkdirAll(oldDir, 0o755)
	session.Save(oldDir)

	var buf bytes.Buffer
	transport := NewTransport(strings.NewReader(""), &buf)
	h := NewHandler(&config.Config{}, tool.NewRegistry(), transport, nil)
	h.sessionsDir = baseDir

	params := ResumeSessionRequest{SessionID: session.ID, CWD: newCWD}
	paramsJSON, _ := json.Marshal(params)
	if _, err := h.handleSessionResume(paramsJSON); err != nil {
		t.Fatalf("resume with CWD override failed: %v", err)
	}

	h.sessionsMu.Lock()
	resumed := h.sessions[session.ID]
	registered := h.workspaceDirs[session.ID]
	h.sessionsMu.Unlock()

	wantDir := workspaceSessionsDir(baseDir, newCWD)
	if resumed == nil {
		t.Fatal("resumed session not registered")
	}
	if got := resumed.SaveDir(); got != wantDir {
		t.Fatalf("SaveDir must re-anchor to the NEW workspace hash: got %q want %q", got, wantDir)
	}
	if got := resumed.CWD; got != newCWD {
		t.Fatalf("CWD override not applied: %q", got)
	}
	if registered != wantDir {
		t.Fatalf("workspaceDirs registry must track the new SaveDir: got %q want %q", registered, wantDir)
	}
	if _, err := os.Stat(wantDir); err != nil {
		t.Fatalf("new workspace session dir must be created: %v", err)
	}
}

func TestIssue3483_ResumeWithoutCWDKeepsOriginalSaveDir(t *testing.T) {
	origCWD := t.TempDir()
	baseDir := t.TempDir()
	session := NewSession(origCWD, nil)
	session.AddMessage("user", []ContentBlock{{Type: "text", Text: "hi"}})
	origDir := workspaceSessionsDir(baseDir, origCWD)
	os.MkdirAll(origDir, 0o755)
	session.Save(origDir)

	var buf bytes.Buffer
	transport := NewTransport(strings.NewReader(""), &buf)
	h := NewHandler(&config.Config{}, tool.NewRegistry(), transport, nil)
	h.sessionsDir = baseDir

	params := ResumeSessionRequest{SessionID: session.ID}
	paramsJSON, _ := json.Marshal(params)
	if _, err := h.handleSessionResume(paramsJSON); err != nil {
		t.Fatalf("resume without CWD failed: %v", err)
	}
	h.sessionsMu.Lock()
	resumed := h.sessions[session.ID]
	h.sessionsMu.Unlock()
	if got := resumed.SaveDir(); got != origDir {
		t.Fatalf("no-override resume must keep the original SaveDir: got %q want %q", got, origDir)
	}
}
