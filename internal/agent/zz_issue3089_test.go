package agent

import (
	"strings"
	"testing"
)

// #3089 V1: shell plumbing must not become the family key.
func TestIssue3089FamilyKeyStripsShellPrefixes(t *testing.T) {
	cases := []struct {
		name, cmd, want string
	}{
		{"bare", "go test ./internal/agent/", "go test"},
		{"cd-chain", "cd /Volumes/new/ggai/ggcode && go test ./...", "go test"},
		{"env-assign", "FOO=1 BAR=2 go test ./...", "go test"},
		{"env-wrapper", "env GOFLAGS=-p=1 go build ./...", "go build"},
		{"sudo", "sudo systemctl restart nginx", "systemctl restart"}, // sudo is a wrapper, real family is the payload
		{"set-e-script", "set -euo pipefail\nmake verify\nmake lint", "make verify"},
		{"cd-only-line", "cd /tmp\ngo vet ./...", "go vet"},
		{"comment-first", "# build all\nmake build", "make build"},
		{"one-token", "make", "make"},
	}
	for _, c := range cases {
		got := pivotCommandKey("run_command", `{"command":`+quoteJSON(c.cmd)+`}`)
		if got != c.want {
			t.Errorf("%s: key(%q) = %q, want %q", c.name, c.cmd, got, c.want)
		}
	}
	// other tools never participate
	if pivotCommandKey("edit_file", `{"file_path":"/x"}`) != "" {
		t.Error("edit_file must yield empty key")
	}
}

// #3089 V1 companion: cd / set segments are skipped even mid-chain.
func TestIssue3089FamilyKeyCdAndSetArePlumbing(t *testing.T) {
	got := pivotCommandKey("run_command", `{"command":"cd a && cd b && go build ./..."}`)
	if got != "go build" {
		t.Errorf("chained cd segments: got %q, want go build", got)
	}
}

// #3089 V2: environmental failures are neutral - neither counted as failure
// nor as same-family success.
func TestIssue3089EnvironmentalFailureNeutral(t *testing.T) {
	p := newPivotDecisionTracker()
	args := `{"command":"go test ./..."}`
	// 3 real failures would trip the first threshold...
	for i := 0; i < 3; i++ {
		p.recordToolCall("run_command", args, true, "exit status 1\n--- FAIL: TestX")
	}
	if w := p.checkAndWarn(); !strings.Contains(w, "go test") {
		t.Fatalf("real failures should warn, got %q", w)
	}
	// environmental failures alone never warn
	q := newPivotDecisionTracker()
	for i := 0; i < 10; i++ {
		q.recordToolCall("run_command", args, true, "signal: killed")
	}
	if w := q.checkAndWarn(); w != "" {
		t.Errorf("OOM kills must be neutral, got %q", w)
	}
	// mixed: 2 real + 5 environmental + 1 real = only 3 counted
	r := newPivotDecisionTracker()
	r.recordToolCall("run_command", args, true, "exit status 1")
	r.recordToolCall("run_command", args, true, "exit status 1")
	for i := 0; i < 5; i++ {
		r.recordToolCall("run_command", args, true, "context deadline exceeded")
	}
	r.recordToolCall("run_command", args, true, "exit status 1")
	if r.fails["go test"] != 3 {
		t.Errorf("counted %d, want 3 (environmental excluded)", r.fails["go test"])
	}
}

func quoteJSON(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString("\\\"")
		case '\\':
			b.WriteString("\\\\")
		case '\n':
			b.WriteString("\\n")
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
