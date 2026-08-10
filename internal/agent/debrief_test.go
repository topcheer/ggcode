package agent

import (
	"strings"
	"testing"
	"time"
)

func TestDebriefAnalyzer_BasicExtraction(t *testing.T) {
	da := newDebriefAnalyzer("test-session-001")

	// Simulate tool calls
	da.recordToolCall("edit_file", map[string]interface{}{"path": "foo.go"}, "success", false)
	da.recordToolCall("run_command", map[string]interface{}{"command": "go test"}, "PASS", false)
	da.recordToolCall("grep", map[string]interface{}{"pattern": "TODO"}, "found 5 matches", false)

	// Not enough tools for debrief (minToolsForDebrief = 3)
	da.startTime = time.Now().Add(-60 * time.Second) // 1 minute ago
	points := da.finalize()

	if len(points) > 0 {
		t.Errorf("Expected no debrief points for minimal session, got %d", len(points))
	}
}

func TestDebriefAnalyzer_ErrorPatterns(t *testing.T) {
	da := newDebriefAnalyzer("test-session-002")

	// Record multiple undefined symbol errors
	da.recordToolCall("run_command", map[string]interface{}{"command": "go build"}, `undefined: foo`, true)
	da.recordToolCall("run_command", map[string]interface{}{"command": "go build"}, `undefined: bar`, true)
	da.recordToolCall("run_command", map[string]interface{}{"command": "go build"}, `undefined: baz`, true)
	da.recordToolCall("edit_file", map[string]interface{}{"path": "main.go"}, "success", false)
	da.recordToolCall("grep", map[string]interface{}{"pattern": "TODO"}, "found 5 matches", false)

	da.startTime = time.Now().Add(-60 * time.Second)
	points := da.finalize()

	// Debug: print all points
	for _, p := range points {
		t.Logf("Point: Category=%s Title=%s Confidence=%.2f", p.Category, p.Title, p.Confidence)
	}

	// Should detect recurring undefined-symbol error
	found := false
	for _, p := range points {
		if p.Category == "failure" && strings.Contains(p.Title, "Undefined") {
			found = true
			if p.Confidence <= 0 {
				t.Errorf("Expected positive confidence for recurring error, got %.2f", p.Confidence)
			}
			break
		}
	}
	if !found {
		t.Error("Expected to find 'Undefined symbol' debrief point")
	}
}

func TestDebriefAnalyzer_ToolOverreliance(t *testing.T) {
	da := newDebriefAnalyzer("test-session-003")

	// Record heavy use of a single tool
	for i := 0; i < 10; i++ {
		da.recordToolCall("edit_file", map[string]interface{}{"path": "file.go"}, "success", false)
	}
	// Minimal use of other tools
	da.recordToolCall("grep", map[string]interface{}{"pattern": "test"}, "found", false)
	da.recordToolCall("read_file", map[string]interface{}{"path": "README.md"}, "content", false)

	da.startTime = time.Now().Add(-60 * time.Second)
	points := da.finalize()

	// Should detect tool tunnel vision
	found := false
	for _, p := range points {
		if p.Category == "strategy" && p.Title == "High reliance on edit_file" {
			found = true
			if p.Confidence < 0.5 {
				t.Errorf("Expected high confidence for tool overreliance, got %.2f", p.Confidence)
			}
			break
		}
	}
	if !found {
		t.Error("Expected to find 'High reliance on edit_file' debrief point")
	}
}

func TestDebriefAnalyzer_SuccessPatterns(t *testing.T) {
	da := newDebriefAnalyzer("test-session-004")

	// Record successful Go test runs
	for i := 0; i < 5; i++ {
		da.recordToolCall("run_command", map[string]interface{}{"command": "go test ./..."}, "PASS", false)
	}
	da.recordToolCall("run_command", map[string]interface{}{"command": "go build"}, "success", false)
	da.recordToolCall("edit_file", map[string]interface{}{"path": "main.go"}, "success", false)
	da.recordToolCall("read_file", map[string]interface{}{"path": "README.md"}, "content", false)

	da.startTime = time.Now().Add(-60 * time.Second)
	points := da.finalize()

	// Debug: print all points
	for _, p := range points {
		t.Logf("Point: Category=%s Title=%s", p.Category, p.Title)
	}

	// Should detect effective cmd-go strategy (both go test and go build contribute)
	found := false
	for _, p := range points {
		if p.Category == "success" && strings.Contains(p.Title, "cmd-go") {
			found = true
			if p.Confidence <= 0 {
				t.Errorf("Expected positive confidence for success pattern, got %.2f", p.Confidence)
			}
			break
		}
	}
	if !found {
		t.Error("Expected to find success pattern for cmd-go")
	}
}

func TestDebriefAnalyzer_CapsAtMaxPoints(t *testing.T) {
	da := newDebriefAnalyzer("test-session-005")

	// Generate many different error patterns
	errors := []string{
		`undefined: foo`,
		`type mismatch`,
		`cannot find package "bar"`,
		`permission denied`,
		`timeout after 30s`,
		`syntax error: unexpected`,
		`nil pointer dereference`,
		`data race detected`,
		`module not found`,
		`api error: 500`,
	}

	for _, err := range errors {
		da.recordToolCall("run_command", map[string]interface{}{"command": "test"}, err, true)
		// Add some successful edits to mix
		da.recordToolCall("edit_file", map[string]interface{}{"path": "file.go"}, "success", false)
	}

	da.startTime = time.Now().Add(-60 * time.Second)
	points := da.finalize()

	// Should cap at maxDebriefPoints (8)
	if len(points) > maxDebriefPoints {
		t.Errorf("Expected at most %d debrief points, got %d", maxDebriefPoints, len(points))
	}
}

func TestDebriefAnalyzer_SkipsShortSessions(t *testing.T) {
	da := newDebriefAnalyzer("test-session-006")

	// Record many tools but very short duration
	for i := 0; i < 10; i++ {
		da.recordToolCall("edit_file", map[string]interface{}{"path": "file.go"}, "success", false)
	}

	// Session only 5 seconds (below minSessionDuration = 30)
	da.startTime = time.Now().Add(-5 * time.Second)
	points := da.finalize()

	if len(points) > 0 {
		t.Errorf("Expected no debrief points for very short session, got %d", len(points))
	}
}

func TestHumanizeErrorPattern(t *testing.T) {
	da := newDebriefAnalyzer("test")

	tests := []struct {
		input    string
		expected string
	}{
		{"undefined-symbol", "Undefined symbol"},
		{"type-mismatch", "Type mismatch"},
		{"import-error", "Import error"},
		{"nil-dereference", "Nil dereference"},
		{"race-condition", "Race condition"},
		{"unknown-pattern", "Unknown pattern"},
	}

	for _, tc := range tests {
		got := da.humanizeErrorPattern(tc.input)
		if got != tc.expected {
			t.Errorf("humanizeErrorPattern(%q) = %q, want %q", tc.input, got, tc.expected)
		}
	}
}

func TestDebriefPoint_ToJSON(t *testing.T) {
	point := &DebriefPoint{
		Category:   "failure",
		Title:      "Test error",
		Detail:     "This is a test",
		Confidence: 0.8,
		Timestamp:  time.Now(),
		SessionID:  "test-session",
	}

	json, err := point.toJSON()
	if err != nil {
		t.Fatalf("toJSON() error: %v", err)
	}

	if json == "" {
		t.Error("toJSON() returned empty string")
	}

	// Verify JSON contains expected fields
	expectedFields := []string{"category", "title", "detail", "confidence", "timestamp", "session_id"}
	for _, field := range expectedFields {
		if !strings.Contains(json, field) {
			t.Errorf("Expected JSON to contain field %q", field)
		}
	}
}

func TestDebriefAnalyzer_FormatSummary(t *testing.T) {
	da := newDebriefAnalyzer("test-session")
	da.points = []*DebriefPoint{
		{
			Category:   "success",
			Title:      "Good pattern",
			Detail:     "This worked well",
			Confidence: 0.9,
			Timestamp:  time.Now(),
			SessionID:  "test-session",
		},
		{
			Category:   "failure",
			Title:      "Bad pattern",
			Detail:     "This failed",
			Confidence: 0.5,
			Timestamp:  time.Now(),
			SessionID:  "test-session",
		},
	}

	summary := da.formatSummary()

	if summary == "" {
		t.Error("formatSummary() returned empty string")
	}

	// Verify summary contains key elements
	expected := []string{"Session Debrief", "2 insights", "SUCCESS", "Good pattern", "FAILURE", "Bad pattern"}
	for _, exp := range expected {
		if !strings.Contains(summary, exp) {
			t.Errorf("Expected summary to contain %q", exp)
		}
	}
}
