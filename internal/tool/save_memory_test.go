package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/topcheer/ggcode/internal/memory"
)

func TestSaveMemoryTool(t *testing.T) {
	am := memory.NewAutoMemory()
	defer os.RemoveAll(am.Dir())

	tol := NewSaveMemoryTool(am)

	input, _ := json.Marshal(map[string]string{
		"key":     "test-pattern",
		"content": "Always use errors.Is()",
	})

	result, err := tol.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}

	// Verify file was created
	data, err := os.ReadFile(filepath.Join(am.Dir(), "test-pattern.md"))
	if err != nil {
		t.Fatalf("file not found: %v", err)
	}
	if string(data) != "Always use errors.Is()" {
		t.Errorf("wrong content: %q", string(data))
	}
}
