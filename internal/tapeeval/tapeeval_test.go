package tapeeval

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/topcheer/ggcode/internal/toolreplay"
)

func entry(tool, input string, isError bool) toolreplay.Entry {
	return toolreplay.Entry{
		ToolName: tool,
		Input:    json.RawMessage(input),
		Result:   toolreplay.Result{Content: "ok", IsError: isError},
	}
}

func entries() []toolreplay.Entry {
	return []toolreplay.Entry{
		entry("read_file", `{"path":"a.go"}`, false),
		entry("grep", `{"pattern":"TODO"}`, false),
		entry("read_file", `{"path":"b.go"}`, false),
		entry("edit_file", `{"path":"b.go","old":"x","new":"y"}`, false),
		entry("run_command", `{"command":"go test"}`, false),
		entry("read_file", `{"path":"c.go"}`, true), // error result
	}
}

func TestEvaluateRequiredTools(t *testing.T) {
	es := entries()
	spec := &Spec{
		Name: "t",
		RequiredTools: []ToolAssertion{
			{Tool: "read_file"},                  // 3 calls, default min 1
			{Tool: "edit_file", MinCount: 2},     // only 1 -> fail
			{Tool: "grep", InputRegex: `"TODO"`}, // input matches
			{Tool: "grep", InputRegex: `"nomatch"`},
			{Tool: "grep", InputRegex: `[`}, // invalid regex -> fail
		},
	}
	rep := Evaluate(es, spec)
	if len(rep.Results) != 5 {
		t.Fatalf("want 5 results, got %d", len(rep.Results))
	}
	want := []bool{true, false, true, false, false}
	for i, w := range want {
		if rep.Results[i].Pass != w {
			t.Errorf("result %d (%s): pass = %v, want %v (detail: %s)",
				i, rep.Results[i].Check, rep.Results[i].Pass, w, rep.Results[i].Detail)
		}
	}
	// Duplicate same-tool assertions get disambiguated names.
	if rep.Results[0].Check == rep.Results[1].Check {
		t.Errorf("duplicate assertion names not disambiguated: %q", rep.Results[0].Check)
	}
}

func TestEvaluateForbiddenTools(t *testing.T) {
	es := entries()
	spec := &Spec{Name: "t", ForbiddenTools: []string{"run_command", "browser"}}
	rep := Evaluate(es, spec)
	if rep.Pass {
		t.Fatal("want FAIL when a forbidden tool was called")
	}
	if rep.Results[0].Pass {
		t.Errorf("run_command was called; check should fail, detail: %s", rep.Results[0].Detail)
	}
	if !rep.Results[1].Pass {
		t.Errorf("browser was not called; check should pass, detail: %s", rep.Results[1].Detail)
	}
}

func TestEvaluateToolOrder(t *testing.T) {
	es := entries()
	passSpec := &Spec{Name: "t", ToolOrder: []string{"read_file", "edit_file", "run_command"}}
	rep := Evaluate(es, passSpec)
	if !rep.Results[0].Pass {
		t.Errorf("subsequence read->edit->run exists; want pass, got: %s", rep.Results[0].Detail)
	}
	failSpec := &Spec{Name: "t", ToolOrder: []string{"edit_file", "grep"}}
	rep2 := Evaluate(es, failSpec)
	if rep2.Results[0].Pass {
		t.Errorf("grep precedes edit_file; want fail, got: %s", rep2.Results[0].Detail)
	}
	empty := &Spec{Name: "t"}
	rep3 := Evaluate(es, empty)
	if !rep3.Pass {
		t.Errorf("empty spec must pass; results: %+v", rep3.Results)
	}
}

func TestEvaluateBudgets(t *testing.T) {
	es := entries()
	spec := &Spec{Name: "t", MaxToolCalls: 5, MaxErrorResults: 1}
	rep := Evaluate(es, spec)
	if rep.Pass {
		t.Fatalf("6 calls > budget 5; want FAIL, results: %+v", rep.Results)
	}
	if rep.Metrics.ToolCalls != 6 || rep.Metrics.ErrorCalls != 1 || rep.Metrics.DistinctTools != 4 {
		t.Errorf("metrics = %+v", rep.Metrics)
	}
	spec2 := &Spec{Name: "t", MaxToolCalls: 6, MaxErrorResults: 1}
	if !Evaluate(es, spec2).Pass {
		t.Fatal("budgets met; want PASS")
	}
}

func TestEvaluateErrFieldCountsAsError(t *testing.T) {
	e := entry("run_command", `{}`, false)
	e.Err = "exit status 1"
	rep := Evaluate([]toolreplay.Entry{e}, &Spec{Name: "t", MaxErrorResults: 0})
	// MaxErrorResults ignored when 0 -> pass, but metric still counts it.
	if rep.Metrics.ErrorCalls != 1 {
		t.Errorf("Err field not counted: %+v", rep.Metrics)
	}
}

func TestLoadSpec(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good.json")
	if err := os.WriteFile(good, []byte(`{"name":"n","required_tools":[{"tool":"read_file","min_count":2}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	spec, err := LoadSpec(good)
	if err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}
	if spec.Version != SpecVersion || len(spec.RequiredTools) != 1 || spec.RequiredTools[0].MinCount != 2 {
		t.Errorf("spec = %+v", spec)
	}

	// Unknown field must be rejected (typo protection).
	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte(`{"name":"n","require_tool":"x"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSpec(bad); err == nil {
		t.Error("unknown field accepted")
	}

	// Missing name must be rejected.
	nameless := filepath.Join(dir, "nameless.json")
	if err := os.WriteFile(nameless, []byte(`{"max_tool_calls":3}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSpec(nameless); err == nil {
		t.Error("missing name accepted")
	}

	// Bad version must be rejected.
	old := filepath.Join(dir, "old.json")
	if err := os.WriteFile(old, []byte(`{"version":99,"name":"n"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSpec(old); err == nil {
		t.Error("unsupported version accepted")
	}
}

func TestScaffoldPassesOnOwnTape(t *testing.T) {
	es := entries()
	spec := Scaffold(es, "scaffolded")
	rep := Evaluate(es, spec)
	if !rep.Pass {
		t.Fatalf("scaffold must pass on its own tape; results: %+v", rep.Results)
	}
	if len(spec.RequiredTools) != rep.Metrics.DistinctTools {
		t.Errorf("want %d required tools, got %d", rep.Metrics.DistinctTools, len(spec.RequiredTools))
	}
	if spec.MaxToolCalls != len(es) || spec.MaxErrorResults != 1 {
		t.Errorf("budgets = %d/%d, want %d/1", spec.MaxToolCalls, spec.MaxErrorResults, len(es))
	}
	// The scaffold must actually catch degradation: replay a tape that uses
	// one fewer read_file call than recorded.
	shrunk := es[:len(es)-1]
	if Evaluate(shrunk, spec).Pass {
		t.Error("scaffold must fail when the trajectory does less work than recorded")
	}
}

func TestFormatReport(t *testing.T) {
	es := entries()
	spec := &Spec{Name: "demo", ForbiddenTools: []string{"run_command"}}
	out := FormatReport(Evaluate(es, spec), "/tmp/x.tape.json", "/tmp/x.eval.json")
	for _, want := range []string{"demo", "FAIL", "forbidden_tools[run_command]", "6 tool calls", "0/1 checks passed"} {
		if !contains(out, want) {
			t.Errorf("report missing %q:\n%s", want, out)
		}
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
