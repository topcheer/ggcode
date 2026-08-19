package agent

import (
	"encoding/json"
	"testing"
)

func issue748Input(t *testing.T, cmd string) string {
	t.Helper()
	b, err := json.Marshal(map[string]string{"command": cmd})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

// Regression guard for #748: commandIsVerification must recognize the three
// real-world shapes the old prefix-only match missed: cd-prefixed, env-prefixed,
// and #-comment-prefixed (the run_command schema's own convention).
func TestCommandIsVerification_PositionAware(t *testing.T) {
	cases := []struct {
		name string
		cmd  string
		want bool
	}{
		{"cd && go test", "cd internal/agent && go test ./...", true},
		{"env-prefixed make verify", "GOFLAGS=\"-p=1\" GOMEMLIMIT=2GiB make verify-ci", true},
		{"# comment then go test", "# Run tests\ngo test -tags goolm ./internal/agent/", true},
		{"bash verify script", "bash scripts/run-tests.sh", false}, // script body unknown; conservative
		{"plain go test", "go test ./...", true},
		{"non-verify cd && ls", "cd /tmp && ls", false},
		{"non-verify echo", "echo done", false},
		{"empty", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := commandIsVerification(issue748Input(t, tc.cmd)); got != tc.want {
				t.Errorf("commandIsVerification(%q) = %v, want %v", tc.cmd, got, tc.want)
			}
		})
	}
}
