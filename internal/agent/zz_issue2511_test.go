package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

func TestIssue2511_MemoryToolDisabledByDefault(t *testing.T) {
	s := newMemoryToolState()
	if s.enabled {
		t.Fatalf("new memory tool state must start disabled (declaration-gated)")
	}
	dir := t.TempDir()
	args, _ := json.Marshal(memoryRawArgs{Command: "view", Path: "/memories"})
	res := s.executeResult(args, dir)
	if !res.IsError {
		t.Fatalf("expected IsError when memory tool is not enabled, got %+v", res)
	}
	if !strings.Contains(res.Content, "not enabled") {
		t.Fatalf("expected 'not enabled' error, got %q", res.Content)
	}

	if _, err := os.Stat(filepath.Join(dir, ".ggcode", "memories")); !os.IsNotExist(err) {
		t.Fatalf("disabled memory call must not create the store directory, stat err=%v", err)
	}
}

func TestIssue2511_MemoryToolEnabledExecutes(t *testing.T) {
	s := newMemoryToolState()
	s.enabled = true
	dir := t.TempDir()
	args, _ := json.Marshal(memoryRawArgs{Command: "view", Path: "/memories"})
	res := s.executeResult(args, dir)
	if res.IsError {
		t.Fatalf("enabled view of the store root must succeed, got %+v", res)
	}
	if !strings.Contains(res.Content, "/memories") {
		t.Fatalf("expected store root listing, got %q", res.Content)
	}
}

func TestIssue2511_NilStateIsSafe(t *testing.T) {
	var s *memoryToolState
	args, _ := json.Marshal(memoryRawArgs{Command: "view", Path: "/memories"})
	res := s.executeResult(args, t.TempDir())
	if !res.IsError {
		t.Fatalf("nil memoryToolState must return an error result, got %+v", res)
	}
}

func TestIssue2511_AgentSetterGatesExecutor(t *testing.T) {
	a := newTestAgent(t)
	a.memoryTool = newMemoryToolState()
	if a.memoryTool == nil {
		t.Fatalf("memory tool state must exist (initialized in NewAgent)")
	}
	if a.memoryTool.enabled {
		t.Fatalf("memory executor must default to disabled before provider application")
	}
	a.SetMemoryToolEnabled(true)
	if !a.memoryTool.enabled {
		t.Fatalf("SetMemoryToolEnabled(true) must enable the executor")
	}
	a.SetMemoryToolEnabled(false)
	if a.memoryTool.enabled {
		t.Fatalf("SetMemoryToolEnabled(false) must disable the executor (config reload resets)")
	}
}

func TestIssue2511_DisabledInterceptReturnsErrorNotUnknownTool(t *testing.T) {

	a := newTestAgent(t)
	a.memoryTool = newMemoryToolState()
	a.SetMemoryToolEnabled(false)
	res := a.executeToolInner(context.Background(), provider.ToolCallDelta{Name: memoryToolName, Arguments: json.RawMessage(`{"command":"view","path":"/memories"}`)})
	if !res.IsError {
		t.Fatalf("disabled memory intercept must yield IsError, got %+v", res)
	}
	if !strings.Contains(res.Content, "not enabled") {
		t.Fatalf("expected 'not enabled' error, got %q", res.Content)
	}
}
