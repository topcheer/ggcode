package agent

import (
	"strings"
	"testing"
)

// zz_issue_insert_class: pin the InterruptionInsert composite-signal contract
// (P0-1, delegation steering). Miss-to-Addition is REQUIRED for short or
// single-sentence guidance; hit only on long multi-sentence task briefs.

// long enough (>=120 runes) AND multi-sentence => Insert.
func TestClassifyInsertedTaskHit(t *testing.T) {
	en := "Please also review the billing module for double-charge bugs. The finance team reported duplicated invoices on renewals. Reproduce with the fixture in testdata/billing, then propose a minimal fix with tests."
	if got := classifyInterruption(en); got != InterruptionInsert {
		t.Errorf("en long multi-sentence brief: got %s, want %s", got, InterruptionInsert)
	}
	cn := "另外帮我审查一下 internal/billing 模块的重复扣款问题，财务团队反馈续费订单偶尔出现两次扣款。请在 testdata/billing 目录用现有 fixture 复现该场景。定位根因后给出最小修复方案，并附带回归测试。注意不要改动其他模块的公共接口，完成后简要汇报结论。"
	if got := classifyInterruption(cn); got != InterruptionInsert {
		t.Errorf("cn long multi-sentence brief: got %s, want %s", got, InterruptionInsert)
	}
}

// short OR single-sentence => stays Addition (never misfires on clarifications).
func TestClassifyInsertedTaskMissConservative(t *testing.T) {
	cases := []struct{ name, text string }{
		{"short follow-up", "also add a README while you are in there"},
		{"single long sentence", strings.Repeat("please also handle the retry backoff in the same client path ", 3)},
		{"two sentences but short", "Fix the typo. Also update the doc."},
		{"empty", ""},
	}
	for _, c := range cases {
		if got := classifyInterruption(c.text); got != InterruptionAddition {
			t.Errorf("%s: got %s, want %s", c.name, got, InterruptionAddition)
		}
	}
}

// retraction and revision still take precedence over Insert.
func TestClassifyInsertPrecedence(t *testing.T) {
	long := "Please switch to plan B entirely and drop the old approach. It must also cover the retry path with tests."
	if got := classifyInterruption(long); got != InterruptionRevision {
		t.Errorf("revision marker must win over insert signal: got %s, want %s", got, InterruptionRevision)
	}
	if got := classifyInterruption("stop"); got != InterruptionRetractStop {
		t.Errorf("bare stop must stay retraction: got %s", got)
	}
}

// preamble must be advisory (consider), mention spawn_agent, suspended main
// thread, and resume - and must NOT contain revision-style supersession.
func TestInsertPreambleContract(t *testing.T) {
	p := interruptionPreamble(InterruptionInsert)
	for _, want := range []string{"SEPARATE task", "consider delegating", "spawn_agent", "suspended main task", "resume the suspended main thread"} {
		if !strings.Contains(p, want) {
			t.Errorf("insert preamble missing %q", want)
		}
	}
	if strings.Contains(p, "superseded") {
		t.Errorf("insert preamble must not use revision supersession language")
	}
}
