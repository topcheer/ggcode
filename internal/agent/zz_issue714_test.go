package agent

import (
	"encoding/json"
	"runtime"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

func TestIssue714_CaseDistinctPathsPlatformAware(t *testing.T) {
	fold := batchPathFoldActive()
	switch runtime.GOOS {
	case "darwin", "windows":
		if !fold {
			t.Fatalf("pathFoldActive should be true on %s", runtime.GOOS)
		}
		if normalizeBatchPath("Config.yaml") != normalizeBatchPath("config.yaml") {
			t.Fatalf("case-insensitive platform: Config.yaml and config.yaml must match")
		}
	default:
		if fold {
			t.Fatalf("pathFoldActive should be false on %s", runtime.GOOS)
		}
		if normalizeBatchPath("Config.yaml") == normalizeBatchPath("config.yaml") {
			t.Fatalf("case-sensitive platform: Config.yaml and config.yaml are different files, must not fold")
		}
	}
}

func TestIssue714_FoldIsASCIIO(t *testing.T) {
	got := asciiFoldLower("Config.YAML")
	if got != "config.yaml" {
		t.Fatalf("asciiFoldLower = %q, want config.yaml", got)
	}

	if in, out := "配置/Config.yaml", asciiFoldLower("配置/Config.yaml"); len(in) != len(out) {
		t.Fatalf("byte length changed: %q -> %q", in, out)
	}
}

func TestIssue714_CaseSensitiveNoFalseConflict(t *testing.T) {
	calls := []provider.ToolCallDelta{
		{Name: "edit_file", Arguments: json.RawMessage(`{"file_path":"Config.yaml","old_text":"a","new_text":"b"}`)},
		{Name: "edit_file", Arguments: json.RawMessage(`{"file_path":"config.yaml","old_text":"a","new_text":"b"}`)},
	}
	conflicts := detectBatchEditConflicts(calls)
	if batchPathFoldActive() {
		if len(conflicts) == 0 {
			t.Fatalf("case-insensitive platform: expected a conflict for the same file")
		}
		return
	}
	if len(conflicts) != 0 {
		t.Fatalf("case-sensitive platform: Config.yaml vs config.yaml are different files, got false conflicts: %v", conflicts)
	}
}
