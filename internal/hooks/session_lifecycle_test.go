package hooks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestValidateSessionLifecycleHooks(t *testing.T) {
	valid := HookConfig{
		SessionStart: []Hook{{Match: "*", Type: HookTypeCommand, Command: "echo start"}},
		SessionEnd:   []Hook{{Match: "*", Type: HookTypeHTTP, URL: "http://example.com/hook"}},
	}
	if errs := ValidateHooks(valid); len(errs) != 0 {
		t.Fatalf("expected valid config, got errors: %v", errs)
	}

	invalid := HookConfig{
		SessionStart: []Hook{{Match: "*", Type: HookTypeCommand}},                 // missing command
		SessionEnd:   []Hook{{Match: "", Type: HookTypeCommand, Command: "true"}}, // missing match
	}
	errs := ValidateHooks(invalid)
	if len(errs) != 2 {
		t.Fatalf("expected 2 validation errors, got %d: %v", len(errs), errs)
	}
	if !strings.Contains(errs[0], "on_session_start[0]") {
		t.Errorf("expected error prefixed with on_session_start[0], got: %s", errs[0])
	}
	if !strings.Contains(errs[1], "on_session_end[0]") {
		t.Errorf("expected error prefixed with on_session_end[0], got: %s", errs[1])
	}
}

func TestBuildPayloadSessionLifecycle(t *testing.T) {
	start := BuildPayload(HookEnv{
		Event:         EventSessionStart,
		SessionID:     "ses-1",
		SessionSource: "startup",
	})
	if start.Session == nil {
		t.Fatal("expected session payload for on_session_start")
	}
	if start.Session.Source != "startup" {
		t.Errorf("expected source=startup, got %q", start.Session.Source)
	}
	if start.Session.Reason != "" {
		t.Errorf("expected empty reason on start payload, got %q", start.Session.Reason)
	}

	end := BuildPayload(HookEnv{
		Event:            EventSessionEnd,
		SessionID:        "ses-1",
		SessionEndReason: "cleared",
	})
	if end.Session == nil {
		t.Fatal("expected session payload for on_session_end")
	}
	if end.Session.Reason != "cleared" {
		t.Errorf("expected reason=cleared, got %q", end.Session.Reason)
	}
	if end.Session.Source != "" {
		t.Errorf("expected empty source on end payload, got %q", end.Session.Source)
	}
}

// TestDispatchSessionLifecycleHooks verifies that on_session_start and
// on_session_end hooks are routed to the configured hook lists and executed
// asynchronously (the marker file only appears after the goroutine runs).
func TestDispatchSessionLifecycleHooks(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "marker.txt")

	writeMarker := "echo fired >> " + shellQuote(marker)
	cfg := HookConfig{
		SessionStart: []Hook{{Match: "*", Type: HookTypeCommand, Command: writeMarker}},
		SessionEnd:   []Hook{{Match: "*", Type: HookTypeCommand, Command: writeMarker}},
	}

	Dispatch(cfg, HookEnv{Event: EventSessionStart, SessionID: "s1"})
	Dispatch(cfg, HookEnv{Event: EventSessionEnd, SessionID: "s1"})

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(marker)
		if err == nil && strings.Count(string(data), "fired") == 2 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("timed out waiting for session lifecycle hooks to run (expected 2 marker writes)")
}

// TestDispatchSessionLifecycleUnconfigured ensures no panic and a permissive
// result when no session hooks are configured.
func TestDispatchSessionLifecycleUnconfigured(t *testing.T) {
	res := Dispatch(HookConfig{}, HookEnv{Event: EventSessionStart, SessionSource: "resume"})
	if !res.Allowed {
		t.Error("unconfigured dispatch should return Allowed=true")
	}
}
