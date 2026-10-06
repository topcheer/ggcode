package hooks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Session lifecycle hooks (on_session_start / on_session_end): Claude Code
// hooks parity (https://code.claude.com/docs/en/hooks). Start is blocking
// with stdout always collected; end is synchronous, non-blocking.

func TestSessionStartHookBlocking(t *testing.T) {
	cfg := HookConfig{OnSessionStart: []Hook{{Match: "*", Command: "echo locked >&2; exit 2"}}}
	res := RunSessionStartHooks(cfg, HookEnv{SessionSource: "startup"})
	if res.Allowed {
		t.Fatal("expected on_session_start exit 2 to block")
	}
	if !strings.Contains(res.Output, "locked") {
		t.Fatalf("block reason lost, output = %q", res.Output)
	}
}

func TestSessionStartHookOutputCollected(t *testing.T) {
	cfg := HookConfig{OnSessionStart: []Hook{{Match: "*", Command: "echo branch: main"}}}
	res := RunSessionStartHooks(cfg, HookEnv{})
	if !res.Allowed {
		t.Fatalf("non-blocking hook must allow, output = %q", res.Output)
	}
	if !strings.Contains(res.Output, "branch: main") {
		t.Fatalf("stdout not collected, output = %q", res.Output)
	}
}

func TestSessionStartSourceEnvVar(t *testing.T) {
	cfg := HookConfig{OnSessionStart: []Hook{{Match: "*", Command: "echo $GGCODE_SESSION_SOURCE"}}}
	res := RunSessionStartHooks(cfg, HookEnv{SessionSource: "resume"})
	if got := strings.TrimSpace(res.Output); got != "resume" {
		t.Fatalf("GGCODE_SESSION_SOURCE = %q, want resume", got)
	}
}

func TestSessionEndHookRunsSynchronously(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "ended")
	cfg := HookConfig{OnSessionEnd: []Hook{{Match: "*", Command: "touch ended"}}}
	res := RunSessionEndHooks(cfg, HookEnv{WorkingDir: dir, SessionEndReason: "exit"})
	if res.Err != nil {
		t.Fatalf("on_session_end hook error: %v", res.Err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("on_session_end hook did not run synchronously: %v", err)
	}
}

func TestSessionEndIsNotBlocking(t *testing.T) {
	cfg := HookConfig{OnSessionEnd: []Hook{{Match: "*", Command: "echo nope >&2; exit 2"}}}
	res := RunSessionEndHooks(cfg, HookEnv{SessionEndReason: "exit"})
	if !res.Allowed {
		t.Fatal("on_session_end must not block; a verdict cannot stop the exit")
	}
}

func TestSessionPayload(t *testing.T) {
	p := BuildPayload(HookEnv{Event: EventOnSessionStart, SessionSource: "resume"})
	if p.Session == nil || p.Session.Source != "resume" || p.Session.Reason != "" {
		t.Fatalf("session-start payload wrong: %+v", p.Session)
	}
	p2 := BuildPayload(HookEnv{Event: EventOnSessionEnd, SessionEndReason: "exit"})
	if p2.Session == nil || p2.Session.Reason != "exit" || p2.Session.Source != "" {
		t.Fatalf("session-end payload wrong: %+v", p2.Session)
	}
	b := p.JSON()
	if !strings.Contains(string(b), `"source":"resume"`) {
		t.Fatalf("payload JSON missing session.source: %s", b)
	}
}

func TestValidateSessionHooks(t *testing.T) {
	errs := ValidateHooks(HookConfig{
		OnSessionStart: []Hook{{Match: "*", Command: "true"}},
		OnSessionEnd:   []Hook{{Match: "*", Type: HookTypeHTTP}},
	})
	if len(errs) != 1 || !strings.Contains(errs[0], "on_session_end[0]") {
		t.Fatalf("validation errors = %v, want exactly on_session_end[0] missing url", errs)
	}
}
