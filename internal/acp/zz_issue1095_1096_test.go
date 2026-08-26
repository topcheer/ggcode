package acp

// Tests for #1095 and #1096.
// #1095: session/load should not overwrite active sessions (prevents MCP manager leak and TryBeginRun bypass).
// #1096: prompt goroutine should not save sessions after they are closed (prevents resurrection and orphan files).

import (
	"context"
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

// ---------- #1095: session/load active session guard ----------

func TestIssue1095_SessionLoadRefusesActiveSession(t *testing.T) {
	// Set up a handler with a test transport
	tmpDir := t.TempDir()
	h := NewHandler(&config.Config{}, tool.NewRegistry(), NewTransport(strings.NewReader(""), io.Discard), &failingProvider{})
	h.initialized = true

	// Create and activate a new session
	createResp, err := h.handleSessionNew([]byte(fmt.Sprintf(`{"CWD":"%s"}`, tmpDir)))
	if err != nil {
		t.Fatalf("session/new: %v", err)
	}
	sessionID := createResp.(SessionNewResult).SessionID

	// Verify session is active
	h.sessionsMu.RLock()
	_, active := h.sessions[sessionID]
	h.sessionsMu.RUnlock()
	if !active {
		t.Fatal("session should be active after session/new")
	}

	// Attempt to load the same session while it's still active
	loadParams := fmt.Sprintf(`{"SessionID":"%s"}`, sessionID)
	_, err = h.handleSessionLoad([]byte(loadParams))

	// Should fail with "already active" error
	if err == nil {
		t.Fatal("session/load should reject loading an already-active session")
	}
	expectedErr := fmt.Sprintf("session %s already active: cannot load", sessionID)
	if err.Error() != expectedErr {
		t.Errorf("expected error %q, got %q", expectedErr, err.Error())
	}

	// Verify the original session is still intact (not replaced)
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
	// Verify that loading a closed session still works (baseline sanity check)
	tmpDir := t.TempDir()
	h := NewHandler(&config.Config{}, tool.NewRegistry(), NewTransport(strings.NewReader(""), io.Discard), &failingProvider{})
	h.initialized = true

	// Create and close a session
	createResp, err := h.handleSessionNew([]byte(fmt.Sprintf(`{"CWD":"%s"}`, tmpDir)))
	if err != nil {
		t.Fatalf("session/new: %v", err)
	}
	sessionID := createResp.(SessionNewResult).SessionID

	// Persist the session before closing so it can be resumed from disk.
	// Save() deletes/no-ops for sessions without conversation history, so
	// seed one message first (a real closed session has history).
	h.sessionsMu.RLock()
	session := h.sessions[sessionID]
	saveDir := h.workspaceDirs[sessionID]
	h.sessionsMu.RUnlock()
	session.AddMessage("user", []ContentBlock{{Type: "text", Text: "hi"}})
	if err := session.Save(saveDir); err != nil {
		t.Fatalf("save session: %v", err)
	}

	// Close the session
	_, err = h.handleSessionClose([]byte(fmt.Sprintf(`{"SessionID":"%s"}`, sessionID)))
	if err != nil {
		t.Fatalf("session/close: %v", err)
	}

	// Verify session is no longer active
	h.sessionsMu.RLock()
	_, active := h.sessions[sessionID]
	h.sessionsMu.RUnlock()
	if active {
		t.Fatal("session should not be active after close")
	}

	// Loading the closed session should succeed
	loadParams := fmt.Sprintf(`{"SessionID":"%s"}`, sessionID)
	_, err = h.handleSessionLoad([]byte(loadParams))
	if err != nil {
		t.Fatalf("session/load of closed session should succeed: %v", err)
	}

	// Verify session is now active again
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

// ---------- #1096: prompt save after close ----------

func TestIssue1096_PromptDoesNotSaveAfterClose(t *testing.T) {
	// This test verifies that when a session is closed while a prompt is running,
	// the prompt goroutine does not save the session (preventing resurrection).
	tmpDir := t.TempDir()
	h := NewHandler(&config.Config{}, tool.NewRegistry(), NewTransport(strings.NewReader(""), io.Discard), &failingProvider{})
	h.initialized = true

	// Create a session
	createResp, err := h.handleSessionNew([]byte(fmt.Sprintf(`{"CWD":"%s"}`, tmpDir)))
	if err != nil {
		t.Fatalf("session/new: %v", err)
	}
	sessionID := createResp.(SessionNewResult).SessionID

	// Start a prompt (this spawns a goroutine)
	promptParams := fmt.Sprintf(`{"SessionID":"%s","Prompt":[{"type":"text","text":"test"}]}`, sessionID)
	_, err = h.handleSessionPrompt([]byte(promptParams))
	if err != nil {
		t.Fatalf("session/prompt: %v", err)
	}

	// Give the prompt goroutine a moment to start
	time.Sleep(100 * time.Millisecond)

	// Close the session while prompt is running
	_, err = h.handleSessionClose([]byte(fmt.Sprintf(`{"SessionID":"%s"}`, sessionID)))
	if err != nil {
		t.Fatalf("session/close: %v", err)
	}

	// Wait for prompt goroutine to complete
	time.Sleep(500 * time.Millisecond)

	// Verify the session file does not exist (or was deleted if empty)
	sessionFile := fmt.Sprintf("%s/%s.json", tmpDir, sessionID)
	if _, err := os.Stat(sessionFile); err == nil {
		t.Errorf("session file should not exist after close during prompt: %s", sessionFile)
	}

	// Verify session is not in active list
	h.sessionsMu.RLock()
	_, active := h.sessions[sessionID]
	h.sessionsMu.RUnlock()
	if active {
		t.Fatal("session should not be in active list after close")
	}
}

func TestIssue1096_PromptSavesWhenSessionStillActive(t *testing.T) {
	// Baseline: verify that normal prompt execution still saves the session.
	// Use a provider that fails immediately so ExecutePrompt returns fast
	// (with nil provider the agent run blocks and the save is never reached).
	tmpDir := t.TempDir()
	h := NewHandler(&config.Config{}, tool.NewRegistry(), NewTransport(strings.NewReader(""), io.Discard), &failingProvider{})
	h.initialized = true
	h.sessionsDir = tmpDir

	// Create a session
	createResp, err := h.handleSessionNew([]byte(fmt.Sprintf(`{"CWD":"%s"}`, tmpDir)))
	if err != nil {
		t.Fatalf("session/new: %v", err)
	}
	sessionID := createResp.(SessionNewResult).SessionID

	// Start a prompt
	promptParams := fmt.Sprintf(`{"SessionID":"%s","Prompt":[{"type":"text","text":"test"}]}`, sessionID)
	_, err = h.handleSessionPrompt([]byte(promptParams))
	if err != nil {
		t.Fatalf("session/prompt: %v", err)
	}

	// Wait for prompt goroutine to complete and save (poll: ExecutePrompt
	// with a nil provider may take a moment to fail before the defer saves)
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
	// Verify that when workspaceDirs is missing (edge case), we don't
	// fall back to root sessionsDir (preventing orphan files)
	tmpDir := t.TempDir()
	h := NewHandler(&config.Config{}, tool.NewRegistry(), NewTransport(strings.NewReader(""), io.Discard), &failingProvider{})
	h.initialized = true
	h.sessionsDir = tmpDir

	// Create a session
	createResp, err := h.handleSessionNew([]byte(fmt.Sprintf(`{"CWD":"%s"}`, tmpDir)))
	if err != nil {
		t.Fatalf("session/new: %v", err)
	}
	sessionID := createResp.(SessionNewResult).SessionID

	// Manually delete the workspaceDirs entry to simulate the edge case
	h.sessionsMu.Lock()
	delete(h.workspaceDirs, sessionID)
	h.sessionsMu.Unlock()

	// Start a prompt
	promptParams := fmt.Sprintf(`{"SessionID":"%s","Prompt":[{"type":"text","text":"test"}]}`, sessionID)
	_, err = h.handleSessionPrompt([]byte(promptParams))
	if err != nil {
		t.Fatalf("session/prompt: %v", err)
	}

	// Wait for prompt goroutine to complete
	time.Sleep(500 * time.Millisecond)

	// Verify no file was created in the root sessionsDir
	sessionFile := fmt.Sprintf("%s/%s.json", tmpDir, sessionID)
	if _, err := os.Stat(sessionFile); err == nil {
		t.Errorf("session file should not be created in root dir when workspaceDirs is missing: %s", sessionFile)
	}

	// The file should also not exist in any workspace subdir
	// (we didn't set one in this test)
}

// failingProvider implements provider.Provider; every request fails
// immediately so AgentLoop.ExecutePrompt returns fast instead of blocking
// on a nil provider, letting the prompt goroutine reach its save path.
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
