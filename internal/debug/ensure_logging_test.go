package debug

import (
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

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

	wasEnabled2, path2 := EnsureFileLogging()
	if !wasEnabled2 || path2 != path {
		t.Errorf("second call should be a no-op, got wasEnabled=%v path=%q", wasEnabled2, path2)
	}
}

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
