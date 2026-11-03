package agent

import "testing"

func TestIssue2648_MutexClosePathsNotFlagged(t *testing.T) {

	src1 := `package p
func send(ch chan int) error {
	if err := doSomething(); err != nil {
		close(ch)
		return err
	}
	ch <- 1
	close(ch)
	return nil
}
func doSomething() error { return nil }
`
	if got := collectChannelSafetyIssues(src1); len(got) != 0 {
		t.Errorf("early-return mutual exclusion: expected 0 issues, got %v", got)
	}

	src2 := `package p
func fin(ch chan int, failed bool) {
	if failed {
		close(ch)
	} else {
		ch <- 1
		close(ch)
	}
}
`
	if got := collectChannelSafetyIssues(src2); len(got) != 0 {
		t.Errorf("if/else sibling exclusion: expected 0 issues, got %v", got)
	}
}

func TestIssue2648_TrueDoubleCloseStillFlagged(t *testing.T) {
	src := `package p
func bad(ch chan int) {
	close(ch)
	close(ch)
}
`
	if got := collectChannelSafetyIssues(src); got["bad|ch|double-close"] != 1 {
		t.Errorf("true double-close not detected: %v", got)
	}
}
