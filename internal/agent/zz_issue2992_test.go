package agent

// Tests for #2992: post-edit detector wiring fixes.
// - case 2 core: bgVerifyRegistry register/take semantics (consume-once,
//   bounded, nil-safe) and job_id extraction from raw tool arguments.

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestIssue2992BgVerifyRegistryConsumeOnce(t *testing.T) {
	r := newBgVerifyRegistry()
	r.register("job-1", "go test ./...")
	cmd, ok := r.take("job-1")
	if !ok || cmd != "go test ./..." {
		t.Fatalf("take #1: ok=%v cmd=%q", ok, cmd)
	}
	// Consume-once: a repeated wait_command poll must not re-clear debt.
	if _, ok := r.take("job-1"); ok {
		t.Fatal("take #2 should miss: registry is consumed-once")
	}
}

func TestIssue2992BgVerifyRegistryBounded(t *testing.T) {
	r := newBgVerifyRegistry()
	for i := 0; i < 80; i++ {
		r.register(string(rune('a'+i%26))+string(rune('0'+i/26)), "cmd")
	}
	r.mu.Lock()
	n := len(r.jobs)
	r.mu.Unlock()
	if n > 64 {
		t.Fatalf("registry unbounded: %d entries", n)
	}
}

func TestIssue2992BgVerifyRegistryEmptyInputs(t *testing.T) {
	var r *bgVerifyRegistry
	r.register("j", "c") // nil-safe, must not panic
	if _, ok := r.take("j"); ok {
		t.Fatal("nil registry take should miss")
	}
	ok := newBgVerifyRegistry()
	ok.register("", "c")
	ok.register("j", "")
	if _, found := ok.take("j"); found {
		t.Fatal("empty jobID or cmd must not register")
	}
}

func TestIssue2992BgVerifyExtractJobID(t *testing.T) {
	cases := []struct {
		raw  string
		want string
	}{
		{`{"job_id":"j-42","since_line":0}`, "j-42"},
		{`{"task_id":"t-7"}`, "t-7"},
		{`{"job_id":"","task_id":"t-9"}`, "t-9"},
		{`{"job_id":"j-1"}`, "j-1"},
		{`not json`, ""},
		{`{}`, ""},
	}
	for _, c := range cases {
		if got := bgVerifyExtractJobID(json.RawMessage(c.raw)); got != c.want {
			t.Errorf("bgVerifyExtractJobID(%s)=%q want %q", c.raw, got, c.want)
		}
	}
}

// The wait/read completion path grades from the rendered job snapshot via
// psTerminalVerifyOutcome(psParseJobStatus(...)). Pin the outcomes that the
// wiring treats as a green background verification.
func TestIssue2992BackgroundOutcomeGrading(t *testing.T) {
	cases := []struct {
		snapshot string
		passed   bool
		terminal bool
	}{
		{"Command: go test ./...\nStatus: completed (exit 0)\nTotal lines: 3", true, true},
		{"Status: running\n", false, false},        // still running: no clear
		{"Status: failed (exit 1)\n", false, true}, // terminal failure: no clear
		{"Status: cancelled\n", false, true},       // terminal non-pass: no clear
		{"garbage with no status header", false, false},
	}
	for _, c := range cases {
		terminal, passed := psTerminalVerifyOutcome(psParseJobStatus(c.snapshot))
		if terminal != c.terminal || (terminal && passed != c.passed) {
			t.Errorf("snapshot %q -> terminal=%v passed=%v; want terminal=%v passed=%v",
				strings.SplitN(c.snapshot, "\n", 2)[0], terminal, passed, c.terminal, c.passed)
		}
	}
}
