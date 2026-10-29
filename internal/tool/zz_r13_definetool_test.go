package tool

// zz_r13_definetool_test.go -- companion tests for define_tool (r13):
// parametrized agent-defined tools over the macro step DSL.
import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// zzDTProbe is a read-only probe tool that echoes its args, registered
// into the read-only whitelist for the duration of each test (same
// technique as zz_issue_sa148_test.go).
type zzDTProbe struct{}

func (zzDTProbe) Name() string        { return "zz_dt_probe" }
func (zzDTProbe) Description() string { return "echo probe" }
func (zzDTProbe) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"pattern":{"type":"string"}}}`)
}
func (zzDTProbe) Execute(_ context.Context, input json.RawMessage) (Result, error) {
	var args struct {
		Pattern string `json:"pattern"`
	}
	_ = json.Unmarshal(input, &args)
	return Result{Content: "echo:" + args.Pattern}, nil
}

func newDTTool(t *testing.T) (*DefineTool, *Registry) {
	t.Helper()
	reg := NewRegistry()
	if err := reg.Register(zzDTProbe{}); err != nil {
		t.Fatal(err)
	}
	readOnlyToolNames["zz_dt_probe"] = true
	t.Cleanup(func() { delete(readOnlyToolNames, "zz_dt_probe") })
	return &DefineTool{WorkingDir: t.TempDir(), Registry: reg}, reg
}

func dtCall(t *testing.T, dt *DefineTool, body string) Result {
	t.Helper()
	res, err := dt.Execute(context.Background(), json.RawMessage(body))
	if err != nil {
		t.Fatalf("execute %s: %v", body, err)
	}
	return res
}

func TestR13DefineAndCallParametrized(t *testing.T) {
	dt, _ := newDTTool(t)
	res := dtCall(t, dt, `{"action":"define","name":"find_usage","description":"find usages of a symbol","params_schema":{"type":"object","properties":{"symbol":{"type":"string"}},"required":["symbol"]},"steps":[{"tool":"zz_dt_probe","args":{"pattern":"{{symbol}}"}}]}`)
	if res.IsError {
		t.Fatalf("define failed: %s", res.Content)
	}
	res = dtCall(t, dt, `{"action":"call","name":"find_usage","params":{"symbol":"Foo"}}`)
	if res.IsError {
		t.Fatalf("call failed: %s", res.Content)
	}
	if !strings.Contains(res.Content, "echo:Foo") {
		t.Fatalf("param not substituted: %q", res.Content)
	}
}

func TestR13CallValidatesRequiredAndUnknownParams(t *testing.T) {
	dt, _ := newDTTool(t)
	if res := dtCall(t, dt, `{"action":"define","name":"req_a","params_schema":{"type":"object","properties":{"a":{"type":"string"}},"required":["a"]},"steps":[{"tool":"zz_dt_probe","args":{"pattern":"{{a}}"}}]}`); res.IsError {
		t.Fatalf("define: %s", res.Content)
	}
	if res := dtCall(t, dt, `{"action":"call","name":"req_a","params":{}}`); !res.IsError || !strings.Contains(res.Content, "missing required param") {
		t.Fatalf("missing-required not rejected: %+v", res)
	}
	if res := dtCall(t, dt, `{"action":"call","name":"req_a","params":{"a":"x","typo":"y"}}`); !res.IsError || !strings.Contains(res.Content, "unknown param") {
		t.Fatalf("unknown param not rejected: %+v", res)
	}
}

func TestR13DefineRejectsWriteTools(t *testing.T) {
	dt, _ := newDTTool(t)
	res := dtCall(t, dt, `{"action":"define","name":"evil","steps":[{"tool":"edit_file","args":{"file_path":"/tmp/x"}}]}`)
	if !res.IsError || !strings.Contains(res.Content, "read-only") {
		t.Fatalf("write tool must be rejected at define time: %+v", res)
	}
}

func TestR13UnknownPlaceholderRejectedAtCall(t *testing.T) {
	dt, _ := newDTTool(t)
	// No params_schema: call-time substitution must still fail loudly on
	// a placeholder with no matching param instead of grepping literally.
	if res := dtCall(t, dt, `{"action":"define","name":"probe_2","steps":[{"tool":"zz_dt_probe","args":{"pattern":"{{ghost}}"}}]}`); res.IsError {
		t.Fatalf("define: %s", res.Content)
	}
	res := dtCall(t, dt, `{"action":"call","name":"probe_2","params":{}}`)
	if !res.IsError || !strings.Contains(res.Content, "{{ghost}}") {
		t.Fatalf("unknown placeholder must error: %+v", res)
	}
}

func TestR13PersistenceAcrossInstances(t *testing.T) {
	dt1, reg := newDTTool(t)
	if res := dtCall(t, dt1, `{"action":"define","name":"persist_me","steps":[{"tool":"zz_dt_probe","args":{"pattern":"x"}}]}`); res.IsError {
		t.Fatalf("define: %s", res.Content)
	}
	dt2 := &DefineTool{WorkingDir: dt1.WorkingDir, Registry: reg}
	if res := dtCall(t, dt2, `{"action":"list"}`); !strings.Contains(res.Content, "persist_me") {
		t.Fatalf("definition not persisted: %q", res.Content)
	}
}

func TestR13NonStringParamSubstitution(t *testing.T) {
	dt, _ := newDTTool(t)
	if res := dtCall(t, dt, `{"action":"define","name":"probe_3","params_schema":{"type":"object","properties":{"n":{"type":"number"}}},"steps":[{"tool":"zz_dt_probe","args":{"pattern":"limit={{n}}"}}]}`); res.IsError {
		t.Fatalf("define: %s", res.Content)
	}
	res := dtCall(t, dt, `{"action":"call","name":"probe_3","params":{"n":42}}`)
	if res.IsError || !strings.Contains(res.Content, "echo:limit=42") {
		t.Fatalf("numeric param substitution: %+v", res)
	}
}
