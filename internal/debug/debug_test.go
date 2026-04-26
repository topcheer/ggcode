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

func TestPerCategoryEnvOverridesGlobal(t *testing.T) {
	// Set per-category env, no global
	os.Setenv("GGCODE_DEBUG_AGENT", "1")
	os.Setenv("GGCODE_DEBUG_OPENAI", "1")
	defer os.Unsetenv("GGCODE_DEBUG_AGENT")
	defer os.Unsetenv("GGCODE_DEBUG_OPENAI")

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
		t.Fatal("expected debug to be enabled when per-category env is set")
	}

	pid := os.Getpid()

	Log("agent", "agent msg")
	Log("openai", "openai msg")
	Log("knight", "knight msg - should be filtered")

	time.Sleep(200 * time.Millisecond)

	// Agent file should exist
	agentPath := filepath.Join(defaultLogDir, fmt.Sprintf("ggcode-agent-%d.log", pid))
	if _, err := os.Stat(agentPath); err != nil {
		t.Errorf("expected agent log: %v", err)
	}

	// OpenAI file should exist
	openaiPath := filepath.Join(defaultLogDir, fmt.Sprintf("ggcode-openai-%d.log", pid))
	if _, err := os.Stat(openaiPath); err != nil {
		t.Errorf("expected openai log: %v", err)
	}

	// Knight file should NOT exist (not in per-category env)
	knightPath := filepath.Join(defaultLogDir, fmt.Sprintf("ggcode-knight-%d.log", pid))
	if _, err := os.Stat(knightPath); !os.IsNotExist(err) {
		t.Error("expected knight log to NOT exist when not in per-category env")
	}
}

func TestPerCategoryEnvIgnoredWhenGlobalSet(t *testing.T) {
	// Both global and per-category set: per-category takes precedence
	os.Setenv("GGCODE_DEBUG_AGENT", "1")
	os.Setenv(envKey, "knight") // global says knight only
	defer os.Unsetenv("GGCODE_DEBUG_AGENT")
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

	pid := os.Getpid()

	Log("agent", "agent msg")
	Log("knight", "knight msg")

	time.Sleep(200 * time.Millisecond)

	// Per-category overrides global: agent should exist, knight should NOT
	agentPath := filepath.Join(defaultLogDir, fmt.Sprintf("ggcode-agent-%d.log", pid))
	if _, err := os.Stat(agentPath); err != nil {
		t.Errorf("expected agent log (per-category override): %v", err)
	}

	knightPath := filepath.Join(defaultLogDir, fmt.Sprintf("ggcode-knight-%d.log", pid))
	if _, err := os.Stat(knightPath); !os.IsNotExist(err) {
		t.Error("expected knight log to NOT exist (global ignored when per-category set)")
	}
}

func TestMessageTruncation(t *testing.T) {
	EnableForTest(t)

	pid := os.Getpid()

	longMsg := strings.Repeat("x", 200)
	Log("agent", longMsg)

	time.Sleep(200 * time.Millisecond)

	mainPath := filepath.Join(defaultLogDir, fmt.Sprintf("ggcode-debug-%d.log", pid))
	data, err := os.ReadFile(mainPath)
	if err != nil {
		t.Fatalf("read main log: %v", err)
	}

	content := string(data)
	// Each line should be at most maxMessageLen + tag prefix + newline
	for _, line := range strings.Split(content, "\n") {
		if line == "" {
			continue
		}
		// The formatted message includes "[agent] " prefix + truncated content
		// Total should be around maxMessageLen
		if len(line) > maxMessageLen+5 { // small margin for timestamp
			t.Errorf("log line too long (%d chars): %q", len(line), line)
		}
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

	time.Sleep(200 * time.Millisecond)

	// Agent category file should exist
	agentPath := filepath.Join(defaultLogDir, fmt.Sprintf("ggcode-agent-%d.log", pid))
	if _, err := os.Stat(agentPath); err != nil {
		t.Errorf("expected agent log file: %v", err)
	}

	// OpenAI category file should exist (separate from provider now)
	openaiPath := filepath.Join(defaultLogDir, fmt.Sprintf("ggcode-openai-%d.log", pid))
	if _, err := os.Stat(openaiPath); err != nil {
		t.Errorf("expected openai log file: %v", err)
	}

	// Knight category file should exist
	knightPath := filepath.Join(defaultLogDir, fmt.Sprintf("ggcode-knight-%d.log", pid))
	if _, err := os.Stat(knightPath); err != nil {
		t.Errorf("expected knight log file: %v", err)
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

	Log("agent", "agent message")
	Log("openai", "openai message")
	Log("unknown", "unknown message")

	time.Sleep(200 * time.Millisecond)

	agentPath := filepath.Join(defaultLogDir, fmt.Sprintf("ggcode-agent-%d.log", pid))
	if _, err := os.Stat(agentPath); err != nil {
		t.Errorf("expected agent log file: %v", err)
	}

	openaiPath := filepath.Join(defaultLogDir, fmt.Sprintf("ggcode-openai-%d.log", pid))
	if _, err := os.Stat(openaiPath); !os.IsNotExist(err) {
		t.Error("expected openai log to NOT exist when filtered")
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
		{"agent,openai", map[string]bool{"agent": true, "openai": true}},
		{" agent , openai ", map[string]bool{"agent": true, "openai": true}},
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
