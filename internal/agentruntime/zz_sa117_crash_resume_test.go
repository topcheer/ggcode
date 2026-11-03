package agentruntime

import (
	"testing"

	"github.com/topcheer/ggcode/internal/agent"
)

func TestCrashResumeTaskExtraction(t *testing.T) {
	cases := []struct {
		name string
		info *agent.CrashRecoveryInfo
		want string
	}{
		{"nil info", nil, ""},
		{"empty prompt", &agent.CrashRecoveryInfo{SessionID: "s1"}, ""},
		{"real task", &agent.CrashRecoveryInfo{SessionID: "s1", UserPrompt: "refactor the parser"}, "refactor the parser"},
		{"corrupt placeholder", &agent.CrashRecoveryInfo{SessionID: "s1", UserPrompt: "<journal corrupted: run-abc.json>"}, ""},
		{"placeholder prefix lookalike", &agent.CrashRecoveryInfo{SessionID: "s1", UserPrompt: "<journal corrupted"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := crashResumeTask(tc.info); got != tc.want {
				t.Fatalf("crashResumeTask(%v) = %q, want %q", tc.info, got, tc.want)
			}
		})
	}
}
