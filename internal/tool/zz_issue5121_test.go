package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/memory"
)

// #5121: snapshot-style keys that deliberately overwrite the same key each
// round (r488 contract: intentional update with .history archival) must not
// emit the duplicate warning on every save. Near-duplicate DIFFERENT keys
// must keep firing.
func TestIssue5121SameKeySnapshotOverwriteNoDupWarning(t *testing.T) {
	withTestHome(t)
	am := memory.NewAutoMemory()
	defer os.RemoveAll(am.Dir())

	projectDir := createTestProjectDir(t)
	pm := memory.NewProjectAutoMemory(projectDir)
	if pm == nil {
		t.Fatal("expected non-nil project memory")
	}

	tol := NewSaveMemoryTool(am, pm)

	save := func(key, content string) string {
		t.Helper()
		input, _ := json.Marshal(map[string]string{"key": key, "content": content})
		result, err := tol.Execute(context.Background(), input)
		if err != nil {
			t.Fatalf("Execute(%q): %v", key, err)
		}
		if result.IsError {
			t.Fatalf("Execute(%q) unexpected error: %s", key, result.Content)
		}
		return result.Content
	}

	// Acceptance 1: same snapshot key saved N times with CHANGING content
	// -> zero duplicate warnings across all rounds.
	for i := 0; i < 4; i++ {
		out := save("pool-snapshot", fmt.Sprintf("round %d: entries=%d, drift=%d", i, 40+i, i))
		if strings.Contains(out, "Warning: memory") || strings.Contains(out, "duplicate") {
			t.Fatalf("round %d: same-key snapshot refresh leaked duplicate warning:\n%s", i, out)
		}
		// The overwrite must still be real: current file carries round i.
		data, err := os.ReadFile(filepath.Join(pm.Dir(), "pool-snapshot.md"))
		if err != nil {
			t.Fatalf("round %d: current file missing: %v", i, err)
		}
		if !strings.Contains(string(data), fmt.Sprintf("round %d", i)) {
			t.Fatalf("round %d: current file not refreshed: %q", i, string(data))
		}
	}

	// Optional-differentiator face from the issue: content-identical same-key
	// save (no-op rewrite) may still warn - pin that it DOES.
	out := save("pool-snapshot", "round 3: entries=43, drift=3")
	if !strings.Contains(out, "Warning: memory") {
		t.Errorf("no-op identical rewrite should keep the duplicate warning, got:\n%s", out)
	}

	// Acceptance 2: near-duplicate DIFFERENT key (3 tokens sharing 2 with the
	// existing key -> jaccard 0.67 >= 0.6 threshold) -> warning still fires.
	out = save("pool-snapshot-archive", "round 4: entries=44, drift=4")
	if !strings.Contains(out, "Warning: memory") || !strings.Contains(out, "very similar") {
		t.Errorf("near-duplicate different key must still warn, got:\n%s", out)
	}
}
