package debug

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRotationKeepsAtMostThreeFiles(t *testing.T) {
	basePath := filepath.Join(t.TempDir(), "ggcode-debug.log")
	resetForTest(t, basePath, 128, 3, 64)

	for i := 0; i < 200; i++ {
		Logf(strings.Repeat("x", 32))
	}

	waitFor(t, time.Second, func() bool {
		_, err0 := os.Stat(basePath)
		_, err1 := os.Stat(basePath + ".1")
		_, err2 := os.Stat(basePath + ".2")
		_, err3 := os.Stat(basePath + ".3")
		return err0 == nil && err1 == nil && err2 == nil && os.IsNotExist(err3)
	})
}

func TestCloseRemovesAllLogs(t *testing.T) {
	basePath := filepath.Join(t.TempDir(), "ggcode-debug.log")
	resetForTest(t, basePath, 128, 3, 64)

	for i := 0; i < 200; i++ {
		Logf(strings.Repeat("cleanup", 8))
	}

	waitFor(t, time.Second, func() bool {
		_, err := os.Stat(basePath)
		return err == nil
	})

	Close()

	matches, err := filepath.Glob(basePath + "*")
	if err != nil {
		t.Fatalf("glob logs: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("expected all logs removed, got %v", matches)
	}
}

func resetForTest(t *testing.T, basePath string, size int64, files, buffer int) {
	t.Helper()
	Close()
	mu.Lock()
	logger = nil
	logSink = nil
	once = sync.Once{}
	logPath = basePath
	maxLogSize = size
	maxLogFiles = files
	asyncBufSize = buffer
	mu.Unlock()
	Init()
	t.Cleanup(func() {
		Close()
		mu.Lock()
		logger = nil
		logSink = nil
		once = sync.Once{}
		logPath = defaultLogPath
		maxLogSize = defaultMaxLogSize
		maxLogFiles = defaultMaxLogFiles
		asyncBufSize = defaultAsyncBufSize
		mu.Unlock()
	})
}

func waitFor(t *testing.T, timeout time.Duration, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met before timeout")
}
