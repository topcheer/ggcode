package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func newTestEngine(t *testing.T, rules string) *invariantEngine {
	t.Helper()
	dir := t.TempDir()
	if rules != "" {
		if err := os.MkdirAll(filepath.Join(dir, ".ggcode"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, ".ggcode", invariantFileName), []byte(rules), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	return &invariantEngine{loadDir: filepath.Join(dir, ".ggcode")}
}

const invNoDelete = `{"invariants":[
	{"id":"nd1","on_tools":["file_ops"],"op":"delete","path_glob":"**/.env*","mode":"block","message":"never delete env files"}
]}`

func argsJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestInvariants_NoFileInert(t *testing.T) {
	e := newTestEngine(t, "")
	if v := e.check("file_ops", argsJSON(t, map[string]any{"operations": []any{map[string]string{"action": "delete", "source": "/x/.env"}}})); v != nil {
		t.Fatalf("inert engine returned violation: %+v", v)
	}
}

func TestInvariants_DeleteEnvBlocked(t *testing.T) {
	e := newTestEngine(t, invNoDelete)
	delEnv := argsJSON(t, map[string]any{"operations": []any{map[string]string{"action": "delete", "source": "/proj/config/.env.production"}}})
	v := e.check("file_ops", delEnv)
	if v == nil || v.Inv.ID != "nd1" {
		t.Fatalf("expected nd1 violation, got %+v", v)
	}
	if v.Op != "delete" {
		t.Errorf("op = %q, want delete", v.Op)
	}

	mkdir := argsJSON(t, map[string]any{"operations": []any{map[string]string{"action": "mkdir", "source": "/x/.env"}}})
	if v := e.check("file_ops", mkdir); v != nil {
		t.Errorf("mkdir tripped a delete rule: %+v", v)
	}

	delOther := argsJSON(t, map[string]any{"operations": []any{map[string]string{"action": "delete", "source": "/proj/main.go"}}})
	if v := e.check("file_ops", delOther); v != nil {
		t.Errorf("non-matching path tripped rule: %+v", v)
	}
}

func TestInvariants_CreatedByRun(t *testing.T) {
	e := newTestEngine(t, `{"invariants":[
		{"id":"own","on_tools":["file_ops"],"op":"delete","created_by_run":true,"mode":"block","message":"only delete files this run created"}
	]}`)
	yes := boolPtr(true)
	_ = yes
	delOther := argsJSON(t, map[string]any{"operations": []any{map[string]string{"action": "delete", "source": "/proj/not-mine.txt"}}})
	if v := e.check("file_ops", delOther); v == nil {
		t.Fatal("delete of foreign file should violate created_by_run=true")
	}

	e.recordProduct("/proj/not-mine.txt")
	if v := e.check("file_ops", delOther); v != nil {
		t.Errorf("delete of own product still flagged: %+v", v)
	}
}

func TestInvariants_ToolSelectors(t *testing.T) {
	e := newTestEngine(t, `{"invariants":[
		{"id":"all","op":"exec","mode":"warn"},
		{"id":"writes","on_tools":["*_file","multi_*"],"op":"write","mode":"warn"}
	]}`)
	if v := e.check("run_command", argsJSON(t, map[string]string{"command": "ls"})); v == nil || v.Inv.ID != "all" {
		t.Fatalf("empty on_tools must match run_command: %+v", v)
	}
	if v := e.check("write_file", argsJSON(t, map[string]string{"path": "/a", "content": "x"})); v == nil || v.Inv.ID != "writes" {
		t.Fatalf("write_file must match writes rule: %+v", v)
	}
	if v := e.check("read_file", argsJSON(t, map[string]string{"path": "/a"})); v != nil {
		t.Errorf("read-only tool tripped exec/write rules: %+v", v)
	}
}

func TestInvariants_BlockPrecedence(t *testing.T) {
	e := newTestEngine(t, `{"invariants":[
		{"id":"w1","op":"exec","mode":"warn"},
		{"id":"b1","op":"exec","mode":"block"}
	]}`)
	v := e.check("run_command", argsJSON(t, map[string]string{"command": "ls"}))
	if v == nil || v.Inv.Mode != "block" {
		t.Fatalf("block must win: %+v", v)
	}
}

func TestInvariants_BadJSONDegrades(t *testing.T) {
	e := newTestEngine(t, `{not json`)
	v := e.check("file_ops", argsJSON(t, map[string]any{"operations": []any{map[string]string{"action": "delete", "source": "/x/.env"}}}))
	if v != nil {
		t.Fatalf("bad file must degrade to inert, got %+v", v)
	}
}

func TestInvariants_UnknownModeFailsOpen(t *testing.T) {
	e := newTestEngine(t, `{"invariants":[{"id":"u","op":"exec","mode":"explode"}]}`)
	v := e.check("run_command", argsJSON(t, map[string]string{"command": "ls"}))
	if v == nil || v.Inv.Mode != "warn" {
		t.Fatalf("unknown mode should normalize to warn: %+v", v)
	}
}

func TestInvariants_GlobSemantics(t *testing.T) {
	if !invariantGlobMatch(".env", "/a/b/.env") {
		t.Error("bare pattern must match basename")
	}
	if !invariantGlobMatch("**/testdata/**", "/proj/internal/tool/testdata/x.txt") {
		t.Error("** must cross directory levels")
	}
	if invariantGlobMatch("**/testdata/**", "/proj/internal/tool/x.txt") {
		t.Error("outside ** dir must not match")
	}
}

func TestInvariants_LazyNilWithoutWorkingDir(t *testing.T) {
	a := &Agent{}
	if e := a.invariantEngineLazy(); e != nil {
		t.Errorf("expected nil engine without working dir, got %+v", e)
	}
}

func boolPtr(b bool) *bool { return &b }
