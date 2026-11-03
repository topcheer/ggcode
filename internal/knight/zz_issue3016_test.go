package knight

import (
	"strings"
	"testing"
)

func isHex(s string) bool {
	for _, r := range s {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return len(s) > 0
}

func TestIssue3016_UnrelatedFailuresDoNotShareKey(t *testing.T) {
	timeout := failure{
		toolInp: `{"command":"go build ./internal/agent/"}`,
		errMsg:  "context deadline exceeded after 300s running go build",
	}
	pathErr := failure{
		toolInp: `{"command":"npm test -- spec/missing.spec.js"}`,
		errMsg:  "Cannot find module '/app/spec/missing.spec.js'",
	}
	a, b := buildFailureFixName(timeout), buildFailureFixName(pathErr)
	if a == b {
		t.Fatalf("#3016: unrelated failures share key %q - EvidenceCount would fake convergence", a)
	}
	if !strings.HasPrefix(a, "build-failure-recovery-") {
		t.Fatalf("timeout failure should keep the build keyword prefix, got %q", a)
	}
	if !strings.HasPrefix(b, "test-failure-recovery-") {
		t.Fatalf("npm failure should keep the test keyword prefix, got %q", b)
	}
}

func TestIssue3016_SameErrorConvergesAcrossSessions(t *testing.T) {

	s1 := failure{toolInp: `{"command":"go test ./..."}`, errMsg: "FAIL: TestFoo [recovered]\n    /ci/agent/foo_test.go:42: undefined: Bar"}
	s2 := failure{toolInp: `{"command":"go test ./..."}`, errMsg: "FAIL: TestFoo [recovered]\n    /home/runner/agent/foo_test.go:77: undefined: Bar"}
	n1, n2 := buildFailureFixName(s1), buildFailureFixName(s2)
	if n1 != n2 {
		t.Fatalf("#3016: same error class must converge across sessions, got %q vs %q", n1, n2)
	}
	if !strings.HasPrefix(n1, "test-failure-recovery-") {
		t.Fatalf("expected test keyword prefix, got %q", n1)
	}
}

func TestIssue3016_AggregationRespectsFingerprint(t *testing.T) {
	mk := func(errMsg string) map[string]*candidateAggregate {
		aggregated := map[string]*candidateAggregate{}
		c1 := SkillCandidate{Name: buildFailureFixName(failure{toolInp: `{"command":"go build ./..."}`, errMsg: errMsg})}
		aggregateCandidate(aggregated, c1, "session-1")
		return aggregated
	}

	agg := mk("undefined: HandleMsg")
	aggregateCandidate(agg, SkillCandidate{Name: buildFailureFixName(failure{toolInp: `{"command":"go build ./..."}`, errMsg: "undefined: HandleMsg"})}, "session-2")
	if len(agg) != 1 {
		t.Fatalf("same error must converge to one aggregate, got %d", len(agg))
	}

	agg = mk("undefined: HandleMsg")
	aggregateCandidate(agg, SkillCandidate{Name: buildFailureFixName(failure{toolInp: `{"command":"go build ./..."}`, errMsg: "link: duplicate symbol RunPipe"})}, "session-2")
	if len(agg) != 2 {
		t.Fatalf("#3016: unrelated failures must not merge (fake convergence), got %d aggregates", len(agg))
	}
}

func TestIssue3016_SignatureNormalizationStability(t *testing.T) {
	base := failure{toolInp: "x", errMsg: "open /var/folders/abc/config.yaml: no such file or directory"}
	variants := []failure{
		{toolInp: "x", errMsg: "open /home/user/work/config.yaml: no such file or directory"},
		{toolInp: "x", errMsg: "Open /tmp/x/config.yaml: no such file or directory"},
	}
	want := failureFingerprint(base)
	for i, v := range variants {
		if got := failureFingerprint(v); got != want {
			t.Fatalf("variant %d signature %q != base %q", i, got, want)
		}
	}

	if got := failureFingerprint(failure{toolInp: "x", errMsg: "permission denied: /etc/shadow"}); got == want {
		t.Fatal("different error class must produce a different signature")
	}
}
