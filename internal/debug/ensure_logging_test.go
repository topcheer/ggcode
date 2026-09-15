package debug

import (
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// resetForTest restores the pre-Init package state (mirrors EnableForTest).
func resetForTest() {
	mu.Lock()
	once = sync.Once{}
	enabled = false
	mainSink = nil
	sinks = nil
	loggers = nil
	tagFilter = nil
	mu.Unlock()
}

func TestEnsureFileLoggingEnablesAndIsIdempotent(t *testing.T) {
	t.Cleanup(func() {
		Close()
		resetForTest()
		os.Unsetenv("GGCODE_DEBUG")
	})

	// Start from a disabled state.
	Close()
	resetForTest()
	os.Unsetenv("GGCODE_DEBUG")
	if Active() {
		t.Fatalf("precondition: logging should be off")
	}

	wasEnabled, path := EnsureFileLogging()
	if wasEnabled {
		t.Errorf("wasEnabled should be false when logging was off")
	}
	if !Active() {
		t.Fatalf("logging should be active after EnsureFileLogging")
	}
	if path == "" || !strings.Contains(path, "ggcode-debug") {
		t.Errorf("expected main log path, got %q", path)
	}
	if path != MainLogPath() {
		t.Errorf("EnsureFileLogging path %q != MainLogPath %q", path, MainLogPath())
	}

	// Idempotent: second call is a no-op reporting the enabled state.
	wasEnabled2, path2 := EnsureFileLogging()
	if !wasEnabled2 || path2 != path {
		t.Errorf("second call should be a no-op, got wasEnabled=%v path=%q", wasEnabled2, path2)
	}
}

// waitForLine polls the log file until substr shows up or the deadline
// passes — the async sink flushes on its own schedule.
func waitForLine(path, substr string) bool {
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(path); err == nil && strings.Contains(string(data), substr) {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

// TestEnsureFileLoggingConcurrentSerializeEnable pins #1539 case B: the
// check-reset-init sequence is serialized, so exactly one concurrent caller
// performs the enable, and the winner's early log lines survive the losers.
// Pre-fix, a loser's Close() cleanup glob deleted every log file the winner
// had already written, and multiple callers could both report enabling.
func TestEnsureFileLoggingConcurrentSerializeEnable(t *testing.T) {
	t.Cleanup(func() {
		Close()
		resetForTest()
		os.Unsetenv("GGCODE_DEBUG")
	})

	Close()
	resetForTest()
	os.Unsetenv("GGCODE_DEBUG")
	if Active() {
		t.Fatalf("precondition: logging should be off")
	}

	const n = 8
	results := make([]bool, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			results[i], _ = EnsureFileLogging()
		}(i)
	}
	close(start)
	wg.Wait()

	if !Active() {
		t.Fatalf("logging should be active after the concurrent burst")
	}
	enabledCount := 0
	for _, r := range results {
		if !r {
			enabledCount++
		}
	}
	if enabledCount != 1 {
		t.Errorf("exactly one caller should perform the enable, got %d", enabledCount)
	}

	// The winner's segment must survive subsequent (and concurrent) callers.
	path := MainLogPath()
	Log("agent", "survivor line after concurrent ensure")
	if !waitForLine(path, "survivor line after concurrent ensure") {
		t.Fatalf("line should reach the log file at %q", path)
	}
	wasEnabled, _ := EnsureFileLogging()
	if !wasEnabled {
		t.Errorf("follow-up call should see logging active")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("log file removed after concurrent EnsureFileLogging: %v", err)
	}
	if !strings.Contains(string(data), "survivor line after concurrent ensure") {
		t.Errorf("winner's log lines were destroyed by concurrent EnsureFileLogging (#1539 case B)")
	}
}
