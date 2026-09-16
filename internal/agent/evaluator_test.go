package agent

import (
	"context"
	"testing"
)

func TestParseAdversarialVerdict(t *testing.T) {
	tests := []struct {
		name         string
		text         string
		wantVerdict  string
		wantFindings string
	}{
		{
			name:        "pass",
			text:        "VERDICT: PASS\nThe change fully satisfies the task.",
			wantVerdict: "PASS",
		},
		{
			name:         "fail with findings",
			text:         "VERDICT: FAIL\n- evaluator.go:40: off-by-one in loop bound\n- missing error propagation in collectAdversarialDiff",
			wantVerdict:  "FAIL",
			wantFindings: "- evaluator.go:40: off-by-one in loop bound\n- missing error propagation in collectAdversarialDiff",
		},
		{
			name:         "lowercase verdict counts",
			text:         "verdict: fail\n- broken integration",
			wantVerdict:  "FAIL",
			wantFindings: "- broken integration",
		},
		{
			name:         "fail with preamble before verdict",
			text:         "Analysis:\nlooks suspicious.\n\nVERDICT: FAIL\n- finding A",
			wantVerdict:  "FAIL",
			wantFindings: "- finding A",
		},
		{
			name:        "malformed no verdict treated as pass",
			text:        "I could not decide. The code seems fine.",
			wantVerdict: "PASS",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			verdict, findings := parseAdversarialVerdict(tt.text)
			if verdict != tt.wantVerdict {
				t.Fatalf("verdict = %q, want %q", verdict, tt.wantVerdict)
			}
			if findings != tt.wantFindings {
				t.Fatalf("findings = %q, want %q", findings, tt.wantFindings)
			}
		})
	}
}

func TestAdversarialReviewGateDisabledByDefault(t *testing.T) {
	a := &Agent{}
	if a.AdversarialReviewEnabled() {
		t.Fatal("adversarial review must be default-off")
	}
	// Disabled gate is a silent no-op even when code changed.
	rs := &RunStats{FilesEdited: []string{"x.go"}}
	if msg := a.checkAdversarialReviewGate(context.Background(), rs, "task"); msg != "" {
		t.Fatalf("disabled gate must not inject, got %q", msg)
	}
}

func TestAdversarialReviewGateSkipsWithoutChanges(t *testing.T) {
	a := &Agent{}
	a.SetAdversarialReview(true)
	if !a.AdversarialReviewEnabled() {
		t.Fatal("SetAdversarialReview(true) did not enable")
	}
	// No changed files -> skip without any provider call (provider is nil here,
	// so reaching runAdversarialEvaluator would not crash but the skip path is
	// exercised before that).
	if msg := a.checkAdversarialReviewGate(context.Background(), &RunStats{}, "task"); msg != "" {
		t.Fatalf("no-change gate must not inject, got %q", msg)
	}
}
