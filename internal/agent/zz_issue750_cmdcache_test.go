package agent

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/tool"
)

// Regression guard for #750: a successful shell source mutation (gofmt -w)
// between two identical test commands must NOT serve the stale cached result.
// Pre-fix, the second get() hit the entry (same key, same editGen, TTL fresh)
// and returned the old FAIL/PASS as if sources were unchanged.
func TestCommandCache_ShellMutationInvalidatesEntries(t *testing.T) {
	cc := newCommandCache()

	testArgs, _ := json.Marshal(map[string]string{"command": "go test ./pkg/"})
	fmtArgs, _ := json.Marshal(map[string]string{"command": "gofmt -w ./pkg/"})
	cmd, wd := parseRunCommandArgs(testArgs)
	if cmd == "" || wd != "" {
		t.Fatalf("parseRunCommandArgs: cmd=%q wd=%q", cmd, wd)
	}

	// Step 1: cache a failing test result (editGen G).
	entry := &commandCacheEntry{
		result:   tool.Result{Content: "FAIL: TestX"},
		editGen:  cc.editGen,
		storedAt: time.Now(),
	}
	key := cmdCacheKey(cmd, wd)
	cc.mu.Lock()
	cc.entries[key] = entry
	genBefore := cc.editGen
	cc.mu.Unlock()

	// Step 2: shell mutation must bump editGen and drop entries.
	fmtCmd, _ := parseRunCommandArgs(fmtArgs)
	if !shellMutatesSources(fmtCmd) {
		t.Fatal("gofmt -w must be recognized as a source mutation")
	}
	cc.invalidate()
	cc.mu.Lock()
	genAfter := cc.editGen
	liveEntries := len(cc.entries)
	cc.mu.Unlock()
	if genAfter <= genBefore {
		t.Fatalf("editGen must advance on invalidate: %d -> %d", genBefore, genAfter)
	}
	if liveEntries != 0 {
		t.Fatalf("entries must be dropped on invalidate, got %d", liveEntries)
	}

	// Step 3: the guard predicate wiring -- run_command with mutating args
	// must be classified as cache-invalidating (mirrors agent.go #750 branch).
	mutCmd, _ := parseRunCommandArgs(fmtArgs)
	plainArgs, _ := json.Marshal(map[string]string{"command": "go test ./pkg/"})
	plainCmd, _ := parseRunCommandArgs(plainArgs)
	if !shellMutatesSources(mutCmd) {
		t.Error("run_command(gofmt -w ...) must trigger invalidation")
	}
	if shellMutatesSources(plainCmd) {
		t.Error("run_command(go test ...) must not trigger invalidation")
	}
}
