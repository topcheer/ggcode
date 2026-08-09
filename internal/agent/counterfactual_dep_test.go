package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

func mustRawJSON(t *testing.T, v map[string]interface{}) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func TestCFDep_WriteThenBuildSameBatch(t *testing.T) {
	s := newCFDepState()
	names := []string{"write_file", "run_command"}
	args := []json.RawMessage{
		mustRawJSON(t, map[string]interface{}{"path": "main.go"}),
		mustRawJSON(t, map[string]interface{}{"command": "go build ./..."}),
	}
	warn := s.recordBatch(names, args, 1)
	if warn == "" {
		t.Fatal("expected dependency warning for write_file + go build in same batch")
	}
	if !strings.Contains(warn, "write_file") || !strings.Contains(warn, "run_command") {
		t.Fatalf("warning should mention both tools, got: %s", warn)
	}
	if !strings.Contains(warn, "Counterfactual") {
		t.Fatalf("warning should mention counterfactual, got: %s", warn)
	}
}

func TestCFDep_EditThenTestSameBatch(t *testing.T) {
	s := newCFDepState()
	names := []string{"edit_file", "run_command"}
	args := []json.RawMessage{
		mustRawJSON(t, map[string]interface{}{"file_path": "foo.go"}),
		mustRawJSON(t, map[string]interface{}{"command": "go test ./..."}),
	}
	warn := s.recordBatch(names, args, 1)
	if warn == "" {
		t.Fatal("expected dependency warning for edit_file + go test in same batch")
	}
}

func TestCFDep_IndependentCallsNoWarn(t *testing.T) {
	s := newCFDepState()
	// Two independent read-only calls -- no dependency.
	names := []string{"read_file", "grep"}
	args := []json.RawMessage{
		mustRawJSON(t, map[string]interface{}{"path": "a.go"}),
		mustRawJSON(t, map[string]interface{}{"pattern": "TODO"}),
	}
	warn := s.recordBatch(names, args, 1)
	if warn != "" {
		t.Fatalf("expected no warning for independent calls, got: %s", warn)
	}
}

func TestCFDep_BuildCommandNotBuildLike(t *testing.T) {
	s := newCFDepState()
	// write_file + run_command(echo) -- echo is not build-like.
	names := []string{"write_file", "run_command"}
	args := []json.RawMessage{
		mustRawJSON(t, map[string]interface{}{"path": "main.go"}),
		mustRawJSON(t, map[string]interface{}{"command": "echo hello"}),
	}
	warn := s.recordBatch(names, args, 1)
	if warn != "" {
		t.Fatalf("expected no warning for write + echo, got: %s", warn)
	}
}

func TestCFDep_MkdirThenWriteSameBatch(t *testing.T) {
	s := newCFDepState()
	names := []string{"file_ops", "write_file"}
	args := []json.RawMessage{
		mustRawJSON(t, map[string]interface{}{
			"operations": []interface{}{
				map[string]interface{}{"action": "mkdir", "source": "newdir"},
			},
		}),
		mustRawJSON(t, map[string]interface{}{"path": "newdir/file.go"}),
	}
	warn := s.recordBatch(names, args, 1)
	if warn == "" {
		t.Fatal("expected dependency warning for mkdir + write_file under that dir")
	}
}

func TestCFDep_MkdirThenWriteUnrelatedPath(t *testing.T) {
	s := newCFDepState()
	names := []string{"file_ops", "write_file"}
	args := []json.RawMessage{
		mustRawJSON(t, map[string]interface{}{
			"operations": []interface{}{
				map[string]interface{}{"action": "mkdir", "source": "newdir"},
			},
		}),
		mustRawJSON(t, map[string]interface{}{"path": "other/file.go"}),
	}
	warn := s.recordBatch(names, args, 1)
	if warn != "" {
		t.Fatalf("expected no warning for mkdir(newdir) + write(other/file), got: %s", warn)
	}
}

func TestCFDep_CheckoutThenEdit(t *testing.T) {
	s := newCFDepState()
	names := []string{"run_command", "edit_file"}
	args := []json.RawMessage{
		mustRawJSON(t, map[string]interface{}{"command": "git checkout feature-branch"}),
		mustRawJSON(t, map[string]interface{}{"file_path": "main.go"}),
	}
	warn := s.recordBatch(names, args, 1)
	if warn == "" {
		t.Fatal("expected dependency warning for git checkout + edit_file")
	}
}

func TestCFDep_ModInitThenGet(t *testing.T) {
	s := newCFDepState()
	names := []string{"run_command", "run_command"}
	args := []json.RawMessage{
		mustRawJSON(t, map[string]interface{}{"command": "go mod init example.com/foo"}),
		mustRawJSON(t, map[string]interface{}{"command": "go get github.com/gin-gonic/gin"}),
	}
	warn := s.recordBatch(names, args, 1)
	if warn == "" {
		t.Fatal("expected dependency warning for go mod init + go get")
	}
}

func TestCFDep_MaxWarnings(t *testing.T) {
	s := newCFDepState()
	// First batch: should warn.
	names1 := []string{"write_file", "run_command"}
	args1 := []json.RawMessage{
		mustRawJSON(t, map[string]interface{}{"path": "a.go"}),
		mustRawJSON(t, map[string]interface{}{"command": "go build"}),
	}
	w1 := s.recordBatch(names1, args1, 1)
	if w1 == "" {
		t.Fatal("expected first warning")
	}

	// Second batch: should still warn (maxWarnings=2).
	w2 := s.recordBatch(names1, args1, 2)
	if w2 == "" {
		t.Fatal("expected second warning")
	}

	// Third batch: should NOT warn (max reached).
	w3 := s.recordBatch(names1, args1, 3)
	if w3 != "" {
		t.Fatalf("expected no more warnings after max, got: %s", w3)
	}
}

func TestCFDep_Reset(t *testing.T) {
	s := newCFDepState()
	names := []string{"write_file", "run_command"}
	args := []json.RawMessage{
		mustRawJSON(t, map[string]interface{}{"path": "a.go"}),
		mustRawJSON(t, map[string]interface{}{"command": "go build"}),
	}
	// Exhaust warnings.
	s.recordBatch(names, args, 1)
	s.recordBatch(names, args, 2)
	s.recordBatch(names, args, 3) // third, no warn

	// Reset should re-enable warnings.
	s.reset()
	w := s.recordBatch(names, args, 4)
	if w == "" {
		t.Fatal("expected warning after reset")
	}
}

func TestCFDep_MultiEditThenBuild(t *testing.T) {
	s := newCFDepState()
	names := []string{"multi_edit_file", "run_command"}
	args := []json.RawMessage{
		mustRawJSON(t, map[string]interface{}{"file_path": "foo.go"}),
		mustRawJSON(t, map[string]interface{}{"command": "go build"}),
	}
	warn := s.recordBatch(names, args, 1)
	if warn == "" {
		t.Fatal("expected dependency warning for multi_edit_file + go build")
	}
}

func TestCFDep_StartCommandVariant(t *testing.T) {
	s := newCFDepState()
	names := []string{"write_file", "start_command"}
	args := []json.RawMessage{
		mustRawJSON(t, map[string]interface{}{"path": "main.go"}),
		mustRawJSON(t, map[string]interface{}{"command": "go build ./..."}),
	}
	warn := s.recordBatch(names, args, 1)
	if warn == "" {
		t.Fatal("expected dependency warning for write_file + start_command(build)")
	}
}

func TestCFDep_RecordToolCallBatchNilAgent(t *testing.T) {
	// Should not panic with nil agent.
	var a *Agent
	warn := a.recordToolCallBatch(nil, 1)
	if warn != "" {
		t.Fatalf("expected empty warning for nil agent, got: %s", warn)
	}
}

func TestCommandIsBuildLike(t *testing.T) {
	tests := []struct {
		cmd  string
		want bool
	}{
		{"go build ./...", true},
		{"go test ./...", true},
		{"go vet ./...", true},
		{"make build", true},
		{"npm run build", true},
		{"cargo build", true},
		{"pytest", true},
		{"echo hello", false},
		{"ls -la", false},
		{"git status", false},
		{"", false},
	}
	for _, tt := range tests {
		got := commandIsBuildLike(map[string]interface{}{"command": tt.cmd})
		if got != tt.want {
			t.Errorf("commandIsBuildLike(%q) = %v, want %v", tt.cmd, got, tt.want)
		}
	}
}

func TestExtractFileOpsMkdirTarget(t *testing.T) {
	// With mkdir source.
	got := extractFileOpsMkdirTarget(map[string]interface{}{
		"operations": []interface{}{
			map[string]interface{}{"action": "mkdir", "source": "foo"},
		},
	})
	if got != "foo" {
		t.Fatalf("expected 'foo', got %q", got)
	}

	// No mkdir action.
	got = extractFileOpsMkdirTarget(map[string]interface{}{
		"operations": []interface{}{
			map[string]interface{}{"action": "delete", "source": "foo"},
		},
	})
	if got != "" {
		t.Fatalf("expected empty for non-mkdir, got %q", got)
	}

	// Nil args.
	got = extractFileOpsMkdirTarget(nil)
	if got != "" {
		t.Fatalf("expected empty for nil, got %q", got)
	}
}
