package agent

import (
	"strings"
	"testing"
)

func TestCheckChannelSafety_DoubleClose(t *testing.T) {
	src := `package main

func worker(ch chan int) {
	ch <- 1
	close(ch)
	close(ch)
}
`
	warnings := checkChannelSafety("worker.go", "", src)
	if len(warnings) == 0 {
		t.Fatal("expected double-close warning")
	}
	if !strings.Contains(strings.ToLower(warnings[0]), "double") {
		t.Errorf("unexpected warning: %s", warnings[0])
	}
}

func TestCheckChannelSafety_SingleClose(t *testing.T) {
	src := `package main

func worker(ch chan int) {
	ch <- 1
	close(ch)
}
`
	warnings := checkChannelSafety("worker.go", "", src)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings for single close, got: %v", warnings)
	}
}

func TestCheckChannelSafety_SendAfterClose(t *testing.T) {
	src := `package main

func worker(ch chan int) {
	close(ch)
	ch <- 2
}
`
	warnings := checkChannelSafety("worker.go", "", src)
	if len(warnings) == 0 {
		t.Fatal("expected send-after-close warning")
	}
	if !strings.Contains(warnings[0], "send") {
		t.Errorf("unexpected warning: %s", warnings[0])
	}
}

func TestCheckChannelSafety_SendBeforeClose(t *testing.T) {
	src := `package main

func worker(ch chan int) {
	ch <- 1
	ch <- 2
	close(ch)
}
`
	warnings := checkChannelSafety("worker.go", "", src)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings for send-before-close, got: %v", warnings)
	}
}

func TestCheckChannelSafety_CloseInLoop(t *testing.T) {
	src := `package main

func process(items []int) {
	ch := make(chan int, 1)
	for _, item := range items {
		ch <- item
		close(ch)
	}
}
`
	warnings := checkChannelSafety("proc.go", "", src)
	if len(warnings) == 0 {
		t.Fatal("expected close-in-loop warning")
	}
	if !strings.Contains(warnings[0], "loop") {
		t.Errorf("unexpected warning: %s", warnings[0])
	}
}

func TestCheckChannelSafety_NoCloseInLoop(t *testing.T) {
	src := `package main

func process(items []int) {
	for _, item := range items {
		ch := make(chan int, 1)
		ch <- item
		close(ch)
	}
}
`
	warnings := checkChannelSafety("proc.go", "", src)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings when channel created in loop, got: %v", warnings)
	}
}

func TestCheckChannelSafety_DeltaAware(t *testing.T) {
	oldSrc := `package main

func worker(ch chan int) {
	ch <- 1
	close(ch)
	close(ch)
}
`
	newSrc := `package main

func worker(ch chan int) {
	ch <- 1
	close(ch)
	close(ch)
	ch <- 2
}
`
	warnings := checkChannelSafety("worker.go", oldSrc, newSrc)
	// double-close already existed, but send-after-close is new
	foundSend := false
	for _, w := range warnings {
		if strings.Contains(w, "send") {
			foundSend = true
		}
	}
	if !foundSend {
		t.Errorf("expected send-after-close warning (delta), got: %v", warnings)
	}
}

func TestCheckChannelSafety_SelectorChannel(t *testing.T) {
	src := `package main

type Server struct {
	ch chan int
}

func (s *Server) stop() {
	close(s.ch)
	close(s.ch)
}
`
	warnings := checkChannelSafety("server.go", "", src)
	if len(warnings) == 0 {
		t.Fatal("expected double-close warning for selector channel")
	}
	if !strings.Contains(warnings[0], "s.ch") {
		t.Errorf("warning should mention s.ch: %s", warnings[0])
	}
}

func TestCheckChannelSafety_NonGoFile(t *testing.T) {
	warnings := checkChannelSafety("worker.py", "", "close(ch)")
	if len(warnings) != 0 {
		t.Errorf("expected no warnings for non-Go file")
	}
}

func TestCheckChannelSafety_TestFile(t *testing.T) {
	src := `package main

func worker(ch chan int) {
	close(ch)
	close(ch)
}
`
	warnings := checkChannelSafety("worker_test.go", "", src)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings for test file")
	}
}

func TestCheckChannelSafety_EmptyContent(t *testing.T) {
	warnings := checkChannelSafety("worker.go", "", "")
	if len(warnings) != 0 {
		t.Errorf("expected no warnings for empty content")
	}
}

func TestCheckChannelSafety_MultipleDoubleClose(t *testing.T) {
	src := `package main

func worker(ch1 chan int, ch2 chan int) {
	close(ch1)
	close(ch1)
	close(ch2)
	close(ch2)
}
`
	warnings := checkChannelSafety("worker.go", "", src)
	if len(warnings) < 2 {
		t.Fatalf("expected at least 2 warnings, got %d: %v", len(warnings), warnings)
	}
}

func TestCheckChannelSafety_SendThenCloseThenSend(t *testing.T) {
	src := `package main

func worker(ch chan int) {
	ch <- 1
	close(ch)
	ch <- 2
}
`
	warnings := checkChannelSafety("worker.go", "", src)
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "send") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected send-after-close warning, got: %v", warnings)
	}
}

func TestCheckChannelSafety_OnlySends(t *testing.T) {
	src := `package main

func worker(ch chan int) {
	ch <- 1
	ch <- 2
	ch <- 3
}
`
	warnings := checkChannelSafety("worker.go", "", src)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings for only sends, got: %v", warnings)
	}
}

func TestCheckChannelSafety_NoChannels(t *testing.T) {
	src := `package main

func worker(x int) {
	if x > 0 {
		return
	}
}
`
	warnings := checkChannelSafety("worker.go", "", src)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings, got: %v", warnings)
	}
}

func TestCheckChannelSafety_RangeLoopSend(t *testing.T) {
	src := `package main

func process(items []int) {
	ch := make(chan int)
	for i := range items {
		ch <- i
	}
	close(ch)
}
`
	warnings := checkChannelSafety("proc.go", "", src)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings for safe range+close, got: %v", warnings)
	}
}
