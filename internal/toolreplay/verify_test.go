package toolreplay

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/topcheer/ggcode/internal/tool"
)

// verifyTool is a stand-in for a real tool. The name must match real tool
// names ("read_file", "write_file", ...) because Verify consults
// permission.IsReadOnlyTool to decide whether live execution is safe.
type verifyTool struct {
	name  string
	fn    func(input json.RawMessage) (tool.Result, error)
	calls atomic.Int64
}

func (f *verifyTool) Name() string                { return f.name }
func (f *verifyTool) Description() string         { return "fake " + f.name }
func (f *verifyTool) Parameters() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (f *verifyTool) Execute(_ context.Context, input json.RawMessage) (tool.Result, error) {
	f.calls.Add(1)
	return f.fn(input)
}

func newRegistry(t *testing.T, tools ...*verifyTool) *tool.Registry {
	t.Helper()
	reg := tool.NewRegistry()
	for _, ft := range tools {
		if err := reg.Register(ft); err != nil {
			t.Fatalf("register %s: %v", ft.name, err)
		}
	}
	return reg
}

func tapeFromEntries(t *testing.T, entries ...Entry) *Tape {
	t.Helper()
	tape := NewTape()
	for _, e := range entries {
		tape.Record(e)
	}
	return tape
}

func okEntry(name, input, content string) Entry {
	return Entry{
		ToolName: name,
		Input:    json.RawMessage(input),
		Result:   Result{Content: content},
	}
}

func statusOf(t *testing.T, results []VerifyResult, i int) VerifyResult {
	t.Helper()
	if i >= len(results) {
		t.Fatalf("missing result %d of %d", i, len(results))
	}
	return results[i]
}

func TestVerify_AllMatch(t *testing.T) {
	ft := &verifyTool{name: "read_file", fn: func(input json.RawMessage) (tool.Result, error) {
		return tool.Result{Content: "hello world"}, nil
	}}
	reg := newRegistry(t, ft)
	tape := tapeFromEntries(t,
		okEntry("read_file", `{"path":"a.txt"}`, "hello world"),
		okEntry("read_file", `{"path":"b.txt"}`, "hello world"),
	)
	results, err := Verify(context.Background(), reg, tape, VerifyOptions{})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
	for i, res := range results {
		if res.Status != VerifyMatch {
			t.Errorf("result %d status = %q, want match (detail: %s)", i, res.Status, res.Detail)
		}
	}
	report := VerifyReport{Results: results}
	if !report.Passed() {
		t.Error("report should pass when all boundaries match")
	}
	if ft.calls.Load() != 2 {
		t.Errorf("live executions = %d, want 2", ft.calls.Load())
	}
}

func TestVerify_DivergenceDetected(t *testing.T) {
	ft := &verifyTool{name: "read_file", fn: func(input json.RawMessage) (tool.Result, error) {
		return tool.Result{Content: "changed!"}, nil
	}}
	reg := newRegistry(t, ft)
	tape := tapeFromEntries(t, okEntry("read_file", `{"path":"a.txt"}`, "original"))
	results, err := Verify(context.Background(), reg, tape, VerifyOptions{})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	res := statusOf(t, results, 0)
	if res.Status != VerifyDiverged {
		t.Fatalf("status = %q, want diverged", res.Status)
	}
	if !strings.Contains(res.Detail, "content differs") {
		t.Errorf("detail = %q, want content-differs explanation", res.Detail)
	}
	if (VerifyReport{Results: results}).Passed() {
		t.Error("diverged boundary must fail the report")
	}
}

func TestVerify_ErrorParity(t *testing.T) {
	bothErr := &verifyTool{name: "read_file", fn: func(input json.RawMessage) (tool.Result, error) {
		return tool.Result{Content: "boom v2", IsError: true}, nil
	}}
	reg := newRegistry(t, bothErr)
	recorded := Entry{ToolName: "read_file", Input: json.RawMessage(`{}`),
		Result: Result{Content: "boom v1", IsError: true}}
	tape := tapeFromEntries(t, recorded)
	results, err := Verify(context.Background(), reg, tape, VerifyOptions{})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if res := statusOf(t, results, 0); res.Status != VerifyMatch {
		t.Errorf("both-errored status = %q, want match (error class parity)", res.Status)
	}

	// Recorded error but live succeeds → divergence.
	liveOK := &verifyTool{name: "read_file", fn: func(input json.RawMessage) (tool.Result, error) {
		return tool.Result{Content: "now it works"}, nil
	}}
	results, err = Verify(context.Background(), newRegistry(t, liveOK), tape, VerifyOptions{})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if res := statusOf(t, results, 0); res.Status != VerifyDiverged {
		t.Errorf("rec-err/live-ok status = %q, want diverged", res.Status)
	}

	// Recorded success but live errors → divergence.
	failTool := &verifyTool{name: "read_file", fn: func(input json.RawMessage) (tool.Result, error) {
		return tool.Result{Content: "eproblem", IsError: true}, nil
	}}
	results, err = Verify(context.Background(), newRegistry(t, failTool),
		tapeFromEntries(t, okEntry("read_file", `{}`, "fine")), VerifyOptions{})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if res := statusOf(t, results, 0); res.Status != VerifyDiverged {
		t.Errorf("rec-ok/live-err status = %q, want diverged", res.Status)
	}
}

func TestVerify_CutServesFromRecord(t *testing.T) {
	ft := &verifyTool{name: "read_file", fn: func(input json.RawMessage) (tool.Result, error) {
		return tool.Result{Content: "same"}, nil
	}}
	reg := newRegistry(t, ft)
	tape := tapeFromEntries(t,
		okEntry("read_file", `{"i":0}`, "same"),
		okEntry("read_file", `{"i":1}`, "same"),
		okEntry("read_file", `{"i":2}`, "same"),
	)
	results, err := Verify(context.Background(), reg, tape, VerifyOptions{Cut: 2})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if res := statusOf(t, results, 0); res.Status != VerifyCut {
		t.Errorf("boundary 0 status = %q, want cut", res.Status)
	}
	if res := statusOf(t, results, 1); res.Status != VerifyCut {
		t.Errorf("boundary 1 status = %q, want cut", res.Status)
	}
	if res := statusOf(t, results, 2); res.Status != VerifyMatch {
		t.Errorf("boundary 2 status = %q, want match", res.Status)
	}
	if ft.calls.Load() != 1 {
		t.Errorf("live executions = %d, want 1 (cut boundaries must not execute)", ft.calls.Load())
	}
	// Cut larger than the tape must not panic or misbehave.
	if _, err := Verify(context.Background(), reg, tape, VerifyOptions{Cut: 99}); err != nil {
		t.Errorf("oversized cut: %v", err)
	}
}

func TestVerify_UnsafeSkippedByDefault(t *testing.T) {
	ft := &verifyTool{name: "write_file", fn: func(input json.RawMessage) (tool.Result, error) {
		return tool.Result{Content: "wrote"}, nil
	}}
	reg := newRegistry(t, ft)
	tape := tapeFromEntries(t, okEntry("write_file", `{"path":"x"}`, "wrote"))

	results, err := Verify(context.Background(), reg, tape, VerifyOptions{})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if res := statusOf(t, results, 0); res.Status != VerifySkippedUnsafe {
		t.Fatalf("status = %q, want skipped-unsafe", res.Status)
	}
	if ft.calls.Load() != 0 {
		t.Errorf("unsafe tool executed %d times, want 0", ft.calls.Load())
	}
	if !(VerifyReport{Results: results}).Passed() {
		t.Error("skipped-unsafe must not fail the report")
	}

	// IncludeUnsafe opts in to live execution of mutating tools.
	results, err = Verify(context.Background(), reg, tape, VerifyOptions{IncludeUnsafe: true})
	if err != nil {
		t.Fatalf("Verify IncludeUnsafe: %v", err)
	}
	if res := statusOf(t, results, 0); res.Status != VerifyMatch {
		t.Errorf("IncludeUnsafe status = %q, want match", res.Status)
	}
	if ft.calls.Load() != 1 {
		t.Errorf("IncludeUnsafe executions = %d, want 1", ft.calls.Load())
	}
}

func TestVerify_NoToolFails(t *testing.T) {
	reg := newRegistry(t) // no tools registered
	tape := tapeFromEntries(t, okEntry("vanished_tool", `{}`, "x"))
	results, err := Verify(context.Background(), reg, tape, VerifyOptions{})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if res := statusOf(t, results, 0); res.Status != VerifyNoTool {
		t.Fatalf("status = %q, want no-tool", res.Status)
	}
	if (VerifyReport{Results: results}).Passed() {
		t.Error("missing tool must fail the report (boundary unverifiable)")
	}
}

func TestVerify_NormalizesLineEndings(t *testing.T) {
	ft := &verifyTool{name: "read_file", fn: func(input json.RawMessage) (tool.Result, error) {
		return tool.Result{Content: "line1\nline2\n"}, nil
	}}
	reg := newRegistry(t, ft)
	tape := tapeFromEntries(t, okEntry("read_file", `{}`, "line1\r\nline2\r\n"))
	results, err := Verify(context.Background(), reg, tape, VerifyOptions{})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if res := statusOf(t, results, 0); res.Status != VerifyMatch {
		t.Errorf("CRLF/LF status = %q, want match", res.Status)
	}
}

func TestVerify_EmptyTape(t *testing.T) {
	reg := newRegistry(t)
	results, err := Verify(context.Background(), reg, NewTape(), VerifyOptions{})
	if err != nil {
		t.Fatalf("Verify empty tape: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("got %d results, want 0", len(results))
	}
	if !(VerifyReport{Results: results}).Passed() {
		t.Error("empty tape should pass trivially")
	}
}

func TestVerify_NilRegistryOrTape(t *testing.T) {
	if _, err := Verify(context.Background(), nil, NewTape(), VerifyOptions{}); err == nil {
		t.Error("nil registry should error")
	}
	reg := newRegistry(t)
	if _, err := Verify(context.Background(), reg, nil, VerifyOptions{}); err == nil {
		t.Error("nil tape should error")
	}
}

func TestVerify_DoesNotConsumeTape(t *testing.T) {
	ft := &verifyTool{name: "read_file", fn: func(input json.RawMessage) (tool.Result, error) {
		return tool.Result{Content: "same"}, nil
	}}
	reg := newRegistry(t, ft)
	tape := tapeFromEntries(t,
		okEntry("read_file", `{"i":0}`, "same"),
		okEntry("read_file", `{"i":0}`, "same"),
	)
	for range 2 {
		if _, err := Verify(context.Background(), reg, tape, VerifyOptions{}); err != nil {
			t.Fatalf("Verify: %v", err)
		}
	}
	// EntriesInOrder must still expose both entries (no slot consumption),
	// and a strict-FIFO replayer can still serve both.
	if got := tape.EntriesInOrder(); len(got) != 2 {
		t.Fatalf("entries after verify = %d, want 2", len(got))
	}
	for range 2 {
		if _, ok := tape.Lookup("read_file", json.RawMessage(`{"i":0}`), true); !ok {
			t.Fatal("tape slot consumed by verification")
		}
	}
}

func TestVerifyReport_Counts(t *testing.T) {
	report := VerifyReport{Results: []VerifyResult{
		{Status: VerifyMatch},
		{Status: VerifyMatch},
		{Status: VerifyDiverged},
		{Status: VerifyCut},
		{Status: VerifySkippedUnsafe},
	}}
	counts := report.Counts()
	if counts[VerifyMatch] != 2 || counts[VerifyDiverged] != 1 ||
		counts[VerifyCut] != 1 || counts[VerifySkippedUnsafe] != 1 {
		t.Fatalf("unexpected counts: %v", counts)
	}
}
