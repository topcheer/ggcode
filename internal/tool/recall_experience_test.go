package tool

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/memory"
)

func TestRecallExperienceTool(t *testing.T) {
	dir := t.TempDir()
	store := memory.NewExperienceStore(filepath.Join(dir, ".ggcode", "memory", "experience"))
	if _, _, err := store.Record("Fix flaky login test",
		"Injected a clock into SessionValidator", "success", nil); err != nil {
		t.Fatalf("seed: %v", err)
	}
	tr := NewRecallExperienceTool(dir)

	t.Run("matching query returns distilled case", func(t *testing.T) {
		res, err := tr.Execute(context.Background(), json.RawMessage(`{"query":"flaky login test clock"}`))
		if err != nil || res.IsError {
			t.Fatalf("unexpected error: err=%v res=%q", err, res.Content)
		}
		if !strings.Contains(res.Content, "SessionValidator") {
			t.Fatalf("expected case detail, got %q", res.Content)
		}
	})

	t.Run("no match is not an error", func(t *testing.T) {
		res, _ := tr.Execute(context.Background(), json.RawMessage(`{"query":"quantum chromodynamics refactor"}`))
		if res.IsError || !strings.Contains(res.Content, "no relevant past experience") {
			t.Fatalf("expected friendly empty result, got %q", res.Content)
		}
	})

	t.Run("blank query refused", func(t *testing.T) {
		res, _ := tr.Execute(context.Background(), json.RawMessage(`{"query":"  "}`))
		if !res.IsError || !strings.Contains(res.Content, "query is required") {
			t.Fatalf("expected validation error, got %q", res.Content)
		}
	})

	t.Run("max out of range refused", func(t *testing.T) {
		res, _ := tr.Execute(context.Background(), json.RawMessage(`{"query":"x","max":9}`))
		if !res.IsError || !strings.Contains(res.Content, "between 1 and") {
			t.Fatalf("expected range error, got %q", res.Content)
		}
	})

	t.Run("HOME working dir disables store gracefully", func(t *testing.T) {
		// NewProjectExperienceStore returns nil when workingDir == HOME;
		// exercise the nil path via an empty workingDir (also nil).
		res, _ := NewRecallExperienceTool("").Execute(context.Background(), json.RawMessage(`{"query":"anything"}`))
		if res.IsError {
			t.Fatalf("disabled store must be a friendly no-op, got %q", res.Content)
		}
		if !strings.Contains(res.Content, "not available") {
			t.Fatalf("expected availability note, got %q", res.Content)
		}
	})
}
