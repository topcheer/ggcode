package tool

import (
	"context"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/memory"
)

// Issue #5121: snapshot-style keys that intentionally overwrite the same
// key each round must not emit a duplicate warning on every save (noise
// trains agents to ignore real near-duplicate warnings). Near-duplicate
// DIFFERENT keys must keep warning.
func TestIssue5121SaveMemorySameKeyNoDupWarning(t *testing.T) {
	dir := t.TempDir()
	am := memory.NewProjectAutoMemory(dir)
	tool := NewSaveMemoryTool(nil, am)

	for round := 1; round <= 3; round++ {
		res, err := tool.Execute(context.Background(), []byte(
			`{"key":"pool-snapshot","content":"round `+string(rune('0'+round))+` state: 3 seats busy"}`,
		))
		if err != nil || res.IsError {
			t.Fatalf("round %d save failed: err=%v res=%v", round, err, res.Content)
		}
		if strings.Contains(res.Content, "Warning: memory") {
			t.Errorf("round %d: same-key overwrite must not emit duplicate warning:\n%s", round, res.Content)
		}
	}
}

func TestIssue5121SaveMemoryNearKeyStillWarns(t *testing.T) {
	dir := t.TempDir()
	am := memory.NewProjectAutoMemory(dir)
	tool := NewSaveMemoryTool(nil, am)

	if _, err := tool.Execute(context.Background(), []byte(
		`{"key":"build-process","content":"go build ./..."}`,
	)); err != nil {
		t.Fatalf("seed save: %v", err)
	}

	res, err := tool.Execute(context.Background(), []byte(
		`{"key":"build-process-v2","content":"go build ./..."}`,
	))
	if err != nil || res.IsError {
		t.Fatalf("near-key save failed: err=%v res=%v", err, res.Content)
	}
	if !strings.Contains(res.Content, "Warning: memory") {
		t.Errorf("near-duplicate different key must still warn:\n%s", res.Content)
	}
}
