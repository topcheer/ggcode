package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/provider"
)

func TestFormatPipeProgressEventToolCall(t *testing.T) {
	args, err := json.Marshal(map[string]string{"path": "packages/web/src/App.tsx"})
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	got := formatPipeProgressEvent(provider.StreamEvent{
		Type: provider.StreamEventToolCallDone,
		Tool: provider.ToolCallDelta{Name: "read_file", Arguments: args},
	})
	if got != "tool: read_file packages/web/src/App.tsx" {
		t.Fatalf("unexpected tool call progress: %q", got)
	}
}

func TestFormatPipeProgressEventToolResult(t *testing.T) {
	got := formatPipeProgressEvent(provider.StreamEvent{
		Type:   provider.StreamEventToolResult,
		Result: "Status: running\nTotal lines: 4\nRecent output:\nstep 4\n",
	})
	if got != "tool result: Status: running" {
		t.Fatalf("unexpected tool result progress: %q", got)
	}
}

func TestSummarizePipeToolArgumentsPrefersCommandPreview(t *testing.T) {
	args, err := json.Marshal(map[string]string{"command": "# comment\nnpm test\nnpm run build\n"})
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	got := summarizePipeToolArguments(args)
	if got != "npm test" {
		t.Fatalf("unexpected command preview: %q", got)
	}
}

func TestTruncatePipeProgress(t *testing.T) {
	got := truncatePipeProgress(strings.Repeat("a", 20), 10)
	if got != "aaaaaaa..." {
		t.Fatalf("unexpected truncation: %q", got)
	}
}

func TestPipeAllowedDirsBaseUsesConfigDir(t *testing.T) {
	cfgPath := filepath.Join(string(filepath.Separator), "tmp", "repo", "ggcode.yaml")
	if got := pipeAllowedDirsBase(cfgPath); got != filepath.Dir(cfgPath) {
		t.Fatalf("pipeAllowedDirsBase() = %q, want %q", got, filepath.Dir(cfgPath))
	}
}

func TestPipeAllowedDirsBaseFallsBackToDot(t *testing.T) {
	if got := pipeAllowedDirsBase(""); got != "." {
		t.Fatalf("pipeAllowedDirsBase() = %q, want %q", got, ".")
	}
}

func TestPipePermissionModeHonorsBypass(t *testing.T) {
	if got := pipePermissionMode(true); got != permission.BypassMode {
		t.Fatalf("pipePermissionMode(true) = %v, want %v", got, permission.BypassMode)
	}
	if got := pipePermissionMode(false); got != permission.AutoMode {
		t.Fatalf("pipePermissionMode(false) = %v, want %v", got, permission.AutoMode)
	}
}
