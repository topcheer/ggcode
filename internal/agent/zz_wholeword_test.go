package agent

import (
	"strings"
	"testing"
)

// #5514: CheckViolation matched anchor keywords with bare substring
// containment, so an anchor containing the word "Test" fired on unrelated
// tool arguments that merely mention "unit tests" (live repro observed by
// the coordinator seat: gh issue edit with prose about tests tripped a
// CI-discipline anchor). Whole-word matching must keep real hits firing.
func TestCheckViolationWholeWord(t *testing.T) {
	mk := func(text string) *AnchorStore {
		return &AnchorStore{Anchors: []ConstraintAnchor{{ID: "a", Text: text, Source: "explicit"}}}
	}

	// False positive from the live repro must stay quiet.
	s := mk("必须逐项识名**: `Test` conclusion 为 null/pending 禁 merge")
	if v := s.CheckViolation("run_command", `gh issue edit 5512 --body "unit tests pass and tests are green"`); v != "" {
		t.Fatalf("prose mentioning tests must not trip the anchor, got %q", v)
	}

	// A genuinely whole-word hit must still fire.
	s2 := mk("Test conclusion pending must never merge")
	if v := s2.CheckViolation("run_command", `gh pr merge 123`); !strings.Contains(v, "anchor-violation") {
		t.Fatalf("whole-word hit on bare target must fire, got %q", v)
	}

	// Word-stem inside a larger token (e.g. "testify", "attest") must not fire.
	s3 := mk("Test conclusion pending must never merge")
	if v := s3.CheckViolation("run_command", "go test ./internal/testify/"); v != "" {
		t.Fatalf("stem inside larger token must not fire, got %q", v)
	}
}
