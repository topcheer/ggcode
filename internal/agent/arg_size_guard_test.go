package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAnalyzeArgSize_NoArgs(t *testing.T) {
	if hint := analyzeArgSize("edit_file", []byte(`{}`)); hint != "" {
		t.Errorf("expected empty hint for empty args, got %q", hint)
	}
}

func TestAnalyzeArgSize_SmallArgs(t *testing.T) {
	args := []byte(`{"file_path":"/tmp/test.go","old_text":"hello","new_text":"world"}`)
	if hint := analyzeArgSize("edit_file", args); hint != "" {
		t.Errorf("expected empty hint for small args, got %q", hint)
	}
}

func TestAnalyzeArgSize_OversizedOldText(t *testing.T) {
	// Create a 5KB old_text — above argSizeWarnField (4KB)
	big := strings.Repeat("x", 5*1024)
	args, _ := json.Marshal(map[string]interface{}{
		"file_path": "/tmp/test.go",
		"old_text":  big,
		"new_text":  "world",
	})
	hint := analyzeArgSize("edit_file", args)
	if hint == "" {
		t.Fatal("expected hint for oversized old_text")
	}
	if !strings.Contains(hint, "old_text") {
		t.Errorf("hint should mention old_text, got %q", hint)
	}
	if !strings.Contains(hint, "line-number anchors") {
		t.Errorf("hint should recommend line-number anchors, got %q", hint)
	}
}

func TestAnalyzeArgSize_SevereOldText(t *testing.T) {
	// Create a 20KB old_text — above argSizeSevereField (16KB)
	big := strings.Repeat("y", 20*1024)
	args, _ := json.Marshal(map[string]interface{}{
		"file_path": "/tmp/test.go",
		"old_text":  big,
		"new_text":  "world",
	})
	hint := analyzeArgSize("edit_file", args)
	if hint == "" {
		t.Fatal("expected hint for severely oversized old_text")
	}
	if !strings.Contains(hint, "concise line-number anchors") {
		t.Errorf("severe hint should strongly recommend line-number anchors, got %q", hint)
	}
}

func TestAnalyzeArgSize_OversizedWriteContent(t *testing.T) {
	big := strings.Repeat("z", 6*1024)
	args, _ := json.Marshal(map[string]interface{}{
		"path":    "/tmp/test.go",
		"content": big,
	})
	hint := analyzeArgSize("write_file", args)
	if hint == "" {
		t.Fatal("expected hint for oversized write_file content")
	}
	if !strings.Contains(hint, "edit_file") {
		t.Errorf("hint should suggest edit_file, got %q", hint)
	}
}

func TestAnalyzeArgSize_OversizedGrepPattern(t *testing.T) {
	big := strings.Repeat("a", 5*1024)
	args, _ := json.Marshal(map[string]interface{}{
		"pattern":     big,
		"description": "test",
	})
	hint := analyzeArgSize("grep", args)
	if hint == "" {
		t.Fatal("expected hint for oversized grep pattern")
	}
	if !strings.Contains(hint, "pattern") {
		t.Errorf("hint should mention pattern, got %q", hint)
	}
}

func TestAnalyzeArgSize_TotalPayloadSize(t *testing.T) {
	// 10KB total args — above argSizeWarnTotal (8KB), but individual fields
	// may be below argSizeWarnField. Should still warn about total size.
	big := strings.Repeat("a", 5000)
	big2 := strings.Repeat("b", 4000)
	args, _ := json.Marshal(map[string]interface{}{
		"old_text": big,
		"new_text": big2,
	})
	hint := analyzeArgSize("edit_file", args)
	if hint == "" {
		t.Fatal("expected hint for large total payload")
	}
	if !strings.Contains(hint, "total argument size") {
		t.Errorf("hint should mention total size, got %q", hint)
	}
}

func TestAnalyzeArgSize_MultiFileEditOversized(t *testing.T) {
	big := strings.Repeat("c", 20*1024)
	args := []byte(`{"files":[{"path":"/tmp/a.go","edits":[{"old_text":"` + big + `","new_text":"x"}]}],"mode":"atomic"}`)
	hint := analyzeArgSize("multi_file_edit", args)
	if hint == "" {
		t.Fatal("expected hint for oversized multi_file_edit")
	}
	if !strings.Contains(hint, "line-number anchors") {
		t.Errorf("hint should recommend line-number anchors, got %q", hint)
	}
}

func TestAnalyzeArgSize_InvalidJSON(t *testing.T) {
	hint := analyzeArgSize("edit_file", []byte(`{invalid`))
	if hint != "" {
		t.Errorf("expected empty hint for invalid JSON, got %q", hint)
	}
}

func TestCheckArgSizeGuard_FiresOnce(t *testing.T) {
	a := &Agent{}
	big := strings.Repeat("x", 20*1024)
	args, _ := json.Marshal(map[string]interface{}{
		"old_text": big,
	})

	hint1 := a.checkArgSizeGuard("edit_file", args)
	if hint1 == "" {
		t.Fatal("expected first hint to fire")
	}
	if a.argSizeGuardFires != 1 {
		t.Errorf("expected argSizeGuardFires=1, got %d", a.argSizeGuardFires)
	}

	hint2 := a.checkArgSizeGuard("edit_file", args)
	if hint2 != "" {
		t.Errorf("expected second hint to be suppressed, got %q", hint2)
	}
}

func TestCheckArgSizeGuard_ResetsOnNewRun(t *testing.T) {
	a := &Agent{argSizeGuardFires: 1}
	a.argSizeGuardFires = 0 // simulate reset

	big := strings.Repeat("x", 20*1024)
	args, _ := json.Marshal(map[string]interface{}{
		"old_text": big,
	})
	hint := a.checkArgSizeGuard("edit_file", args)
	if hint == "" {
		t.Fatal("expected hint after reset")
	}
}

func TestFormatArgBytes(t *testing.T) {
	tests := []struct {
		input int
		want  string
	}{
		{100, "100B"},
		{1024, "1.0KB"},
		{5120, "5.0KB"},
		{16384, "16.0KB"},
	}
	for _, tt := range tests {
		got := formatArgBytes(tt.input)
		if got != tt.want {
			t.Errorf("formatArgBytes(%d) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
