package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// r454: declarative invariants engine probes. The user-scope file lives
// in the real HOME (absent on test machines - safe); all probes drive the
// project scope via loadDir.

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
	// loadDir semantics match production (invariantEngineLazy): it already
	// points at the .ggcode directory itself.
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

// No file / no rules: engine inert (default state, zero behavior change).
func TestInvariants_NoFileInert(t *testing.T) {
	e := newTestEngine(t, "")
	if v := e.check("file_ops", argsJSON(t, map[string]any{"operations": []any{map[string]string{"action": "delete", "source": "/x/.env"}}})); v != nil {
		t.Fatalf("inert engine returned violation: %+v", v)
	}
}

// Delete of a .env path violates; mkdir and non-matching paths do not.
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
	// mkdir on the same path: op mismatch, no violation. (Source field:
	// mkdir's target is its source since #3254, matching the tool's
	// os.MkdirAll(source) semantics.)
	mkdir := argsJSON(t, map[string]any{"operations": []any{map[string]string{"action": "mkdir", "source": "/x/.env"}}})
	if v := e.check("file_ops", mkdir); v != nil {
		t.Errorf("mkdir tripped a delete rule: %+v", v)
	}
	// delete of a non-matching path: glob mismatch.
	delOther := argsJSON(t, map[string]any{"operations": []any{map[string]string{"action": "delete", "source": "/proj/main.go"}}})
	if v := e.check("file_ops", delOther); v != nil {
		t.Errorf("non-matching path tripped rule: %+v", v)
	}
}

// created_by_run: rule forbids deleting paths NOT created by this run.
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
	// Register the path as a run product, then the same call passes.
	e.recordProduct("/proj/not-mine.txt")
	if v := e.check("file_ops", delOther); v != nil {
		t.Errorf("delete of own product still flagged: %+v", v)
	}
}

// Tool selector: empty on_tools = all tools; trailing * prefix-match.
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

// block beats warn regardless of file order.
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

// Malformed project file degrades to no rules from that scope (no panic).
func TestInvariants_BadJSONDegrades(t *testing.T) {
	e := newTestEngine(t, `{not json`)
	v := e.check("file_ops", argsJSON(t, map[string]any{"operations": []any{map[string]string{"action": "delete", "source": "/x/.env"}}}))
	if v != nil {
		t.Fatalf("bad file must degrade to inert, got %+v", v)
	}
}

// Unknown mode normalized to warn (fail-open).
func TestInvariants_UnknownModeFailsOpen(t *testing.T) {
	e := newTestEngine(t, `{"invariants":[{"id":"u","op":"exec","mode":"explode"}]}`)
	v := e.check("run_command", argsJSON(t, map[string]string{"command": "ls"}))
	if v == nil || v.Inv.Mode != "warn" {
		t.Fatalf("unknown mode should normalize to warn: %+v", v)
	}
}

// Glob semantics: bare name matches basename; ** crosses dirs.
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

// Lazy engine: no working dir -> nil (agent stays inert).
func TestInvariants_LazyNilWithoutWorkingDir(t *testing.T) {
	a := &Agent{}
	if e := a.invariantEngineLazy(); e != nil {
		t.Errorf("expected nil engine without working dir, got %+v", e)
	}
}

func boolPtr(b bool) *bool { return &b }
