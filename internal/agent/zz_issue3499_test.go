package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

// #3499: write/edit/multi_edit → run_command build-like matchers previously
// used bare substring Contains ("build", "tsc", "make ") on the whole command
// string, flagging zero-relation parallel batches as dependency violations.
// The fix matches command SEGMENT PREFIXES instead.

func issue3499Batch(t *testing.T, names []string, rawArgs []string) string {
	t.Helper()
	s := newCFDepState()
	args := make([]json.RawMessage, len(rawArgs))
	for i, a := range rawArgs {
		args[i] = json.RawMessage(a)
	}
	return s.recordBatch(names, args, 1)
}

func TestIssue3499_ZeroRelationBatchNotFlagged(t *testing.T) {
	cases := []struct {
		name string
		cmd  string
	}{
		{"log file named build", "cat logs/build.log"},
		{"docker log stream", "docker logs buildkit"},
		{"removing build dir", "rm -rf build/"},
		{"grep tsc", "grep tsc tsconfig.notes.md"},
		{"makefile word", "cat Makefile > /dev/null && echo done"},
	}
	for _, tc := range cases {
		warn := issue3499Batch(t,
			[]string{"write_file", "run_command"},
			[]string{`{"path":"docs/readme.md"}`, `{"command":"` + tc.cmd + `"}`},
		)
		if warn != "" {
			t.Errorf("%s: expected no warning for zero-relation batch, got: %s", tc.name, warn)
		}
	}
}

func TestIssue3499_RealBuildDependencyStillFlagged(t *testing.T) {
	cases := []struct {
		name string
		cmd  string
	}{
		{"go build", "go build ./..."},
		{"go test chained", "cd internal && go test ./agent/"},
		{"cargo test", "cargo test --all"},
		{"make target", "make build"},
		{"tsc bare", "tsc"},
		{"tsc with args", "tsc --noEmit"},
		{"pytest", "pytest -q"},
	}
	for _, tc := range cases {
		warn := issue3499Batch(t,
			[]string{"write_file", "run_command"},
			[]string{`{"path":"main.go"}`, `{"command":"` + tc.cmd + `"}`},
		)
		if warn == "" {
			t.Errorf("%s: expected dependency warning, got none", tc.name)
		}
		if !strings.Contains(warn, "write_file → run_command") {
			t.Errorf("%s: warning missing pair label: %s", tc.name, warn)
		}
	}
}

func TestIssue3499_EditorVariantsCovered(t *testing.T) {
	for _, producer := range []string{"edit_file", "multi_edit_file"} {
		warn := issue3499Batch(t,
			[]string{producer, "start_command"},
			[]string{`{"file_path":"a.go"}`, `{"command":"go vet ./..."}`},
		)
		if warn == "" {
			t.Errorf("%s + start_command go vet: expected warning", producer)
		}
	}
}

func TestIssue3499_CommandIsBuildLikeSegmentPrefix(t *testing.T) {
	tests := []struct {
		cmd  string
		want bool
	}{
		{"go build ./...", true},
		{"go test ./...", true},
		{"npm run build", true},
		{"cat logs/build.log", false},
		{"grep tsc", false},
		{"echo 'build failed'", false},
		{"go build", true},       // exact segment match
		{"gobuild ./...", false}, // no space boundary
		{"makefile", false},      // prefix without word boundary
		{"ls && make", true},     // second segment
		{"cat a.log; go test ./...", true},
		{"", false},
	}
	for _, tt := range tests {
		got := commandIsBuildLike(map[string]interface{}{"command": tt.cmd})
		if got != tt.want {
			t.Errorf("commandIsBuildLike(%q) = %v, want %v", tt.cmd, got, tt.want)
		}
	}
}

func TestIssue3499_MaxWarningsStillCapped(t *testing.T) {
	s := newCFDepState()
	names := []string{"write_file", "run_command"}
	raw := []string{`{"path":"a.go"}`, `{"command":"go build ./..."}`}
	w1 := s.recordBatch(names, toRaw(t, raw), 1)
	w2 := s.recordBatch(names, toRaw(t, raw), 2)
	w3 := s.recordBatch(names, toRaw(t, raw), 3)
	if w1 == "" || w2 == "" {
		t.Fatalf("expected first two batches to warn")
	}
	if w3 != "" {
		t.Fatalf("expected third batch to be silent (max 2 warnings), got: %s", w3)
	}
}

func toRaw(t *testing.T, ss []string) []json.RawMessage {
	t.Helper()
	out := make([]json.RawMessage, len(ss))
	for i, s := range ss {
		out[i] = json.RawMessage(s)
	}
	return out
}
