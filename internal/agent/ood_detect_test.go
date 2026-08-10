package agent

import (
	"testing"
)

func TestOODDetectorBasic(t *testing.T) {
	d := newOODDetector()

	// Warm up with some observations
	d.addObservations([]string{".go", ".md", ".ts"}, []string{"read_file+edit_file", "edit_file+grep", "grep+write_file"})

	// Test observation recording
	targets := extractTargetsFromToolCall("read_file", []byte(`{"path":"main.go"}`))
	d.recordObservation("read_file", targets)

	if d.sessionCount != 7 { // 3 file exts + 3 tool combos + 1 new observation
		t.Errorf("expected sessionCount=7, got %d", d.sessionCount)
	}
}

func TestOODDetectionNovelFileType(t *testing.T) {
	d := newOODDetector()

	// Warm up with common file types and tool combos
	d.addObservations([]string{".go", ".md", ".ts", ".js", ".json"}, []string{"read_file+edit_file", "edit_file+grep"})
	d.alertThreshold = 0.25 // lower threshold for this test (single feature = 0.3)

	// Force past minSamples threshold
	for i := 0; i < 50; i++ {
		d.recordObservation("read_file", []string{"test.go"})
	}

	// Reset prevTool for the novel check
	d.prevTool = "read_file"

	// Now encounter a novel file type
	targets := []string{"unknown.xyz"}
	signal := d.checkOOD("read_file", targets)

	if signal == nil {
		t.Fatal("expected OOD signal for novel file type")
	}

	if signal.Severity != SeverityMedium {
		t.Errorf("expected SeverityMedium, got %v", signal.Severity)
	}

	if len(signal.Features) == 0 {
		t.Fatal("expected novel features")
	}

	t.Logf("OOD signal: %v", signal.Message)
	t.Logf("Features: %v", signal.Features)
}

func TestOODDetectionNovelToolCombo(t *testing.T) {
	d := newOODDetector()

	// Warm up with common tool combos and file types
	d.addObservations([]string{".go", ".md"}, []string{"read_file+edit_file", "edit_file+grep", "grep+write_file"})
	d.alertThreshold = 0.35 // lower threshold for this test (single combo = 0.4)

	// Force past minSamples threshold
	for i := 0; i < 50; i++ {
		d.recordObservation("read_file", []string{"test.go"})
	}

	// Now encounter a novel tool combo
	targets := []string{"test.go"}
	d.prevTool = "read_file" // set previous tool (seen before)

	signal := d.checkOOD("novel_tool", targets) // novel_tool has never been seen

	if signal == nil {
		t.Fatal("expected OOD signal for novel tool combo")
	}

	if signal.Severity != SeverityMedium {
		t.Errorf("expected SeverityMedium, got %v", signal.Severity)
	}

	t.Logf("OOD signal: %v", signal.Message)
}

func TestOODDetectionInactiveBeforeMinSamples(t *testing.T) {
	d := newOODDetector()

	// Don't warm up - stay below minSamples
	d.sessionCount = 10

	// Try to detect OOD with completely novel patterns
	targets := []string{"completely_unknown.xyz"}
	d.prevTool = "never_seen_before"

	signal := d.checkOOD("novel_tool", targets)

	if signal != nil {
		t.Fatalf("expected no OOD signal before minSamples, got: %v", signal)
	}
}

func TestOODDetectionHighCertainty(t *testing.T) {
	d := newOODDetector()

	// Warm up with limited file types
	d.addObservations([]string{".go", ".md"}, []string{})

	// Force past minSamples threshold
	d.sessionCount = 50

	// Encounter multiple novel features at once (high certainty)
	targets := []string{"novel1.xyz", "novel2.abc", "novel3.def"}
	d.prevTool = "read_file"

	signal := d.checkOOD("novel_tool", targets)

	if signal == nil {
		t.Fatal("expected OOD signal for multiple novel features")
	}

	// With 3 novel features, certainty should be > 0.9, triggering HIGH severity
	if signal.Severity != SeverityHigh {
		t.Errorf("expected SeverityHigh for high certainty, got %v (certainty=%.2f)", signal.Severity, signal.Certainty)
	}

	t.Logf("Certainty: %.2f, Severity: %v", signal.Certainty, signal.Severity)
}

func TestExtractFileExt(t *testing.T) {
	tests := []struct {
		path     string
		expected string
	}{
		{"file.go", ".go"},
		{"file.ts", ".ts"},
		{"file.test.go", ".go"},
		{"path/to/file.md", ".md"},
		{"file.tar.gz", ".tar.gz"},
		{"file-with-dashes.json", ".json"},
		{"file", ""},
		{".hidden", ""},
		{"file.", ""},
		{"path/file.txt?query=1", ".txt"},
	}

	for _, tt := range tests {
		got := extractFileExt(tt.path)
		if got != tt.expected {
			t.Errorf("extractFileExt(%q) = %q, want %q", tt.path, got, tt.expected)
		}
	}
}

func TestExtractTargetsFromToolCall(t *testing.T) {
	tests := []struct {
		name     string
		toolName string
		args     []byte
		wantLen  int
	}{
		{
			name:     "read_file",
			toolName: "read_file",
			args:     []byte(`{"path":"main.go"}`),
			wantLen:  1,
		},
		{
			name:     "grep",
			toolName: "grep",
			args:     []byte(`{"pattern":"TODO","path":"./..."}`),
			wantLen:  2,
		},
		{
			name:     "multi_file_read",
			toolName: "multi_file_read",
			args:     []byte(`{"files":[{"path":"a.go"},{"path":"b.ts"}]}`),
			wantLen:  2,
		},
		{
			name:     "run_command",
			toolName: "run_command",
			args:     []byte(`{"command":"make build"}`),
			wantLen:  1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractTargetsFromToolCall(tt.toolName, tt.args)
			if len(got) != tt.wantLen {
				t.Errorf("extractTargetsFromToolCall() = %v, want %d targets", got, tt.wantLen)
			}
		})
	}
}

func TestOODDetectorPruning(t *testing.T) {
	d := newOODDetector()
	d.maxFileExts = 5 // set low threshold for testing

	// Add more extensions than max
	for i := 0; i < 10; i++ {
		ext := ".ext" + string(rune('a'+i))
		d.seenFileExts[ext] = i + 1
	}

	d.pruneMap(d.seenFileExts, d.maxFileExts)

	if len(d.seenFileExts) > d.maxFileExts {
		t.Errorf("expected at most %d extensions after pruning, got %d", d.maxFileExts, len(d.seenFileExts))
	}
}
