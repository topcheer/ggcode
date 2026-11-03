package acp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

func sessionNewParams(cwd string) []byte {
	b, _ := json.Marshal(struct {
		CWD string `json:"CWD"`
	}{CWD: cwd})
	return b
}

func TestIssue1095_SessionLoadRefusesActiveSession(t *testing.T) {
	tmpDir := t.TempDir()
	h := NewHandler(&config.Config{}, tool.NewRegistry(), NewTransport(strings.NewReader(""), io.Discard), &failingProvider{})
	h.initialized = true

	createResp, err := h.handleSessionNew(sessionNewParams(tmpDir))
	if err != nil {
		t.Fatalf("session/new: %v", err)
	}
	sessionID := createResp.(SessionNewResult).SessionID

	h.sessionsMu.RLock()
	_, active := h.sessions[sessionID]
	h.sessionsMu.RUnlock()
	if !active {
		t.Fatal("session should be active after session/new")
	}

	loadParams := fmt.Sprintf(`{"SessionID":"%s"}`, sessionID)
	_, err = h.handleSessionLoad([]byte(loadParams))

	if err == nil {
		t.Fatal("session/load should reject loading an already-active session")
	}
	expectedErr := fmt.Sprintf("session %s already active: cannot load", sessionID)
	if err.Error() != expectedErr {
		t.Errorf("expected error %q, got %q", expectedErr, err.Error())
	}

	h.sessionsMu.RLock()
	_, stillActive := h.sessions[sessionID]
	oldWorkspaceDir, hasWorkspaceDir := h.workspaceDirs[sessionID]
	h.sessionsMu.RUnlock()

	if !stillActive {
		t.Fatal("original session should remain active after failed load")
	}
	if !hasWorkspaceDir {
		t.Fatal("original session's workspace dir should not be lost")
	}
	if oldWorkspaceDir == "" {
		t.Fatal("workspace dir should not be empty for active session")
	}
}

func TestIssue1095_SessionLoadResumesClosedSession(t *testing.T) {
	tmpDir := t.TempDir()
	h := NewHandler(&config.Config{}, tool.NewRegistry(), NewTransport(strings.NewReader(""), io.Discard), &failingProvider{})
	h.initialized = true

	createResp, err := h.handleSessionNew(sessionNewParams(tmpDir))
	if err != nil {
		t.Fatalf("session/new: %v", err)
	}
	sessionID := createResp.(SessionNewResult).SessionID

	h.sessionsMu.RLock()
	session := h.sessions[sessionID]
	saveDir := h.workspaceDirs[sessionID]
	h.sessionsMu.RUnlock()
	session.AddMessage("user", []ContentBlock{{Type: "text", Text: "hi"}})
	if err := session.Save(saveDir); err != nil {
		t.Fatalf("save session: %v", err)
	}

	_, err = h.handleSessionClose([]byte(fmt.Sprintf(`{"SessionID":"%s"}`, sessionID)))
	if err != nil {
		t.Fatalf("session/close: %v", err)
	}

	h.sessionsMu.RLock()
	_, active := h.sessions[sessionID]
	h.sessionsMu.RUnlock()
	if active {
		t.Fatal("session should not be active after close")
	}

	loadParams := fmt.Sprintf(`{"SessionID":"%s"}`, sessionID)
	_, err = h.handleSessionLoad([]byte(loadParams))
	if err != nil {
		t.Fatalf("session/load of closed session should succeed: %v", err)
	}

	h.sessionsMu.RLock()
	_, activeAfterLoad := h.sessions[sessionID]
	_, hasWorkspaceDirAfterLoad := h.workspaceDirs[sessionID]
	h.sessionsMu.RUnlock()

	if !activeAfterLoad {
		t.Fatal("session should be active after successful load")
	}
	if !hasWorkspaceDirAfterLoad {
		t.Fatal("loaded session should have a workspace dir")
	}
}

func TestIssue1096_PromptDoesNotSaveAfterClose(t *testing.T) {
	tmpDir := t.TempDir()
	h := NewHandler(&config.Config{}, tool.NewRegistry(), NewTransport(strings.NewReader(""), io.Discard), &failingProvider{})
	h.initialized = true

	createResp, err := h.handleSessionNew(sessionNewParams(tmpDir))
	if err != nil {
		t.Fatalf("session/new: %v", err)
	}
	sessionID := createResp.(SessionNewResult).SessionID

	promptParams := fmt.Sprintf(`{"SessionID":"%s","Prompt":[{"type":"text","text":"test"}]}`, sessionID)
	_, err = h.handleSessionPrompt([]byte(promptParams))
	if err != nil {
		t.Fatalf("session/prompt: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	_, err = h.handleSessionClose([]byte(fmt.Sprintf(`{"SessionID":"%s"}`, sessionID)))
	if err != nil {
		t.Fatalf("session/close: %v", err)
	}

	time.Sleep(500 * time.Millisecond)

	sessionFile := fmt.Sprintf("%s/%s.json", tmpDir, sessionID)
	if _, err := os.Stat(sessionFile); err == nil {
		t.Errorf("session file should not exist after close during prompt: %s", sessionFile)
	}

	h.sessionsMu.RLock()
	_, active := h.sessions[sessionID]
	h.sessionsMu.RUnlock()
	if active {
		t.Fatal("session should not be in active list after close")
	}
}

func TestIssue1096_PromptSavesWhenSessionStillActive(t *testing.T) {
	tmpDir := t.TempDir()
	h := NewHandler(&config.Config{}, tool.NewRegistry(), NewTransport(strings.NewReader(""), io.Discard), &failingProvider{})
	h.initialized = true
	h.sessionsDir = tmpDir

	createResp, err := h.handleSessionNew(sessionNewParams(tmpDir))
	if err != nil {
		t.Fatalf("session/new: %v", err)
	}
	sessionID := createResp.(SessionNewResult).SessionID

	promptParams := fmt.Sprintf(`{"SessionID":"%s","Prompt":[{"type":"text","text":"test"}]}`, sessionID)
	_, err = h.handleSessionPrompt([]byte(promptParams))
	if err != nil {
		t.Fatalf("session/prompt: %v", err)
	}

	sessionFile := filepath.Join(workspaceSessionsDir(tmpDir, tmpDir), sessionID+".json")
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(sessionFile); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Errorf("session file should exist after normal prompt: %s", sessionFile)
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestIssue1096_PromptDoesNotCreateOrphanInRootDir(t *testing.T) {
	tmpDir := t.TempDir()
	h := NewHandler(&config.Config{}, tool.NewRegistry(), NewTransport(strings.NewReader(""), io.Discard), &failingProvider{})
	h.initialized = true
	h.sessionsDir = tmpDir

	createResp, err := h.handleSessionNew(sessionNewParams(tmpDir))
	if err != nil {
		t.Fatalf("session/new: %v", err)
	}
	sessionID := createResp.(SessionNewResult).SessionID

	h.sessionsMu.Lock()
	delete(h.workspaceDirs, sessionID)
	h.sessionsMu.Unlock()

	promptParams := fmt.Sprintf(`{"SessionID":"%s","Prompt":[{"type":"text","text":"test"}]}`, sessionID)
	_, err = h.handleSessionPrompt([]byte(promptParams))
	if err != nil {
		t.Fatalf("session/prompt: %v", err)
	}

	time.Sleep(500 * time.Millisecond)

	sessionFile := fmt.Sprintf("%s/%s.json", tmpDir, sessionID)
	if _, err := os.Stat(sessionFile); err == nil {
		t.Errorf("session file should not be created in root dir when workspaceDirs is missing: %s", sessionFile)
	}

}

type failingProvider struct{}

func (*failingProvider) Name() string { return "failing" }
func (*failingProvider) Chat(context.Context, []provider.Message, []provider.ToolDefinition) (*provider.ChatResponse, error) {
	return nil, errors.New("no provider in test")
}
func (*failingProvider) ChatStream(context.Context, []provider.Message, []provider.ToolDefinition) (<-chan provider.StreamEvent, error) {
	return nil, errors.New("no provider in test")
}
func (*failingProvider) CountTokens(context.Context, []provider.Message) (int, error) {
	return 0, errors.New("no provider in test")
}
