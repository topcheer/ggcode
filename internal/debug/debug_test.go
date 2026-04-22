package debug

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestDisabledByDefault(t *testing.T) {
	os.Unsetenv(envKey)
	Close()
	mu.Lock()
	once = sync.Once{}
	mu.Unlock()
	defer func() {
		Close()
		mu.Lock()
		once = sync.Once{}
		mu.Unlock()
	}()

	Init()

	if Active() {
		t.Fatal("expected debug to be disabled when GGCODE_DEBUG is unset")
	}

	// Log should be a no-op
	Log("agent", "this should not crash")
	Logf("this should not crash either")
}

func TestEnabledWithEnvVar(t *testing.T) {
	os.Setenv(envKey, "1")
	defer os.Unsetenv(envKey)

	Close()
	mu.Lock()
	once = sync.Once{}
	mu.Unlock()
	defer func() {
		Close()
		mu.Lock()
		once = sync.Once{}
		mu.Unlock()
	}()

	Init()

	if !Active() {
		t.Fatal("expected debug to be enabled when GGCODE_DEBUG=1")
	}
}

func TestRotationKeepsAtMostThreeFiles(t *testing.T) {
	dir := t.TempDir()
	basePath := filepath.Join(dir, "ggcode-debug.log")
	sink, err := newAsyncFileSink(basePath, "", 128, 3, 64)
	if err != nil {
		t.Fatalf("create sink: %v", err)
	}
	defer sink.Close()

	for i := 0; i < 200; i++ {
		sink.Write([]byte(strings.Repeat("x", 32) + "\n"))
	}

	waitFor(t, 2*time.Second, func() bool {
		_, err0 := os.Stat(basePath)
		_, err1 := os.Stat(basePath + ".1")
		_, err2 := os.Stat(basePath + ".2")
		_, err3 := os.Stat(basePath + ".3")
		return err0 == nil && err1 == nil && err2 == nil && os.IsNotExist(err3)
	})
}

func TestCloseRemovesAllLogs(t *testing.T) {
	EnableForTest(t)

	mu.RLock()
	basePath := mainSink.basePath
	mu.RUnlock()

	for i := 0; i < 200; i++ {
		Logf("%s", strings.Repeat("cleanup", 8))
	}

	waitFor(t, 2*time.Second, func() bool {
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

func TestCategoryLogFiles(t *testing.T) {
	EnableForTest(t)

	pid := os.Getpid()

	Log("agent", "agent message")
	Log("openai", "openai message")
	Log("knight", "knight message")
	Log("unknown_tag", "unknown tag message")

	time.Sleep(200 * time.Millisecond) // wait for async writes

	// Agent category file should exist
	agentPath := filepath.Join(defaultLogDir, fmt.Sprintf("ggcode-agent-%d.log", pid))
	if _, err := os.Stat(agentPath); err != nil {
		t.Errorf("expected agent log file %s to exist: %v", agentPath, err)
	}

	// Provider category file should exist
	providerPath := filepath.Join(defaultLogDir, fmt.Sprintf("ggcode-provider-%d.log", pid))
	if _, err := os.Stat(providerPath); err != nil {
		t.Errorf("expected provider log file %s to exist: %v", providerPath, err)
	}

	// Knight category file should exist
	knightPath := filepath.Join(defaultLogDir, fmt.Sprintf("ggcode-knight-%d.log", pid))
	if _, err := os.Stat(knightPath); err != nil {
		t.Errorf("expected knight log file %s to exist: %v", knightPath, err)
	}

	// Main file should contain all messages
	mainPath := filepath.Join(defaultLogDir, fmt.Sprintf("ggcode-debug-%d.log", pid))
	data, err := os.ReadFile(mainPath)
	if err != nil {
		t.Fatalf("read main log: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "[agent]") {
		t.Error("main log should contain [agent] messages")
	}
	if !strings.Contains(content, "[openai]") {
		t.Error("main log should contain [openai] messages")
	}
	if !strings.Contains(content, "[knight]") {
		t.Error("main log should contain [knight] messages")
	}
	if !strings.Contains(content, "[unknown_tag]") {
		t.Error("main log should contain [unknown_tag] messages")
	}
}

func TestTagFilter(t *testing.T) {
	os.Setenv(envKey, "agent")
	defer os.Unsetenv(envKey)

	Close()
	mu.Lock()
	once = sync.Once{}
	mu.Unlock()

	Init()
	defer func() {
		Close()
		mu.Lock()
		once = sync.Once{}
		mu.Unlock()
	}()

	pid := os.Getpid()

	Log("agent", "agent message - should appear")
	Log("openai", "openai message - should NOT appear")
	Log("unknown", "unknown message - should NOT appear")

	time.Sleep(200 * time.Millisecond)

	// Agent file should exist
	agentPath := filepath.Join(defaultLogDir, fmt.Sprintf("ggcode-agent-%d.log", pid))
	if _, err := os.Stat(agentPath); err != nil {
		t.Errorf("expected agent log file to exist: %v", err)
	}

	// Provider file should NOT exist (filtered out)
	providerPath := filepath.Join(defaultLogDir, fmt.Sprintf("ggcode-provider-%d.log", pid))
	if _, err := os.Stat(providerPath); !os.IsNotExist(err) {
		t.Errorf("expected provider log file to NOT exist when filtered, but it does")
	}
}

func TestResolveLogPathsUsesPerProcessFileForDefaultPath(t *testing.T) {
	basePath, compatPath := resolveLogPaths("/tmp/ggcode-debug.log", 4321)
	if compatPath != "/tmp/ggcode-debug.log" {
		t.Fatalf("expected compat path %q, got %q", "/tmp/ggcode-debug.log", compatPath)
	}
	if basePath != filepath.Join(defaultLogDir, "ggcode-debug-4321.log") {
		t.Fatalf("unexpected resolved base path %q", basePath)
	}
}

func TestCleanupCompatPathDoesNotRemoveAnotherInstanceAlias(t *testing.T) {
	dir := t.TempDir()
	alias := filepath.Join(dir, "ggcode-debug.log")
	other := filepath.Join(dir, "other.log")
	if err := os.WriteFile(other, []byte("other"), 0o644); err != nil {
		t.Fatalf("write other log: %v", err)
	}
	if err := os.Symlink(other, alias); err != nil {
		t.Fatalf("create alias: %v", err)
	}

	sink := &asyncFileSink{
		basePath:   filepath.Join(dir, "self.log"),
		compatPath: alias,
	}
	sink.cleanupCompatPath()

	if _, err := os.Lstat(alias); err != nil {
		t.Fatalf("expected alias for another instance to remain, err=%v", err)
	}
}

func TestParseTagFilter(t *testing.T) {
	tests := []struct {
		input string
		want  map[string]bool // nil = all
	}{
		{"", nil},
		{"1", nil},
		{"true", nil},
		{"all", nil},
		{"agent", map[string]bool{"agent": true}},
		{"agent,provider", map[string]bool{"agent": true, "provider": true}},
		{" agent , provider ", map[string]bool{"agent": true, "provider": true}},
	}

	for _, tt := range tests {
		got := parseTagFilter(tt.input)
		if tt.want == nil && got != nil {
			t.Errorf("parseTagFilter(%q) = %v, want nil", tt.input, got)
		}
		if tt.want != nil && got == nil {
			t.Errorf("parseTagFilter(%q) = nil, want %v", tt.input, tt.want)
		}
		if tt.want != nil && got != nil {
			for k := range tt.want {
				if !got[k] {
					t.Errorf("parseTagFilter(%q): missing key %q", tt.input, k)
				}
			}
		}
	}
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
