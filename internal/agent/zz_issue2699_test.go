package agent

import (
	"strings"
	"testing"
)

func TestIssue2699_CloseThenReturnInSelect(t *testing.T) {
	src := `package main

func fanIn(in <-chan int, out chan<- int, done <-chan struct{}) {
	for v := range in {
		select {
		case out <- v:
		case <-done:
			close(out)
			return
		}
	}
}
`
	warnings := checkChannelSafety("proc.go", "", src)
	for _, w := range warnings {
		if strings.Contains(w, "close") && strings.Contains(w, "loop") {
			t.Fatalf("false positive close-in-loop on close+return idiom: %s", w)
		}
	}
}

func TestIssue2699_CloseThenBreakDirectInLoop(t *testing.T) {
	src := `package main

func worker(in <-chan int, out chan int, done <-chan struct{}) {
	for v := range in {
		if v < 0 {
			close(out)
			break
		}
		out <- v
	}
}
`
	warnings := checkChannelSafety("proc.go", "", src)
	for _, w := range warnings {
		if strings.Contains(w, "close") && strings.Contains(w, "loop") {
			t.Fatalf("false positive close-in-loop on close+break idiom: %s", w)
		}
	}
}

func TestIssue2699_PlainStmtsThenReturn(t *testing.T) {
	src := `package main

import "log"

func worker(in <-chan int, out chan int) {
	for v := range in {
		if v < 0 {
			close(out)
			log.Println("closed")
			return
		}
		out <- v
	}
}
`
	warnings := checkChannelSafety("proc.go", "", src)
	for _, w := range warnings {
		if strings.Contains(w, "close") && strings.Contains(w, "loop") {
			t.Fatalf("false positive close-in-loop with cleanup between close and return: %s", w)
		}
	}
}

func TestIssue2699_BreakInsideSelectStillWarns(t *testing.T) {

	src := `package main

func worker(in <-chan int, out chan int) {
	for v := range in {
		select {
		case out <- v:
		default:
			close(out)
			break
		}
	}
}
`
	warnings := checkChannelSafety("proc.go", "", src)
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "close") && strings.Contains(w, "loop") {
			found = true
		}
	}
	if !found {
		t.Fatal("expected close-in-loop warning: break inside select does not exit the loop")
	}
}

func TestIssue2699_ContinueStillWarns(t *testing.T) {
	src := `package main

func worker(in <-chan int, out chan int) {
	for v := range in {
		if v < 0 {
			close(out)
			continue
		}
		out <- v
	}
}
`
	warnings := checkChannelSafety("proc.go", "", src)
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "close") && strings.Contains(w, "loop") {
			found = true
		}
	}
	if !found {
		t.Fatal("expected close-in-loop warning: continue keeps the loop alive")
	}
}

func TestIssue2699_ConditionalTerminatorStillWarns(t *testing.T) {

	src := `package main

func worker(in <-chan int, out chan int) {
	for v := range in {
		close(out)
		if v < 0 {
			return
		}
		out <- v
	}
}
`
	warnings := checkChannelSafety("proc.go", "", src)
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "close") && strings.Contains(w, "loop") {
			found = true
		}
	}
	if !found {
		t.Fatal("expected close-in-loop warning: conditional terminator does not exempt")
	}
}

func TestIssue2699_ReturnInFuncLitStillWarns(t *testing.T) {

	src := `package main

func worker(in <-chan int, out chan int) {
	for v := range in {
		fn := func() {
			close(out)
			return
		}
		fn()
		_ = v
	}
}
`
	warnings := checkChannelSafety("proc.go", "", src)
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "close") && strings.Contains(w, "loop") {
			found = true
		}
	}
	if !found {
		t.Fatal("expected close-in-loop warning: return in func literal does not exit the loop")
	}
}

func TestIssue2699_TruePositiveStillWarns(t *testing.T) {

	src := `package main

func worker(in <-chan int, out chan int) {
	for v := range in {
		if v < 0 {
			close(out)
		}
		out <- v
	}
}
`
	warnings := checkChannelSafety("proc.go", "", src)
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "close") && strings.Contains(w, "loop") {
			found = true
		}
	}
	if !found {
		t.Fatal("expected close-in-loop warning: unprotected close must still be flagged")
	}
}

func TestIssue2699_GoCloseThenReturn(t *testing.T) {

	src := `package main

func worker(in <-chan int, out chan int) {
	for v := range in {
		if v < 0 {
			go close(out)
			return
		}
		out <- v
	}
}
`
	warnings := checkChannelSafety("proc.go", "", src)
	for _, w := range warnings {
		if strings.Contains(w, "close") && strings.Contains(w, "loop") {
			t.Fatalf("false positive close-in-loop on go close + return: %s", w)
		}
	}
}
