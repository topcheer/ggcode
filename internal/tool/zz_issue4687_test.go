package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Regression guard for #4687: a JSON null value for a required param passed
// the key-existence check, and the placeholder substitution later
// json.Marshal(nil)'d it into the literal string "null" - a grep step then
// silently searched for "null" instead of erroring. null must be treated as
// missing at the required-param gate.
func TestDefineToolCallRejectsNullRequiredParam(t *testing.T) {
	dir := t.TempDir()
	gg := filepath.Join(dir, ".ggcode")
	if err := os.MkdirAll(gg, 0o755); err != nil {
		t.Fatal(err)
	}
	// Hand-crafted store (same smuggling helper style as zz_issue4221):
	// required string param q feeding a grep pattern placeholder.
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
