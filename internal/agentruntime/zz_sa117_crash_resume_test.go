package agentruntime

import (
	"testing"

	"github.com/topcheer/ggcode/internal/agent"
)

// sa-117: crashResumeTask extracts the interrupted task prompt for the
// /continue-task confirm-to-resume flow. The corrupt-journal placeholder
// ("<journal corrupted: ...>", built by run_journal.go CheckCrashedRun)
// names an evidence file, not a task - it must never be re-dispatched as a
// user prompt.

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
