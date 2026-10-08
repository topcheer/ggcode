package tool

// sa-148 (tool composition) probes: the macro builtin must (a) persist and
// replay reusable read-only tool-call sequences with positional
// placeholders, (b) refuse write tools at define time so macros can never
// bypass the per-call permission flow, (c) stop at the first failing step
// during replay, (d) survive corrupt store files fail-open.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeProbeTool is a minimal Tool registered under a temporarily
// read-only-whitelisted name so macros can call it in tests.
type fakeProbeTool struct {
	name string
	out  string
	err  bool
}

func (f *fakeProbeTool) Name() string        { return f.name }
func (f *fakeProbeTool) Description() string { return "fake probe" }
func (f *fakeProbeTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"}}}`)
}
func (f *fakeProbeTool) Execute(_ context.Context, input json.RawMessage) (Result, error) {
	if f.err {
		return Result{IsError: true, Content: "probe failure"}, nil
	}
	var args struct {
		Q string `json:"q"`
	}
	_ = json.Unmarshal(input, &args)
	return Result{Content: f.out + ":" + args.Q}, nil
}

func newMacroTestEnv(t *testing.T) (*MacroTool, *Registry) {
	t.Helper()
	readOnlyToolNames["zz_probe"] = true
	readOnlyToolNames["zz_fail"] = true
	t.Cleanup(func() {
		delete(readOnlyToolNames, "zz_probe")
		delete(readOnlyToolNames, "zz_fail")
	})
	reg := NewRegistry()
	if err := reg.Register(&fakeProbeTool{name: "zz_probe", out: "probe"}); err != nil {
		t.Fatal(err)
	}
	if err := reg.Register(&fakeProbeTool{name: "zz_fail", out: "x", err: true}); err != nil {
		t.Fatal(err)
	}
	return &MacroTool{WorkingDir: t.TempDir(), Registry: reg}, reg
}

func macroExec(t *testing.T, m *MacroTool, payload string) Result {
	t.Helper()
	res, err := m.Execute(context.Background(), json.RawMessage(payload))
	if err != nil {
		t.Fatalf("Execute(%s) error: %v", payload, err)
	}
	return res
}

// P1: define -> list -> get -> run round trip with {{1}} placeholder
// substitution across two steps.
func TestSA148_MacroDefineRunRoundTrip(t *testing.T) {
	m, _ := newMacroTestEnv(t)
	def := macroExec(t, m, `{"action":"define","name":"release-check","description":"pre-release checks","steps":[
		{"tool":"zz_probe","args":{"q":"base"}},
		{"tool":"zz_probe","args":{"q":"{{1}}"}}
	]}`)
	if def.IsError {
		t.Fatalf("define failed: %s", def.Content)
	}

	run := macroExec(t, m, `{"action":"run","name":"release-check","args":["v1.2.3"]}`)
	if run.IsError {
		t.Fatalf("run failed: %s", run.Content)
	}
	if !strings.Contains(run.Content, "probe:base") || !strings.Contains(run.Content, "probe:v1.2.3") {
		t.Fatalf("run output missing step results: %s", run.Content)
	}
	if !strings.Contains(run.Content, "2 step(s)") {
		t.Fatalf("run output should report 2 steps: %s", run.Content)
	}

	lst := macroExec(t, m, `{"action":"list"}`)
	if !strings.Contains(lst.Content, "release-check") || !strings.Contains(lst.Content, "pre-release checks") {
		t.Fatalf("list missing macro: %s", lst.Content)
	}
	got := macroExec(t, m, `{"action":"get","name":"release-check"}`)
	if !strings.Contains(got.Content, `"tool": "zz_probe"`) {
		t.Fatalf("get output missing step: %s", got.Content)
	}
	del := macroExec(t, m, `{"action":"delete","name":"release-check"}`)
	if del.IsError {
		t.Fatalf("delete failed: %s", del.Content)
	}
	run2 := macroExec(t, m, `{"action":"run","name":"release-check"}`)
	if !run2.IsError {
		t.Fatal("run after delete must fail")
	}
}

// P2: write tools are rejected at define time - a macro must never
// bypass the normal per-call permission flow.
func TestSA148_MacroRejectsWriteTools(t *testing.T) {
	m, _ := newMacroTestEnv(t)
	for _, bad := range []string{"edit_file", "write_file", "run_command", "macro"} {
		res := macroExec(t, m, `{"action":"define","name":"evil-macro","steps":[{"tool":"`+bad+`"}]}`)
		if !res.IsError {
			t.Fatalf("define with write tool %q must be rejected", bad)
		}
		if !strings.Contains(res.Content, "read-only") {
			t.Fatalf("rejection must explain the read-only contract: %s", res.Content)
		}
	}
}

// P3: validation - bad name, empty steps, too many steps, unknown tool.
func TestSA148_MacroValidation(t *testing.T) {
	m, _ := newMacroTestEnv(t)
	cases := []struct {
		name, payload, wantErr string
	}{
		{"bad name", `{"action":"define","name":"Bad Name!","steps":[{"tool":"zz_probe"}]}`, "invalid macro name"},
		{"empty steps", `{"action":"define","name":"empty-macro","steps":[]}`, "steps"},
		{"unknown tool", `{"action":"define","name":"ghost-macro","steps":[{"tool":"no_such_tool"}]}`, "read-only"},
		{"run unknown macro", `{"action":"run","name":"ghost-macro"}`, "not found"},
	}
	for _, c := range cases {
		res := macroExec(t, m, c.payload)
		if !res.IsError || !strings.Contains(res.Content, c.wantErr) {
			t.Fatalf("%s: want error containing %q, got %+v", c.name, c.wantErr, res)
		}
	}
	tooMany := `{"action":"define","name":"long-macro","steps":[`
	for i := 0; i < 9; i++ {
		if i > 0 {
			tooMany += ","
		}
		tooMany += `{"tool":"zz_probe"}`
	}
	tooMany += `]}`
	res := macroExec(t, m, tooMany)
	if !res.IsError {
		t.Fatal("9 steps must be rejected (max 8)")
	}
}

// P4: replay stops at the first failing step and reports which one.
func TestSA148_MacroRunStopsAtFailure(t *testing.T) {
	m, _ := newMacroTestEnv(t)
	def := macroExec(t, m, `{"action":"define","name":"boom-macro","steps":[
		{"tool":"zz_probe","args":{"q":"first"}},
		{"tool":"zz_fail"},
		{"tool":"zz_probe","args":{"q":"never-reached"}}
	]}`)
	if def.IsError {
		t.Fatalf("define failed: %s", def.Content)
	}
	run := macroExec(t, m, `{"action":"run","name":"boom-macro"}`)
	if !run.IsError {
		t.Fatal("run must report failure")
	}
	if !strings.Contains(run.Content, "stopped at step 2") {
		t.Fatalf("must stop at step 2: %s", run.Content)
	}
	if strings.Contains(run.Content, "never-reached") {
		t.Fatal("step 3 must not execute after step 2 failed")
	}
}

// P5: persistence - a fresh MacroTool over the same dir sees saved macros
// (workspace-shared asset, not per-session).
func TestSA148_MacroPersistsAcrossInstances(t *testing.T) {
	m, reg := newMacroTestEnv(t)
	if res := macroExec(t, m, `{"action":"define","name":"persist-macro","steps":[{"tool":"zz_probe"}]}`); res.IsError {
		t.Fatalf("define failed: %s", res.Content)
	}
	m2 := &MacroTool{WorkingDir: m.WorkingDir, Registry: reg}
	run := macroExec(t, m2, `{"action":"run","name":"persist-macro"}`)
	if run.IsError {
		t.Fatalf("second instance must see the macro: %s", run.Content)
	}
	// And it really is on disk.
	if _, err := os.Stat(filepath.Join(m.WorkingDir, ".ggcode", macroFileName)); err != nil {
		t.Fatalf("store file missing: %v", err)
	}
}

// P6: corrupt store is quarantined fail-open, not a hard wedge.
func TestSA148_MacroCorruptStoreFailOpen(t *testing.T) {
	m, _ := newMacroTestEnv(t)
	if err := os.MkdirAll(filepath.Join(m.WorkingDir, ".ggcode"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(m.WorkingDir, ".ggcode", macroFileName), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	lst := macroExec(t, m, `{"action":"list"}`)
	if lst.IsError {
		t.Fatalf("corrupt store must fail open: %s", lst.Content)
	}
	if !strings.Contains(lst.Content, "no macros") {
		t.Fatalf("expected empty store after quarantine: %s", lst.Content)
	}
	if _, err := os.Stat(filepath.Join(m.WorkingDir, ".ggcode", macroFileName)); !os.IsNotExist(err) {
		t.Fatal("corrupt file should have been renamed to .corrupt")
	}
}

// P7: placeholder expansion JSON-escapes values so injection cannot break
// out of the string literal.
func TestSA148_MacroPlaceholderEscaping(t *testing.T) {
	m, _ := newMacroTestEnv(t)
	if res := macroExec(t, m, `{"action":"define","name":"esc-macro","steps":[{"tool":"zz_probe","args":{"q":"{{1}}"}}]}`); res.IsError {
		t.Fatalf("define failed: %s", res.Content)
	}
	run := macroExec(t, m, `{"action":"run","name":"esc-macro","args":["a\"b"]}`)
	if run.IsError {
		t.Fatalf("run failed: %s", run.Content)
	}
	if !strings.Contains(run.Content, `probe:a"b`) {
		t.Fatalf("escaped value must round-trip as a plain string: %s", run.Content)
	}
}
