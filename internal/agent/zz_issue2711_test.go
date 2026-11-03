package agent

import (
	"testing"

	"github.com/topcheer/ggcode/internal/tool"
)

func TestIssue2711ExecutedFailureWithBlockedWord(t *testing.T) {
	res := tool.Result{Content: "npm ERR! Request blocked by proxy\nnpm ERR! code ECONNREFUSED", IsError: true}
	outcome, enters := classifyEffectOutcome(res)
	if !enters {
		t.Fatalf("executed failure misclassified as never-executed (outcome=%v)", outcome)
	}
	if outcome != effectFailed {
		t.Fatalf("outcome = %v, want effectFailed", outcome)
	}
}

func TestIssue2711GateBlockStillNeverExecuted(t *testing.T) {
	res := tool.Result{Content: "Command blocked: rm -rf / (dangerous command)", IsError: true}
	if _, enters := classifyEffectOutcome(res); enters {
		t.Fatalf("gate block must stay never-executed")
	}
}
