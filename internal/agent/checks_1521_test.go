package agent

import "testing"

// #1521 case B pins: bare CJK substrings must NOT fire.
func Test1521CJKBareFormsNotFlagged(t *testing.T) {
	mkAgent := func() *Agent { return &Agent{ambiguityPoint: newAmbiguityPointState()} }
	for _, msg := range []string{
		"修复排序不稳定的 bug",
		"大概是上个月引入的",
		"最近的改动导致回归",
		"用最新的 API 重写这一段",
		"去重逻辑有问题，key 不对",
	} {
		if w := mkAgent().checkAmbiguityPoints(msg); len(w) > 0 {
			t.Errorf("bare CJK substring falsely flagged %q: %v", msg, w)
		}
	}
	for _, msg := range []string{"排序一下", "帮我排序", "改个名", "最新的几个", "大概几个", "随便挑一个"} {
		if w := mkAgent().checkAmbiguityPoints(msg); len(w) == 0 {
			t.Errorf("phrase shape should flag %q", msg)
		}
	}
}

// #1521 case C pin: long redirection messages must still classify.
func Test1521LongRedirectionDetected(t *testing.T) {
	long := "Actually, I meant the opposite - " + makeStr('x', 150)
	mid := makeStr('y', 120) + " the wrong approach might actually be right if we consider edge cases"
	if c := detectNegativeFeedback(mid); c != "" {
		t.Fatalf("mid-sentence actually in a long reflective message must NOT classify, got %q", c)
	}
	if c := detectNegativeFeedback(long); c != "redirection" {
		t.Fatalf("long redirection must fire regardless of length, got %q", c)
	}
}

// #1521 case D pin: a failed test run must not satisfy verification.
func Test1521FailedRunNotVerification(t *testing.T) {
	rs := &RunStats{
		CommandsRun: []string{"go test ./..."},
		Errors:      []string{"go test ./... : exit status 1 --- FAIL"},
	}
	if hasVerificationCommands(rs) {
		t.Fatal("failed test run must not count as verification")
	}
	rsOK := &RunStats{CommandsRun: []string{"go test ./..."}}
	if !hasVerificationCommands(rsOK) {
		t.Fatal("successful test run must count as verification")
	}
}

func makeStr(b byte, n int) string {
	s := make([]byte, n)
	for i := range s {
		s[i] = b
	}
	return string(s)
}
