package debug

import (
	"os"
	"strings"
	"sync"
	"testing"
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
