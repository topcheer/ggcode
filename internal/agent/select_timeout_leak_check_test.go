package agent

import (
	"strings"
	"testing"
)

func TestCheckSelectTimerLeak_DetectsInForLoop(t *testing.T) {
	old := `package main

func worker(ch chan int) {
}
`
	new := `package main

import "time"

func worker(ch chan int) {
	for {
		select {
		case v := <-ch:
			_ = v
		case <-time.After(100 * time.Millisecond):
			return
		}
	}
}
`
	warnings := checkSelectTimerLeak("worker.go", old, new)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d: %v", len(warnings), warnings)
	}
	if !strings.Contains(warnings[0], "1 time.After timer leak") {
		t.Errorf("warning should mention time.After timer leak: %s", warnings[0])
	}
}

func TestCheckSelectTimerLeak_DetectsInRangeLoop(t *testing.T) {
	old := `package main

func process(items []int) {
}
`
	new := `package main

import "time"

func process(items []int) {
	for range items {
		select {
		case <-time.After(time.Second):
			return
		}
	}
}
`
	warnings := checkSelectTimerLeak("worker.go", old, new)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d: %v", len(warnings), warnings)
	}
}

func TestCheckSelectTimerLeak_TimeNewTimerIsSafe(t *testing.T) {
	old := `package main

func worker(ch chan int) {
}
`
	new := `package main

import "time"

func worker(ch chan int) {
	timer := time.NewTimer(100 * time.Millisecond)
	defer timer.Stop()
	for {
		timer.Reset(100 * time.Millisecond)
		select {
		case v := <-ch:
			_ = v
		case <-timer.C:
			return
		}
	}
}
`
	warnings := checkSelectTimerLeak("worker.go", old, new)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings for time.NewTimer pattern, got %d: %v", len(warnings), warnings)
	}
}

func TestCheckSelectTimerLeak_TimeAfterOutsideLoopIsSafe(t *testing.T) {
	// time.After in a select that is NOT inside a loop is fine -- the timer
	// fires once and is collected.
	old := `package main

func handler(ch chan int) {
}
`
	new := `package main

import "time"

func handler(ch chan int) {
	select {
	case v := <-ch:
		_ = v
	case <-time.After(5 * time.Second):
		return
	}
}
`
	warnings := checkSelectTimerLeak("handler.go", old, new)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings for non-loop select, got %d: %v", len(warnings), warnings)
	}
}

func TestCheckSelectTimerLeak_DeltaAware(t *testing.T) {
	// Old content already has the pattern; new content keeps it but adds no
	// new ones. Should NOT warn.
	old := `package main

import "time"

func worker(ch chan int) {
	for {
		select {
		case <-ch:
		case <-time.After(100 * time.Millisecond):
			return
		}
	}
}
`
	new := `package main

import "time"

func worker(ch chan int) {
	for {
		select {
		case <-ch:
		case <-time.After(100 * time.Millisecond):
			return
		}
	}
	_ = time.Now
}
`
	warnings := checkSelectTimerLeak("worker.go", old, new)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings (no new timer leaks), got %d: %v", len(warnings), warnings)
	}
}

func TestCheckSelectTimerLeak_MultipleNew(t *testing.T) {
	old := `package main

import "time"

func worker(ch1, ch2 chan int) {
}
`
	new := `package main

import "time"

func worker(ch1, ch2 chan int) {
	for {
		select {
		case <-ch1:
		case <-time.After(100 * time.Millisecond):
		}
		select {
		case <-ch2:
		case <-time.After(200 * time.Millisecond):
		}
	}
}
`
	warnings := checkSelectTimerLeak("worker.go", old, new)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d: %v", len(warnings), warnings)
	}
	if !strings.Contains(warnings[0], "2 time.After timer leaks") {
		t.Errorf("warning should mention 2 timer leaks: %s", warnings[0])
	}
}

func TestCheckSelectTimerLeak_NestedLoop(t *testing.T) {
	// time.After in a select inside a nested if inside a for loop should
	// still be detected.
	old := `package main

func worker(ch chan int, flag bool) {
}
`
	new := `package main

import "time"

func worker(ch chan int, flag bool) {
	for {
		if flag {
			select {
			case <-ch:
			case <-time.After(time.Second):
			}
		}
	}
}
`
	warnings := checkSelectTimerLeak("worker.go", old, new)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning for nested timer leak, got %d: %v", len(warnings), warnings)
	}
}

func TestCheckSelectTimerLeak_AssignWithTimeAfter(t *testing.T) {
	// Pattern: case v := <-time.After(d)
	old := `package main

func worker(ch chan int) {
}
`
	new := `package main

import "time"

func worker(ch chan int) {
	for {
		select {
		case <-ch:
		case t := <-time.After(100 * time.Millisecond):
			_ = t
		}
	}
}
`
	warnings := checkSelectTimerLeak("worker.go", old, new)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning for assign pattern, got %d: %v", len(warnings), warnings)
	}
}

func TestCheckSelectTimerLeak_NonGoFile(t *testing.T) {
	new := `for (;;) { select { case <-time.After(): } }`
	warnings := checkSelectTimerLeak("worker.js", "", new)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings for non-Go file, got %d: %v", len(warnings), warnings)
	}
}

func TestCheckSelectTimerLeak_EmptyContent(t *testing.T) {
	warnings := checkSelectTimerLeak("worker.go", "", "")
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings for empty content, got %d: %v", len(warnings), warnings)
	}
}

func TestCheckSelectTimerLeak_TestFileSkipped(t *testing.T) {
	new := `package main

import "time"

func worker(ch chan int) {
	for {
		select {
		case <-ch:
		case <-time.After(time.Second):
		}
	}
}
`
	warnings := checkSelectTimerLeak("worker_test.go", "", new)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings for test file, got %d: %v", len(warnings), warnings)
	}
}
