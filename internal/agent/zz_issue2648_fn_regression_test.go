package agent

import "testing"

// Regression guard for the false-negative found in the #2648 mutual-exclusion
// handling: clearing haveClose on a mutually-exclusive send let later
// NON-exclusive sends escape detection. Fix: skip only, keep the close marker.

// Exclusive send AFTER close must not clear the marker: the trailing
// non-exclusive send executes after close when cond==false (guaranteed
// "send on closed channel" panic) and must be flagged.
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

// Pure exclusive pair (close in then, send in else) still must not warn.
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

// Multiple exclusive sends between close and a non-exclusive send: every
// exclusive one is skipped, the final one still warns.
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
