package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #2620: GGCODE_TOOL_TAPE=replay:<path> whose tape cannot be loaded must
// NEVER silently degrade to real execution - that would invert the header
// promise ("never invoke real tools") exactly when the operator most needs
// it (e.g. replaying a session full of destructive run_command calls under
// bypass permissions with a typo'd tape path). The fix fails closed: the
// state enters toolTapeReplayFailed and every tool call returns an
// explicit IsError result, mirroring tape-miss semantics. RECORD-mode and
// env-parse failures still degrade to "off" (harmless: fewer recordings).
func TestIssue2620ReplayLoadFailureFailsClosed(t *testing.T) {
	t.Setenv(toolTapeEnv, "replay:"+filepath.Join(t.TempDir(), "does-not-exist.tape.json"))
	st := newToolTapeState()
	if st == nil {
		t.Fatal("state must be non-nil")
	}
	if st.mode != toolTapeReplayFailed {
		t.Fatalf("mode = %v, want toolTapeReplayFailed (NOT off - silent real execution is the #2620 bug)", st.mode)
	}

	a := &Agent{toolTape: st}
	res, _, handled := a.replayToolCall("run_command", json.RawMessage(`{"command":"rm -rf /"}`))
	if !handled {
		t.Fatal("replayToolCall must handle (block) the call under replayFailed - an unhandled return means real execution")
	}
	if !res.IsError {
		t.Fatal("blocked call result must be IsError")
	}
	for _, want := range []string{"tape failed to load", "NOT executed"} {
		if !strings.Contains(res.Content, want) {
			t.Fatalf("result %q must mention %q", res.Content, want)
		}
	}

	// Repeated calls stay blocked: no state transition can re-enable
	// real execution mid-session.
	res2, _, handled2 := a.replayToolCall("read_file", json.RawMessage(`{"path":"/etc/hosts"}`))
	if !handled2 || !res2.IsError {
		t.Fatal("second blocked call must also be handled with IsError")
	}
}

// The fail-closed error must carry the underlying load error (ENOENT,
// corrupt JSON, permission denied) so the operator can tell a typo'd path
// from a damaged tape without enabling debug logging.
func TestIssue2620FailClosedMessageCarriesLoadError(t *testing.T) {
	bad := filepath.Join(t.TempDir(), "corrupt.tape.json")
	if err := os.WriteFile(bad, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(toolTapeEnv, "replay:"+bad)
	st := newToolTapeState()
	if st.mode != toolTapeReplayFailed {
		t.Fatalf("mode = %v, want toolTapeReplayFailed", st.mode)
	}
	if st.loadErr == "" {
		t.Fatal("loadErr must record the underlying failure for the message")
	}
	a := &Agent{toolTape: st}
	res, _, handled := a.replayToolCall("grep", json.RawMessage(`{}`))
	if !handled || !res.IsError || !strings.Contains(res.Content, st.loadErr) {
		t.Fatalf("blocked result must be IsError and carry loadErr %q, got %+v", st.loadErr, res)
	}
}

// Regression guard for the non-replay degradation paths that stay "off":
// a malformed env value or RECORD mode never blocks real execution
// (#2620 fix must not over-block the harmless cases).
func TestIssue2620NonReplayFailuresStillDegradeToOff(t *testing.T) {
	t.Setenv(toolTapeEnv, "bogus-value")
	if st := newToolTapeState(); st.mode != toolTapeOff {
		t.Fatalf("bad env value: mode = %v, want off", st.mode)
	}
	t.Setenv(toolTapeEnv, "record:"+filepath.Join(t.TempDir(), "out.tape.json"))
	if st := newToolTapeState(); st.mode != toolTapeRecord {
		t.Fatalf("record mode: mode = %v, want record", st.mode)
	}
	// Off state stays fully inert (not handled -> real execution, correct).
	a := &Agent{toolTape: &toolTapeState{mode: toolTapeOff}}
	if _, _, handled := a.replayToolCall("grep", json.RawMessage(`{}`)); handled {
		t.Fatal("off mode must not handle calls")
	}
}
