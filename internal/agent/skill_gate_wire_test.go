package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/topcheer/ggcode/internal/commands"
)

// TestToolTouchActivatesGatedSkill verifies the agent-loop wiring contract:
// paths extracted from tool-call arguments (the same extractor the read-path
// recorder uses) must flip a path-gated skill from hidden to discoverable via
// commands.NoteTouchedPaths - the call added next to the expiredRead recorder
// in the tool-result processing path.
func TestToolTouchActivatesGatedSkill(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, ".ggcode", "skills", "wasm-tools")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: wasm-tools\ndescription: Build and inspect wasm modules\npaths:\n  - \"**/*.wasm\"\n---\n\nBody"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	commands.ResetSkillGateForTest()
	t.Cleanup(commands.ResetSkillGateForTest)

	m := commands.NewManager(dir)
	if names := m.SkillNames(); containsString(names, "wasm-tools") {
		t.Fatalf("gated skill must be hidden before touch, got %v", names)
	}

	// Simulate the agent loop: extract paths from a successful read_file
	// call's arguments, exactly as agent.go does next to expiredRead.
	args := json.RawMessage(`{"path": "build/plugin.wasm"}`)
	paths := extractFilePathsFromArgs(args, "read_file")
	if len(paths) == 0 {
		t.Fatal("extractFilePathsFromArgs returned no paths for read_file")
	}
	commands.NoteTouchedPaths(paths...)

	if names := m.SkillNames(); !containsString(names, "wasm-tools") {
		t.Fatalf("gated skill must be discoverable after tool touch, got %v", names)
	}
}

func containsString(names []string, want string) bool {
	for _, n := range names {
		if n == want {
			return true
		}
	}
	return false
}
