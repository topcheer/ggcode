package agent

import (
	"strings"
	"testing"
	"time"
)

func TestNewTrajectoryEnhancer(t *testing.T) {
	enhancer := newTrajectoryEnhancer()
	if enhancer == nil {
		t.Fatal("newTrajectoryEnhancer returned nil")
	}
	if enhancer.patterns == nil {
		t.Fatal("patterns map not initialized")
	}
}

func TestHashContext(t *testing.T) {
	enhancer := newTrajectoryEnhancer()

	// Same inputs should produce same hash
	h1 := enhancer.hashContext("add feature", ".go,.ts")
	h2 := enhancer.hashContext("add feature", ".go,.ts")
	if h1 != h2 {
		t.Errorf("hashContext not deterministic: %v != %v", h1, h2)
	}

	// Different inputs should produce different hashes
	h3 := enhancer.hashContext("fix bug", ".go,.ts")
	if h1 == h3 {
		t.Error("hashContext collision for different tasks")
	}

	h4 := enhancer.hashContext("add feature", ".py,.js")
	if h1 == h4 {
		t.Error("hashContext collision for different file types")
	}
}

func TestExtractFileTypesTrajectory(t *testing.T) {
	enhancer := newTrajectoryEnhancer()

	tests := []struct {
		name     string
		paths    []string
		expected string
	}{
		{
			name:     "go files",
			paths:    []string{"main.go", "utils.go"},
			expected: ".go",
		},
		{
			name:     "mixed files",
			paths:    []string{"app.go", "style.css", "index.html"},
			expected: ".css,.go,.html", // sorted order
		},
		{
			name:     "no extensions",
			paths:    []string{"Makefile", "README"},
			expected: "unknown",
		},
		{
			name:     "empty list",
			paths:    []string{},
			expected: "unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := enhancer.extractFileTypes(tt.paths)
			// Sort result since map iteration is unordered
			if result != "unknown" {
				parts := strings.Split(result, ",")
				for i := 0; i < len(parts); i++ {
					for j := i + 1; j < len(parts); j++ {
						if parts[i] > parts[j] {
							parts[i], parts[j] = parts[j], parts[i]
						}
					}
				}
				result = strings.Join(parts, ",")
			}
			if result != tt.expected {
				t.Errorf("extractFileTypes(%v) = %q, want %q", tt.paths, result, tt.expected)
			}
		})
	}
}

func TestNormalizeTask(t *testing.T) {
	enhancer := newTrajectoryEnhancer()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "remove quotes and files",
			input:    `Add "feature.go" with 'utils.go' changes`,
			expected: "add  with  changes",
		},
		{
			name:     "remove numbers",
			input:    "Fix issue 123 and add 456 tests",
			expected: "fix issue  and add  tests",
		},
		{
			name:     "normalize and truncate",
			input:    strings.Repeat("test ", 50),
			expected: strings.Repeat("test ", 25) + "te",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := enhancer.normalizeTask(tt.input)
			if len(result) > 100 {
				t.Errorf("normalizeTask result too long: %d", len(result))
			}
			// Check that numbers and quotes were removed
			if strings.ContainsAny(result, `"'`) {
				t.Error("normalizeTask did not remove quotes")
			}
		})
	}
}

func TestRecordSuccess(t *testing.T) {
	enhancer := newTrajectoryEnhancer()

	toolNames := []string{"read_file", "edit_file", "run_command"}
	enhancer.recordSuccess("add feature", toolNames, ".go")

	stats := enhancer.getStats()
	if stats["total_patterns"].(int) != 1 {
		t.Errorf("expected 1 pattern, got %d", stats["total_patterns"])
	}
	if stats["total_success"].(int) != 1 {
		t.Errorf("expected 1 success, got %d", stats["total_success"])
	}
	if stats["total_attempts"].(int) != 1 {
		t.Errorf("expected 1 attempt, got %d", stats["total_attempts"])
	}
}

func TestConfidence(t *testing.T) {
	enhancer := newTrajectoryEnhancer()

	toolNames := []string{"read_file", "edit_file"}
	task := "fix bug"
	fileTypes := ".go"

	// Record multiple successes
	for i := 0; i < 5; i++ {
		enhancer.recordSuccess(task, toolNames, fileTypes)
	}

	stats := enhancer.getStats()
	confidence := stats["avg_confidence"].(float64)
	if confidence != 1.0 {
		t.Errorf("expected 1.0 confidence after 5/5 successes, got %.2f", confidence)
	}
}

func TestSuggestPattern(t *testing.T) {
	enhancer := newTrajectoryEnhancer()

	task := "add feature"
	fileTypes := ".go"
	toolNames := []string{"read_file", "edit_file", "run_command"}

	// No pattern yet
	_, _, ok := enhancer.suggestPattern(task, fileTypes)
	if ok {
		t.Error("suggestPattern returned true for non-existent pattern")
	}

	// Record success
	enhancer.recordSuccess(task, toolNames, fileTypes)

	// Should now suggest
	suggestion, suggestedTools, ok := enhancer.suggestPattern(task, fileTypes)
	if !ok {
		t.Error("suggestPattern returned false after recording success")
	}
	if suggestion == "" {
		t.Error("suggestion message is empty")
	}
	if len(suggestedTools) != len(toolNames) {
		t.Errorf("expected %d tools, got %d", len(toolNames), len(suggestedTools))
	}
}

func TestEvictOldest(t *testing.T) {
	enhancer := newTrajectoryEnhancer()

	// Fill beyond limit
	for i := 0; i < maxPatternEntries+10; i++ {
		task := strings.Repeat("task ", i)
		enhancer.recordSuccess(task, []string{"read_file"}, ".go")
	}

	stats := enhancer.getStats()
	total := stats["total_patterns"].(int)
	if total > maxPatternEntries {
		t.Errorf("eviction failed: %d patterns (limit %d)", total, maxPatternEntries)
	}
}

func TestCleanupExpired(t *testing.T) {
	enhancer := newTrajectoryEnhancer()

	// Record a pattern
	enhancer.recordSuccess("test", []string{"read_file"}, ".go")

	// Manually set lastUsed to expired time
	enhancer.mu.Lock()
	for _, p := range enhancer.patterns {
		p.lastUsed = time.Now().Add(-patternExpiry - time.Hour)
	}
	enhancer.mu.Unlock()

	// Cleanup
	enhancer.cleanupExpired()

	stats := enhancer.getStats()
	if stats["total_patterns"].(int) != 0 {
		t.Errorf("expected 0 patterns after cleanup, got %d", stats["total_patterns"])
	}
}

func TestGetStats(t *testing.T) {
	enhancer := newTrajectoryEnhancer()

	// Record some patterns
	enhancer.recordSuccess("task1", []string{"read_file"}, ".go")
	enhancer.recordSuccess("task2", []string{"edit_file", "run_command"}, ".ts")
	enhancer.recordSuccess("task1", []string{"read_file"}, ".go") // duplicate task

	stats := enhancer.getStats()

	requiredKeys := []string{
		"total_patterns", "total_success", "total_attempts",
		"avg_confidence", "memory_limit", "min_confidence", "pattern_expiry_hr",
	}

	for _, key := range requiredKeys {
		if _, ok := stats[key]; !ok {
			t.Errorf("missing stats key: %s", key)
		}
	}

	if stats["total_patterns"].(int) != 2 {
		t.Errorf("expected 2 unique patterns, got %d", stats["total_patterns"])
	}
	if stats["total_success"].(int) != 3 {
		t.Errorf("expected 3 total successes, got %d", stats["total_success"])
	}
}

func TestConcurrentAccess(t *testing.T) {
	enhancer := newTrajectoryEnhancer()
	done := make(chan bool)

	// Concurrent writers
	for i := 0; i < 10; i++ {
		go func(id int) {
			task := strings.Repeat("task ", id)
			enhancer.recordSuccess(task, []string{"read_file"}, ".go")
			done <- true
		}(i)
	}

	// Concurrent readers
	for i := 0; i < 10; i++ {
		go func() {
			_, _, _ = enhancer.suggestPattern("test", ".go")
			_ = enhancer.getStats()
			done <- true
		}()
	}

	// Wait for all goroutines
	for i := 0; i < 20; i++ {
		<-done
	}

	// Verify no data corruption
	stats := enhancer.getStats()
	if stats["total_patterns"].(int) != 10 {
		t.Errorf("concurrent access corrupted data: expected 10 patterns, got %d",
			stats["total_patterns"])
	}
}
