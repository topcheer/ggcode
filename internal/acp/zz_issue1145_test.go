package acp

// Characteristic tests for issue #1145.
//
// handleSetConfigOption previously treated the "mode" config option as
// echo-only: it updated CurrentValue in the response copy without writing
// h.sessionModes or applying the mode to the active agent loop, silently
// ignoring permission downgrades (auto -> supervised). These tests pin the
// fixed behavior:
//
//   - a mode set via session/set_config_option persists in h.sessionModes and
//     is applied to any registered active loop (same semantics as set_mode)
//   - an unknown session is rejected instead of returning success
//   - values not declared in the select options are rejected (no "banana")
//   - a valid value echoes back consistently with what was actually applied
//   - non-mode config options keep their previous echo-only behavior

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/tool"
)

func newIssue1145Handler(t *testing.T) *Handler {
	t.Helper()
	var buf bytes.Buffer
	transport := NewTransport(strings.NewReader(""), &buf)
	h := NewHandler(&config.Config{}, tool.NewRegistry(), transport, nil)
	h.initialized = true
	// Redirect session storage into a temp dir so the test is hermetic.
	h.sessionsDir = t.TempDir()
	return h
}

func issue1145RegisterSession(t *testing.T, h *Handler) string {
	t.Helper()
	newParams, _ := json.Marshal(SessionNewParams{CWD: t.TempDir()})
	newRes, err := h.handleSessionNew(newParams)
	if err != nil {
		t.Fatalf("session/new error: %v", err)
	}
	return newRes.(SessionNewResult).SessionID
}

func issue1145AttachLoop(t *testing.T, h *Handler, sessionID string) *AgentLoop {
	t.Helper()
	loop := NewAgentLoop(h.cfg, h.toolRegistry, h.transport, h.sessions[sessionID], h.clientCaps, nil)
	h.sessionsMu.Lock()
	h.agentLoops[sessionID] = loop
	h.sessionsMu.Unlock()
	t.Cleanup(func() {
		h.sessionsMu.Lock()
		delete(h.agentLoops, sessionID)
		h.sessionsMu.Unlock()
	})
	return loop
}

func issue1145ModeValue(t *testing.T, res interface{}) SessionConfigValueId {
	t.Helper()
	resp, ok := res.(SetSessionConfigOptionResponse)
	if !ok {
		t.Fatalf("unexpected response type %T", res)
	}
	for _, opt := range resp.ConfigOptions {
		if opt.ID == "mode" {
			return opt.CurrentValue
		}
	}
	t.Fatal("mode config option missing from response")
	return ""
}

// TestIssue1145ConfigOptionModeDowngradeReallyApplies verifies that setting
// mode=supervised through session/set_config_option is not just echoed back:
// h.sessionModes records the downgrade and the active loop adopts it, matching
// handleSessionSetMode semantics.
func TestIssue1145ConfigOptionModeDowngradeReallyApplies(t *testing.T) {
	h := newIssue1145Handler(t)
	sessionID := issue1145RegisterSession(t, h)
	loop := issue1145AttachLoop(t, h, sessionID)

	params, _ := json.Marshal(SetSessionConfigOptionRequest{
		SessionID: sessionID,
		ConfigID:  "mode",
		Value:     "supervised",
	})
	res, err := h.handleSetConfigOption(params)
	if err != nil {
		t.Fatalf("set_config_option error: %v", err)
	}

	h.sessionsMu.RLock()
	got, ok := h.sessionModes[sessionID]
	h.sessionsMu.RUnlock()
	if !ok || got != "supervised" {
		t.Fatalf("h.sessionModes[%s] = (%q, %v), want (supervised, true)", sessionID, got, ok)
	}

	if applied := loop.Mode(); applied != "supervised" {
		t.Errorf("active loop mode = %q, want supervised (loop.SetMode must be called for #1145)", applied)
	}

	if echo := issue1145ModeValue(t, res); echo != "supervised" {
		t.Errorf("response CurrentValue for mode = %q, want supervised", echo)
	}
}

// TestIssue1145ConfigOptionUnknownSessionErrors verifies the additional defect:
// set_config_option on a nonexistent session must fail like set_mode does,
// instead of returning a success response with default config options.
func TestIssue1145ConfigOptionUnknownSessionErrors(t *testing.T) {
	h := newIssue1145Handler(t)

	params, _ := json.Marshal(SetSessionConfigOptionRequest{
		SessionID: "does-not-exist",
		ConfigID:  "mode",
		Value:     "auto",
	})
	if _, err := h.handleSetConfigOption(params); err == nil {
		t.Fatal("set_config_option on unknown session should return an error")
	} else if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error should mention missing session, got: %v", err)
	}

	h.sessionsMu.RLock()
	_, recorded := h.sessionModes["does-not-exist"]
	h.sessionsMu.RUnlock()
	if recorded {
		t.Error("no mode must be recorded for an unknown session")
	}
}

// TestIssue1145ConfigOptionInvalidModeValueRejected verifies that values which
// are not declared select options are rejected instead of being echoed back
// as accepted (the "banana" case), and that nothing is applied on rejection.
func TestIssue1145ConfigOptionInvalidModeValueRejected(t *testing.T) {
	h := newIssue1145Handler(t)
	sessionID := issue1145RegisterSession(t, h)
	loop := issue1145AttachLoop(t, h, sessionID)

	params, _ := json.Marshal(SetSessionConfigOptionRequest{
		SessionID: sessionID,
		ConfigID:  "mode",
		Value:     "banana",
	})
	if _, err := h.handleSetConfigOption(params); err == nil {
		t.Fatal("set_config_option with undeclared value \"banana\" should return an error")
	}

	h.sessionsMu.RLock()
	_, recorded := h.sessionModes[sessionID]
	h.sessionsMu.RUnlock()
	if recorded {
		t.Error("invalid value must not be persisted into h.sessionModes")
	}
	if applied := loop.Mode(); applied != "auto" {
		t.Errorf("active loop mode = %q after invalid value, want unchanged auto", applied)
	}
}

// TestIssue1145ConfigOptionValidEchoConsistentWithoutLoop verifies the happy
// path when no agent loop is active yet: a valid value still succeeds, is
// persisted for later prompts, and the response echo matches the applied value.
func TestIssue1145ConfigOptionValidEchoConsistentWithoutLoop(t *testing.T) {
	h := newIssue1145Handler(t)
	sessionID := issue1145RegisterSession(t, h)

	params, _ := json.Marshal(SetSessionConfigOptionRequest{
		SessionID: sessionID,
		ConfigID:  "mode",
		Value:     "bypass",
	})
	res, err := h.handleSetConfigOption(params)
	if err != nil {
		t.Fatalf("set_config_option error: %v", err)
	}

	h.sessionsMu.RLock()
	got, ok := h.sessionModes[sessionID]
	h.sessionsMu.RUnlock()
	if !ok || got != "bypass" {
		t.Fatalf("h.sessionModes[%s] = (%q, %v), want (bypass, true)", sessionID, got, ok)
	}

	if echo := issue1145ModeValue(t, res); echo != "bypass" {
		t.Errorf("response CurrentValue for mode = %q, want bypass (echo must match applied value)", echo)
	}
}

// TestIssue1145NonModeConfigOptionBehaviorUnchanged pins requirement 3: config
// options other than "mode" keep their previous behavior. The server currently
// only declares the "mode" select option, so an unknown/non-mode configId
// succeeds without error, leaves every echoed option unchanged (mode stays at
// its current value), and never touches permission-mode bookkeeping.
func TestIssue1145NonModeConfigOptionBehaviorUnchanged(t *testing.T) {
	h := newIssue1145Handler(t)
	sessionID := issue1145RegisterSession(t, h)

	params, _ := json.Marshal(SetSessionConfigOptionRequest{
		SessionID: sessionID,
		ConfigID:  "model",
		Value:     "some-model-id",
	})
	res, err := h.handleSetConfigOption(params)
	if err != nil {
		t.Fatalf("set_config_option non-mode error: %v", err)
	}

	resp, ok := res.(SetSessionConfigOptionResponse)
	if !ok {
		t.Fatalf("unexpected response type %T", res)
	}
	if len(resp.ConfigOptions) != 1 || resp.ConfigOptions[0].ID != "mode" {
		t.Fatalf("response must echo only the declared mode option unchanged, got %+v", resp.ConfigOptions)
	}
	if resp.ConfigOptions[0].CurrentValue != "auto" {
		t.Errorf("mode option must stay untouched by non-mode updates, got %q", resp.ConfigOptions[0].CurrentValue)
	}

	h.sessionsMu.RLock()
	_, recorded := h.sessionModes[sessionID]
	h.sessionsMu.RUnlock()
	if recorded {
		t.Error("non-mode config update must not write h.sessionModes")
	}
}
