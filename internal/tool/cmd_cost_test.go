package tool

import (
	"strings"
	"sync"
	"testing"
	"time"
)

// resetCommandCosts isolates each test from the package-level map.
func resetCommandCosts(t *testing.T) {
	t.Helper()
	saved := make(map[string]any)
	commandCosts.Range(func(k, v any) bool {
		saved[k.(string)] = v
		commandCosts.Delete(k)
		return true
	})
	t.Cleanup(func() {
		commandCosts.Range(func(k, _ any) bool {
			commandCosts.Delete(k)
			return true
		})
		for k, v := range saved {
			commandCosts.Store(k, v)
		}
	})
}

func TestCommandCostHintRequiresHistoryAndThreshold(t *testing.T) {
	resetCommandCosts(t)

	// Thin history (1 run) never advises.
	recordCommandCost("go test ./...", 5*time.Minute)
	if h := commandCostHint("go test ./..."); h != "" {
		t.Fatalf("single run must not advise, got %q", h)
	}

	// Cheap commands never advise regardless of run count.
	for i := 0; i < 5; i++ {
		recordCommandCost("ls -la", 10*time.Millisecond)
	}
	if h := commandCostHint("ls -la"); h != "" {
		t.Fatalf("cheap command must not advise, got %q", h)
	}

	// Slow command with enough history advises.
	for i := 0; i < 3; i++ {
		recordCommandCost("make verify-ci", 3*time.Minute+20*time.Second)
	}
	h := commandCostHint("make verify-ci")
	if h == "" {
		t.Fatal("slow command with 3 runs must advise")
	}
	if !strings.Contains(h, "cost hint") || !strings.Contains(h, "narrower") {
		t.Errorf("hint must be advisory and suggest narrower scope: %q", h)
	}
}

func TestCommandCostKeyNormalization(t *testing.T) {
	resetCommandCosts(t)

	recordCommandCost("  go build ./...  ", 3*time.Minute)
	recordCommandCost("go build ./...", 3*time.Minute)
	// Both spellings collapse to one bucket: 2 runs, avg above threshold.
	if h := commandCostHint("go build ./..."); h == "" {
		t.Fatal("normalized spellings must share one history bucket")
	}
	// A different command must not inherit it.
	if h := commandCostHint("go build ./cmd/..."); h != "" {
		t.Fatalf("distinct command must have empty history, got %q", h)
	}
}

func TestCommandCostRingBounded(t *testing.T) {
	resetCommandCosts(t)

	st := &costStat{}
	for i := 0; i < costHistoryMax+10; i++ {
		st.add(time.Second)
	}
	if n, _ := st.snapshot(); n != costHistoryMax {
		t.Fatalf("ring must cap at %d, got %d", costHistoryMax, n)
	}
}

func TestCommandCostConcurrentAccess(t *testing.T) {
	resetCommandCosts(t)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				recordCommandCost("race-probe", 2*time.Minute)
				commandCostHint("race-probe")
			}
		}()
	}
	wg.Wait()
	if n, avg := (&costStat{}).snapshot(); n != 0 {
		_ = avg // placeholder branch, never taken
	}
	if h := commandCostHint("race-probe"); h == "" {
		t.Fatal("concurrent recordings must all land in the bucket")
	}
}

func TestFormatCommandCostIncludesObservedAndHint(t *testing.T) {
	resetCommandCosts(t)

	recordCommandCost("make verify-ci", 4*time.Minute)
	recordCommandCost("make verify-ci", 4*time.Minute)
	out := formatCommandCost("make verify-ci", 5*time.Minute)
	if !strings.Contains(out, "[cost] took 5m") {
		t.Errorf("must report observed duration: %q", out)
	}
	if !strings.Contains(out, "[cost hint]") {
		t.Errorf("must include history hint when warranted: %q", out)
	}

	// Fast command: no cost line at all (below costReportMinElapsed and no
	// history) - fast commands stay noise-free.
	resetCommandCosts(t)
	if out := formatCommandCost("echo hi", 5*time.Millisecond); out != "" {
		t.Errorf("fast command must produce no cost line: %q", out)
	}
}
