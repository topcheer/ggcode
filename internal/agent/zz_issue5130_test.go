package agent

import "testing"

// #5130: three detector-rot faces.
// Face 1 - verifyHint: "go test -race" is a real test runner; the table
// carried a malformed dead key ("go test-race": false) that nobody types.
// Face 2 - user sentiment: 还是错误 ("still wrong") is genuine negative
// feedback, not a benign phrase; removing it from the CJK benign allowlist
// lets the existing 还是错 frustration pattern fire (EN parity: "still
// wrong" is tested as negative).
// Face 3 lives in internal/tool/edit_file.go (declaration-line matcher).

func TestIssue5130GoTestRaceIsVerifyCommand(t *testing.T) {
	if !isVerifyCommandSegment("go test -race ./internal/agent/") {
		t.Error("go test -race must be recognized as a verify command (#5130 Face 1)")
	}
	if isVerifyCommandSegment("go test-race ./x/") {
		t.Error("malformed dead key go test-race must no longer match anything")
	}
}

func TestIssue5130StillWrongCJKIsNegative(t *testing.T) {
	s := newUserSentimentState()

	fb := s.analyzeAndUpdate("还是错误")
	if fb.Level == 0 {
		t.Error("还是错误 must classify as negative feedback (#5130 Face 2), got level 0")
	}

	s2 := newUserSentimentState()
	fb2 := s2.analyzeAndUpdate("垃圾回收帮我看下")
	if fb2.Level != 0 {
		t.Errorf("genuine benign CJK phrase must stay benign, got level %d", fb2.Level)
	}

	// #4181 reconciliation: compound noun readings stay neutral.
	for _, compound := range []string{"还是错误处理更简单，直接 try-catch 吧", "看下还是错误码没变，日志里有线索"} {
		s3 := newUserSentimentState()
		if fb3 := s3.analyzeAndUpdate(compound); fb3.Level != 0 {
			t.Errorf("compound %q must stay neutral (#4181), got level %d", compound, fb3.Level)
		}
	}
}
