package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// E2E: 5MB-output command through the real RunCommand pipeline must return
// bounded output with head+tail content preserved.
func TestRunCommand_HeavyOutputBounded_E2E(t *testing.T) {
	r := RunCommand{}
	args := json.RawMessage(`{"command":"awk 'BEGIN{srand();for(i=1;i<=200000;i++){printf \"HEADMARK-%06d payload line with some content\\n\", i} print \"TAILMARK-final-line\"}'","timeout":120}`)
	res, err := r.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	out := res.Content
	if len(out) > 300*1024 {
		t.Fatalf("output not bounded: %d bytes", len(out))
	}
	if !strings.Contains(out, "HEADMARK-000001") {
		t.Errorf("head content lost")
	}
	if !strings.Contains(out, "TAILMARK-final-line") {
		t.Errorf("tail content lost")
	}
	t.Logf("retained %d bytes from 5MB stream; head+tail OK", len(out))
}

// E2E: stderr-heavy commands (test suites, linters) must be bounded too -
// both streams run through independent boundedOutputWriter instances.
func TestRunCommand_HeavyStderrBounded_E2E(t *testing.T) {
	r := RunCommand{}
	args := json.RawMessage(`{"command":"awk 'BEGIN{for(i=1;i<=200000;i++){printf \"ERRHEAD-%06d stderr payload line\\n\", i > \"/dev/stderr\"} print \"ERRTAIL-final\" > \"/dev/stderr\"; print \"stdout-done\"}'","timeout":120}`)
	res, err := r.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	out := res.Content
	if len(out) > 300*1024 {
		t.Fatalf("combined output not bounded: %d bytes", len(out))
	}
	if !strings.Contains(out, "ERRHEAD-000001") {
		t.Errorf("stderr head content lost")
	}
	if !strings.Contains(out, "ERRTAIL-final") {
		t.Errorf("stderr tail content lost")
	}
	if !strings.Contains(out, "stdout-done") {
		t.Errorf("stdout content lost when stderr dominates")
	}
	t.Logf("retained %d bytes from stderr-heavy stream", len(out))
}
