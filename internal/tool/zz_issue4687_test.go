package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefineToolCallRejectsNullRequiredParam(t *testing.T) {
	dir := t.TempDir()
	gg := filepath.Join(dir, ".ggcode")
	if err := os.MkdirAll(gg, 0o755); err != nil {
		t.Fatal(err)
	}

	payload := `{"entries":{"find_q":{"name":"find_q","description":"find q","params_schema":{"type":"object","properties":{"q":{"type":"string"}},"required":["q"]},"steps":[{"tool":"grep","args":{"pattern":"{{q}}"}}]}}}`
	if err := os.WriteFile(filepath.Join(gg, "define_tools.json"), []byte(payload), 0o644); err != nil {
		t.Fatal(err)
	}

	dt := &DefineTool{WorkingDir: dir, Registry: NewRegistry()}

	out, err := dt.Execute(context.Background(), json.RawMessage(`{"action":"call","name":"find_q","params":{"q":null}}`))
	if err == nil && !out.IsError {
		t.Fatalf("null required param executed the tool instead of erroring: %+v", out)
	}
	if !strings.Contains(out.Content, `missing required param "q"`) {
		t.Fatalf("unexpected refusal text: %q", out.Content)
	}
}
