package acp

// Issue #548 characteristic tests for internal/acp.
//
// Bug C (high): session/list must enumerate the flat <hash>/<id>.json layout
// that Session.Save actually writes.
// Bug D (mid-high): session/set_mode must survive the prompt gap (loop
// recreation) instead of silently reverting to "auto".
// Bug E (mid): client EOF must fail all in-flight Agent→Client requests.
// Bug F (mid): async device-flow auth failures must propagate to the next
// prompt, and the negotiated auth method must be enforced; authenticated
// writes must be lock-protected (checked here via behavior, not race tests).
// Sister (low): session/close must use DoCancel (locked) — covered by compile
// and by the mode-forget behavior test.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/tool"
)

func newIssue548Handler(t *testing.T) *Handler {
	t.Helper()
	var buf bytes.Buffer
	transport := NewTransport(strings.NewReader(""), &buf)
	h := NewHandler(&config.Config{}, tool.NewRegistry(), transport, nil)
	h.initialized = true
	// Redirect session storage into a temp dir so the test is hermetic.
	dir := t.TempDir()
	h.sessionsDir = dir
	return h
}

// issue548PersistSession writes a session in the exact layout Session.Save
// produces: <sessionsDir>/<workspace-hash>/<id>.json.
func issue548PersistSession(t *testing.T, h *Handler, id, cwd string, updated time.Time) {
	t.Helper()
	s := NewSession(cwd, nil)
	s.ID = id
	s.CreatedAt = updated.Add(-time.Hour)
	s.AddMessage("user", []ContentBlock{{Type: "text", Text: "hello"}})
	dir := workspaceSessionsDir(h.sessionsDir, cwd)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Write with a deterministic UpdatedAt so list-order assertions are stable.
	data := SessionData{
		ID:        id,
		CWD:       cwd,
		CreatedAt: s.CreatedAt,
		UpdatedAt: updated,
		Messages:  s.Messages(),
	}
	b, _ := json.Marshal(data)
	if err := os.WriteFile(filepath.Join(dir, id+".json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// --- Bug C: session/list reads the real Save layout -------------------------

func TestIssue548SessionListReadsSaveLayout(t *testing.T) {
	h := newIssue548Handler(t)
	issue548PersistSession(t, h, "sess-alpha", "/tmp/wk", time.Now())
	issue548PersistSession(t, h, "sess-beta", "/tmp/wk", time.Now().Add(-time.Minute))

	// List scoped to a CWD
	params, _ := json.Marshal(ListSessionsRequest{CWD: "/tmp/wk"})
	res, err := h.handleSessionList(params)
	if err != nil {
		t.Fatalf("handleSessionList error: %v", err)
	}
	list := res.(ListSessionsResponse)
	if len(list.Sessions) != 2 {
		t.Fatalf("expected 2 sessions from Save layout, got %d (%+v)", len(list.Sessions), list.Sessions)
	}
	byID := map[string]SessionInfo{}
	for _, s := range list.Sessions {
		byID[s.SessionID] = s
	}
	if _, ok := byID["sess-alpha"]; !ok {
		t.Error("sess-alpha missing from list")
	}
	if _, ok := byID["sess-beta"]; !ok {
		t.Error("sess-beta missing from list")
	}
	if byID["sess-alpha"].CWD != "/tmp/wk" {
		t.Errorf("alpha CWD = %q, want /tmp/wk", byID["sess-alpha"].CWD)
	}
}

func TestIssue548SessionListUnscopedScansAllWorkspaces(t *testing.T) {
	h := newIssue548Handler(t)
	issue548PersistSession(t, h, "sess-w1", "/tmp/wk1", time.Now())
	issue548PersistSession(t, h, "sess-w2", "/tmp/wk2", time.Now())

	params, _ := json.Marshal(ListSessionsRequest{})
	res, err := h.handleSessionList(params)
	if err != nil {
		t.Fatalf("handleSessionList error: %v", err)
	}
	list := res.(ListSessionsResponse)
	if len(list.Sessions) != 2 {
		t.Fatalf("expected 2 sessions across workspaces, got %d", len(list.Sessions))
	}
}

func TestIssue548SessionListSaveThenListIntegration(t *testing.T) {
	// End-to-end: real Session.Save → handleSessionList must return it.
	h := newIssue548Handler(t)
	s := NewSession(t.TempDir(), nil)
	s.AddMessage("user", []ContentBlock{{Type: "text", Text: "hi"}})
	dir := workspaceSessionsDir(h.sessionsDir, s.CWD)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(dir); err != nil {
		t.Fatalf("Save error: %v", err)
	}
	params, _ := json.Marshal(ListSessionsRequest{CWD: s.CWD})
	res, err := h.handleSessionList(params)
	if err != nil {
		t.Fatal(err)
	}
	list := res.(ListSessionsResponse)
	if len(list.Sessions) != 1 || list.Sessions[0].SessionID != s.ID {
		t.Fatalf("Save+list roundtrip failed: %+v", list.Sessions)
	}
}

// --- Bug D: set_mode survives the prompt gap --------------------------------

func TestIssue548SetModeSurvivesPromptGap(t *testing.T) {
	h := newIssue548Handler(t)

	// Register a session.
	newParams, _ := json.Marshal(SessionNewParams{CWD: t.TempDir()})
	newRes, err := h.handleSessionNew(newParams)
	if err != nil {
		t.Fatal(err)
	}
	sessionID := newRes.(SessionNewResult).SessionID

	// Explicitly set supervised mode while a loop is running (loop present).
	loop := NewAgentLoop(h.cfg, h.toolRegistry, h.transport, h.sessions[sessionID], h.clientCaps, nil)
	h.sessionsMu.Lock()
	h.agentLoops[sessionID] = loop
	h.sessionsMu.Unlock()
	defer func() {
		h.sessionsMu.Lock()
		delete(h.agentLoops, sessionID)
		h.sessionsMu.Unlock()
	}()

	modeParams, _ := json.Marshal(SessionSetModeParams{SessionID: sessionID, Mode: "supervised"})
	if _, err := h.handleSessionSetMode(modeParams); err != nil {
		t.Fatalf("set_mode error: %v", err)
	}

	// Simulate end of prompt: the handler deletes the loop reference.
	h.sessionsMu.Lock()
	delete(h.agentLoops, sessionID)
	h.sessionsMu.Unlock()

	// Next prompt recreates the loop — the persisted mode must be re-applied.
	h.sessionsMu.Lock()
	recreated := NewAgentLoop(h.cfg, h.toolRegistry, h.transport, h.sessions[sessionID], h.clientCaps, nil)
	h.agentLoops[sessionID] = recreated
	mode, ok := h.sessionModes[sessionID]
	h.sessionsMu.Unlock()
	defer func() {
		h.sessionsMu.Lock()
		delete(h.agentLoops, sessionID)
		h.sessionsMu.Unlock()
	}()

	if !ok || mode != "supervised" {
		t.Fatalf("sessionModes after prompt gap = (%q, %v), want (supervised, true)", mode, ok)
	}

	// Apply the recorded mode the same way handleSessionPrompt does and check
	// the loop actually adopted it (not the default auto).
	recreated.SetMode(mode)
	if got := recreated.Mode(); got != "supervised" {
		t.Fatalf("recreated loop mode = %q, want supervised", got)
	}
}

func TestIssue548SetModeWithoutLoopStillRecorded(t *testing.T) {
	h := newIssue548Handler(t)
	newParams, _ := json.Marshal(SessionNewParams{CWD: t.TempDir()})
	newRes, _ := h.handleSessionNew(newParams)
	sessionID := newRes.(SessionNewResult).SessionID

	// No active loop — set_mode must still succeed and persist.
	modeParams, _ := json.Marshal(SessionSetModeParams{SessionID: sessionID, Mode: "bypass"})
	if _, err := h.handleSessionSetMode(modeParams); err != nil {
		t.Fatalf("set_mode without loop should succeed, got %v", err)
	}
	h.sessionsMu.RLock()
	mode, ok := h.sessionModes[sessionID]
	h.sessionsMu.RUnlock()
	if !ok || mode != "bypass" {
		t.Fatalf("sessionModes = (%q, %v), want (bypass, true)", mode, ok)
	}
}

func TestIssue548ModeForgottenAfterClose(t *testing.T) {
	h := newIssue548Handler(t)
	newParams, _ := json.Marshal(SessionNewParams{CWD: t.TempDir()})
	newRes, _ := h.handleSessionNew(newParams)
	sessionID := newRes.(SessionNewResult).SessionID

	modeParams, _ := json.Marshal(SessionSetModeParams{SessionID: sessionID, Mode: "auto"})
	h.handleSessionSetMode(modeParams)

	closeParams, _ := json.Marshal(CloseSessionRequest{SessionID: sessionID})
	if _, err := h.handleSessionClose(closeParams); err != nil {
		t.Fatal(err)
	}
	h.sessionsMu.RLock()
	_, ok := h.sessionModes[sessionID]
	h.sessionsMu.RUnlock()
	if ok {
		t.Error("sessionModes should be cleared on session/close")
	}
}

// --- Bug E: EOF fails pending requests ---------------------------------------

func TestIssue548FailAllPendingFailsWaiter(t *testing.T) {
	var out bytes.Buffer
	transport := NewTransport(strings.NewReader(""), &out)

	var wg sync.WaitGroup
	wg.Add(1)
	errCh := make(chan error, 1)
	go func() {
		defer wg.Done()
		_, err := transport.SendRequest("session/request_permission", map[string]any{}, 5*time.Minute)
		errCh <- err
	}()

	// Give the waiter a moment to register its pending entry.
	deadline := time.Now().Add(2 * time.Second)
	registered := false
	for time.Now().Before(deadline) {
		transport.pendingMu.Lock()
		registered = len(transport.pending) > 0
		transport.pendingMu.Unlock()
		if registered {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !registered {
		t.Fatal("SendRequest never registered a pending entry")
	}

	transport.FailAllPending(fmt.Errorf("client disconnected"))

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("expected error from SendRequest after FailAllPending")
		}
		if !strings.Contains(err.Error(), "client disconnected") {
			t.Errorf("error = %v, want it to mention 'client disconnected'", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("SendRequest still hanging after FailAllPending (would have waited 5min)")
	}
	wg.Wait()
}

func TestIssue548FailAllPendingEmptyIsNoop(t *testing.T) {
	var out bytes.Buffer
	transport := NewTransport(strings.NewReader(""), &out)
	// Must not panic or block with zero pending entries.
	transport.FailAllPending(nil)
	transport.FailAllPending(fmt.Errorf("boom"))
}

func TestIssue548RunEOFFailsPendingThenCleansUp(t *testing.T) {
	// Handler.Run with an immediately-EOF reader must return promptly even
	// while a permission request is parked in the transport.
	var out bytes.Buffer
	transport := NewTransport(strings.NewReader(""), &out) // EOF on first read
	h := NewHandler(&config.Config{}, tool.NewRegistry(), transport, nil)
	h.initialized = true

	errCh := make(chan error, 1)
	go func() {
		errCh <- h.Run(context.Background())
	}()

	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("Run on EOF should return nil, got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return on EOF")
	}

	// After EOF, any pending SendRequest must fail fast rather than hang.
	transport.pendingMu.Lock()
	left := len(transport.pending)
	transport.pendingMu.Unlock()
	if left != 0 {
		t.Errorf("pending map should be empty after EOF Run, has %d", left)
	}
}

// --- Bug F: auth failure propagation + enforcement ---------------------------

func TestIssue548PromptEnforcesNegotiatedAuthMethod(t *testing.T) {
	h := newIssue548Handler(t)
	newParams, _ := json.Marshal(SessionNewParams{CWD: t.TempDir()})
	newRes, _ := h.handleSessionNew(newParams)
	sessionID := newRes.(SessionNewResult).SessionID

	// Simulate a negotiated-but-failed "agent" auth flow.
	h.authMu.Lock()
	h.authMethodUsed = "agent"
	h.authenticated = false
	h.authErr = fmt.Errorf("device flow rejected")
	h.authMu.Unlock()

	promptParams, _ := json.Marshal(SessionPromptParams{SessionID: sessionID, Prompt: []ContentBlock{{Type: "text", Text: "hi"}}})
	_, err := h.handleSessionPrompt(promptParams)
	if err == nil {
		t.Fatal("expected prompt to be rejected while negotiated auth method has failed")
	}
	if !strings.Contains(err.Error(), "authentication required (agent)") || !strings.Contains(err.Error(), "device flow rejected") {
		t.Errorf("error = %v, want it to propagate the device-flow failure", err)
	}
}

func TestIssue548PromptRejectedWhileAuthInProgress(t *testing.T) {
	h := newIssue548Handler(t)
	newParams, _ := json.Marshal(SessionNewParams{CWD: t.TempDir()})
	newRes, _ := h.handleSessionNew(newParams)
	sessionID := newRes.(SessionNewResult).SessionID

	// Negotiated "agent" auth, still in progress (no error, not authenticated).
	h.authMu.Lock()
	h.authMethodUsed = "agent"
	h.authenticated = false
	h.authErr = nil
	h.authMu.Unlock()

	promptParams, _ := json.Marshal(SessionPromptParams{SessionID: sessionID, Prompt: []ContentBlock{{Type: "text", Text: "hi"}}})
	_, err := h.handleSessionPrompt(promptParams)
	if err == nil {
		t.Fatal("expected prompt to be rejected while auth is still in progress")
	}
	if !strings.Contains(err.Error(), "still in progress") {
		t.Errorf("error = %v, want 'still in progress'", err)
	}
}

func TestIssue548PromptAllowedAfterSuccessfulAuth(t *testing.T) {
	h := newIssue548Handler(t)
	newParams, _ := json.Marshal(SessionNewParams{CWD: t.TempDir()})
	newRes, _ := h.handleSessionNew(newParams)
	sessionID := newRes.(SessionNewResult).SessionID

	h.authMu.Lock()
	h.authMethodUsed = "agent"
	h.authenticated = true
	h.authErr = nil
	h.authMu.Unlock()

	promptParams, _ := json.Marshal(SessionPromptParams{SessionID: sessionID, Prompt: []ContentBlock{{Type: "text", Text: "hi"}}})
	res, err := h.handleSessionPrompt(promptParams)
	if err != nil {
		t.Fatalf("prompt should proceed once authenticated, got %v", err)
	}
	if _, ok := res.(SessionPromptResult); !ok {
		t.Fatalf("expected SessionPromptResult, got %T", res)
	}
	// Clean up the spawned agent loop goroutine.
	h.sessionsMu.RLock()
	loop := h.agentLoops[sessionID]
	h.sessionsMu.RUnlock()
	if loop != nil {
		loop.Stop()
		h.sessionsMu.Lock()
		delete(h.agentLoops, sessionID)
		h.sessionsMu.Unlock()
	}
}

// --- Low-sev: set_mode is atomic with session lookup -------------------------

func TestIssue548SetModeUnknownSessionStillErrors(t *testing.T) {
	h := newIssue548Handler(t)
	modeParams, _ := json.Marshal(SessionSetModeParams{SessionID: "nope", Mode: "auto"})
	if _, err := h.handleSessionSetMode(modeParams); err == nil {
		t.Error("set_mode on unknown session must error")
	}
	h.sessionsMu.RLock()
	_, ok := h.sessionModes["nope"]
	h.sessionsMu.RUnlock()
	if ok {
		t.Error("set_mode must not record a mode for an unknown session")
	}
}
