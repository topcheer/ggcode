package acp

import (
	"encoding/json"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/tool"
)

// Regression guard for #752: handleSessionClose must delete the sessionID
// entry from workspaceDirs alongside its three sibling maps.
func TestSessionCloseDeletesWorkspaceDir(t *testing.T) {
	h := NewHandler(&config.Config{}, tool.NewRegistry(), nil, nil)
	h.initialized = true

	params, _ := json.Marshal(map[string]interface{}{
		"cwd":        t.TempDir(),
		"mcpServers": []interface{}{},
	})
	// session/new populates sessions + workspaceDirs.
	sidRaw, err := h.handleSessionNew(params)
	if err != nil {
		t.Fatalf("session/new: %v", err)
	}
	sidRes, ok := sidRaw.(SessionNewResult)
	if !ok {
		t.Fatalf("session/new returned %T, want SessionNewResult", sidRaw)
	}
	sid := sidRes.SessionID

	h.sessionsMu.RLock()
	_, inSessions := h.sessions[sid]
	dir, inDirs := h.workspaceDirs[sid]
	h.sessionsMu.RUnlock()
	if !inSessions || !inDirs || dir == "" {
		t.Fatalf("pre-close state: sessions=%v dirs=%v dir=%q", inSessions, inDirs, dir)
	}

	closeParams, _ := json.Marshal(map[string]interface{}{"sessionId": sid})
	if _, err := h.handleSessionClose(closeParams); err != nil {
		t.Fatalf("session/close: %v", err)
	}

	h.sessionsMu.RLock()
	_, inSessions = h.sessions[sid]
	_, inDirs = h.workspaceDirs[sid]
	h.sessionsMu.RUnlock()
	if inSessions {
		t.Error("session entry must be deleted on close")
	}
	if inDirs {
		t.Error("#752: workspaceDirs entry must be deleted on close")
	}
}
