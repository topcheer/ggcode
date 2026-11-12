package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// zzDTJSON emits a JSON object so later steps can reference its fields
// via {{steps[N].key}} (the #P0 step-output data-flow feature).
type zzDTJSON struct{}

func (zzDTJSON) Name() string        { return "zz_dt_json" }
func (zzDTJSON) Description() string { return "json probe" }
func (zzDTJSON) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`)
}
func (zzDTJSON) Execute(_ context.Context, input json.RawMessage) (Result, error) {
	var args struct {
		Path string `json:"path"`
	}
	_ = json.Unmarshal(input, &args)
	return Result{Content: `{"path":"` + args.Path + `","hits":[{"file":"a.go"},{"file":"b.go"}]}`}, nil
}

func newStepRefTool(t *testing.T) *DefineTool {
	t.Helper()
	reg := NewRegistry()
	if err := reg.Register(zzDTJSON{}); err != nil {
		t.Fatal(err)
	}
	readOnlyToolNames["zz_dt_json"] = true
	t.Cleanup(func() { delete(readOnlyToolNames, "zz_dt_json") })
	return &DefineTool{WorkingDir: t.TempDir(), Registry: reg}
}

func TestStepOutputRefChainsData(t *testing.T) {
	dt := newStepRefTool(t)
	call := func(body string) Result {
		res, err := dt.Execute(context.Background(), json.RawMessage(body))
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	if res := call(`{"action":"define","name":"chain_ok","description":"chain","steps":[
		{"tool":"zz_dt_json","args":{"path":"x.go"}},
		{"tool":"zz_dt_json","args":{"path":"{{steps[0].path}}"}}
	]}`); res.IsError {
		t.Fatalf("define: %s", res.Content)
	}
	res := call(`{"action":"call","name":"chain_ok","params":{}}`)
	if res.IsError {
		t.Fatalf("call: %s", res.Content)
	}
	// step 2 echoed back the path captured from step 1's JSON output.
	if !strings.Contains(res.Content, `"path":"x.go","hits"`) {
		t.Fatalf("step ref did not inject JSON field: %q", res.Content)
	}
	if strings.Count(res.Content, "echo:") == 0 && !strings.Contains(res.Content, "x.go") {
		t.Fatalf("expected chained value in output: %q", res.Content)
	}
	if !strings.Contains(strings.SplitN(res.Content, "--- step 2", 2)[1], `{"path":"x.go"`) {
		t.Fatalf("step 2 must receive chained input: %q", res.Content)
	}
}

func TestStepOutputRefDefineRejectsForwardAndSelf(t *testing.T) {
	dt := newStepRefTool(t)
	call := func(body string) Result {
		res, _ := dt.Execute(context.Background(), json.RawMessage(body))
		return res
	}
	if res := call(`{"action":"define","name":"fwd","description":"x","steps":[
		{"tool":"zz_dt_json","args":{"path":"{{steps[1].path}}"}},
		{"tool":"zz_dt_json","args":{"path":"y.go"}}
	]}`); !res.IsError || !strings.Contains(res.Content, "must point to an earlier step") {
		t.Fatalf("forward ref must be rejected at define: %+v", res)
	}
	if res := call(`{"action":"define","name":"selfref","description":"x","steps":[
		{"tool":"zz_dt_json","args":{"path":"{{steps[0].path}}"}}
	]}`); !res.IsError || !strings.Contains(res.Content, "must point to an earlier step") {
		t.Fatalf("self ref must be rejected at define: %+v", res)
	}
}

func TestStepOutputRefCallRejectsUnresolvable(t *testing.T) {
	dt := newStepRefTool(t)
	call := func(body string) Result {
		res, _ := dt.Execute(context.Background(), json.RawMessage(body))
		return res
	}
	if res := call(`{"action":"define","name":"miss","description":"x","steps":[
		{"tool":"zz_dt_json","args":{"path":"a.go"}},
		{"tool":"zz_dt_json","args":{"path":"{{steps[0].nope}}"}}
	]}`); res.IsError {
		t.Fatalf("valid backward ref must define fine: %s", res.Content)
	}
	if res := call(`{"action":"call","name":"miss","params":{}}`); !res.IsError || !strings.Contains(res.Content, "unresolvable step reference") {
		t.Fatalf("unresolvable key must fail the call: %+v", res)
	}
}
