package agent

import (
	"strings"
	"testing"
)

func TestZZLeaveAloneExtraction(t *testing.T) {
	cases := []struct {
		name string
		text string
		want bool
	}{

		{"file object", "leave config.yaml alone, please", true},
		{"go file object", "leave main.go alone, please", true},
		{"n-containing object", "leave nginx.conf alone, please", true},
		{"pronoun them", "leave them alone, please", true},
		{"pronoun that", "leave that alone, please", true},
		{"long path object", "leave internal/agent/agent.go alone, thanks", true},
		{"mixed case", "Leave Config.Yaml Alone, please", true},

		{"leaven is not leave", "leaven alone, please", false},

		{"period in gap accepted", "leave it. alone, please", true},
		{"no mention", "config.yaml is fine, please", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractRefusals(tc.text)
			if (len(got) > 0) != tc.want {
				t.Fatalf("extractRefusals(%q) = %v, want nonempty=%v", tc.text, got, tc.want)
			}
		})
	}
}

func TestZZLeaveAloneAnchorSurface(t *testing.T) {
	if got := refusalTargets("leave config.yaml alone, please"); !strings.Contains(strings.Join(got, ","), "config.yaml") {
		t.Fatalf("file object must anchor config.yaml, got %v", got)
	}
	if got := refusalTargets("leave main.go alone, please"); !strings.Contains(strings.Join(got, ","), "main.go") {
		t.Fatalf("go file object must anchor main.go, got %v", got)
	}
	if got := refusalTargets("leave them alone, please"); len(got) != 0 {
		t.Fatalf("pronoun object must anchor nothing, got %v", got)
	}
	if got := refusalTargets("leave that alone, please"); len(got) != 0 {
		t.Fatalf("'that' must anchor nothing, got %v", got)
	}
}

func TestZZLeaveAloneBlocksLikeDontTouch(t *testing.T) {
	l := newRefusalLedger(t.TempDir())
	l.record("leave config.yaml alone, please", "nl", refusalKindDirect)
	if msg := l.checkBlocked("edit_file", `{"file_path":"/repo/config.yaml"}`); msg == "" {
		t.Fatal("leave-alone refusal must block edit of the named file")
	}
}
