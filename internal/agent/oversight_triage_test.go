package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

func tc(name, argsJSON string) provider.ToolCallDelta {
	return provider.ToolCallDelta{Name: name, Arguments: json.RawMessage(argsJSON)}
}

func TestOversightCriticalFileEditIsNovel(t *testing.T) {
	o := newOversightTriageState()
	o.record(tc("edit_file", `{"file_path":"go.mod"}`))
	o.record(tc("read_file", `{"path":"main.go"}`))
	d := o.digest()
	if !strings.Contains(d, "go.mod") || !strings.Contains(d, "supply-chain") {
		t.Fatalf("digest should flag go.mod edit as supply-chain novel, got: %q", d)
	}
	if !strings.Contains(d, "1 routine actions folded") {
		t.Fatalf("routine read should fold into count, got: %q", d)
	}
}

func TestOversightRoutineRunStaysSilent(t *testing.T) {
	o := newOversightTriageState()
	o.record(tc("edit_file", `{"file_path":"internal/agent/foo.go"}`))
	o.record(tc("run_command", `{"command":"go test ./..."}`))
	if d := o.digest(); d != "" {
		t.Fatalf("routine-only run must stay silent, got: %q", d)
	}
}

func TestOversightWideBlastAndIrreversible(t *testing.T) {
	files := `[` + strings.Repeat(`"a.go",`, 6) + `"b.go"]`
	o := newOversightTriageState()
	o.record(tc("batch_replace", `{"pattern":"x","replacement":"y","files":`+files+`}`))
	o.record(tc("git_push", `{}`))
	o.record(tc("git_reset", `{"mode":"hard"}`))
	d := o.digest()
	for _, want := range []string{"wide blast", "git_push", "hard reset"} {
		if !strings.Contains(d, want) {
			t.Fatalf("digest missing %q, got: %q", want, d)
		}
	}
}

func TestOversightDigestEmitsOnceAndResetClears(t *testing.T) {
	o := newOversightTriageState()
	o.record(tc("git_push", `{}`))
	if d := o.digest(); d == "" {
		t.Fatal("first digest should emit")
	}
	if d := o.digest(); d != "" {
		t.Fatal("second digest must be suppressed")
	}
	o.reset()
	o.record(tc("edit_file", `{"file_path":"package.json"}`))
	d := o.digest()
	if !strings.Contains(d, "package.json") {
		t.Fatalf("after reset a new novel decision must digest, got: %q", d)
	}
}

func TestOversightFileOpsDeleteDetected(t *testing.T) {
	o := newOversightTriageState()
	o.record(tc("file_ops", `{"operations":[{"action":"delete","source":"/tmp/x"}]}`))
	d := o.digest()
	if !strings.Contains(d, "destructive delete") {
		t.Fatalf("file_ops delete should be novel, got: %q", d)
	}
}

func TestOversightMalformedArgsAreRoutine(t *testing.T) {
	o := newOversightTriageState()
	o.record(provider.ToolCallDelta{Name: "edit_file", Arguments: json.RawMessage(`{bad json`)})
	if d := o.digest(); d != "" {
		t.Fatalf("malformed args must classify routine, got digest: %q", d)
	}
}
