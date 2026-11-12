package swarm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestTeammateExpOutcomeNormalization pins the writer-side guard: unknown
// outcome strings coerce to failed, never silently to success (#P0
// fabricated-success fix).
func TestTeammateExpOutcomeNormalization(t *testing.T) {
	dir := t.TempDir()
	appendTeammateExperience(dir, "t", "tm-x", "", "did something", "banana")
	data, err := os.ReadFile(filepath.Join(dir, ".ggcode", "teammate-experience.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	line := strings.TrimSpace(string(data))
	if line == "" {
		t.Fatal("expected one entry")
	}
	if !strings.Contains(line, `"outcome":"failed"`) {
		t.Fatalf("unknown outcome must coerce to failed, got: %s", line)
	}
}
