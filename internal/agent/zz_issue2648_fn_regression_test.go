package agent

import "testing"

func TestSendAfterClose_ExclusiveThenNonExclusiveSend(t *testing.T) {
	src := `package p
func f(cond bool, ch chan int) {
	if cond {
		close(ch)
	} else {
		ch <- 1
	}
	ch <- 2
}
`
	if !hasSendAfterClose(runChannelSafetyOnCode(t, src)) {
		t.Fatal("trailing send is not exclusive with the close; must warn send-after-close")
	}
}

func TestSendAfterClose_PureExclusivePairStillSilent(t *testing.T) {
	src := `package p
func f(cond bool, ch chan int) {
	if cond {
		close(ch)
	} else {
		ch <- 1
	}
}
`
	if hasSendAfterClose(runChannelSafetyOnCode(t, src)) {
		t.Fatal("sibling if/else branches are mutually exclusive; must not warn")
	}
}

func TestSendAfterClose_MultipleExclusiveThenNonExclusive(t *testing.T) {
	src := `package p
func f(a, b bool, ch chan int) {
	if a {
		close(ch)
	} else {
		ch <- 1
	}
	if b {
		ch <- 2
	} else {
		ch <- 3
	}
	ch <- 4
}
`
	insts := runChannelSafetyOnCode(t, src)
	if !hasSendAfterClose(insts) {
		t.Fatal("final unconditional send after close must warn")
	}
	if len(insts) != 1 {
		t.Fatalf("expected exactly 1 send-after-close instance, got %d: %+v", len(insts), insts)
	}
}
