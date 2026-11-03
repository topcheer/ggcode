package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/debug"
)

func TestIssue3128_SchemaHasNoDescriptionLabel(t *testing.T) {
	tool := CreateSkillTool{}
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
		Required   []string                   `json:"required"`
	}
	if err := json.Unmarshal(tool.Parameters(), &schema); err != nil {
		t.Fatalf("schema is not valid JSON: %v", err)
	}
	if _, ok := schema.Properties["description_label"]; ok {
		t.Error("schema still declares description_label property (#3128 V1)")
	}
	for _, r := range schema.Required {
		if r == "description_label" {
			t.Error("schema still lists description_label as required (#3128 V1)")
		}
	}

	for _, want := range []string{"name", "description", "content"} {
		found := false
		for _, r := range schema.Required {
			if r == want {
				found = true
			}
		}
		if !found {
			t.Errorf("required field %q missing from schema", want)
		}
	}
}

func TestIssue3128_SkillWriteAtomicNoTmpResidue(t *testing.T) {
	dir := t.TempDir()
	tool := CreateSkillTool{WorkingDir: dir}
	input := `{"name": "probe3128", "description": "probe skill", "content": "# body\nsome text\n"}`
	res, err := tool.Execute(context.Background(), json.RawMessage(input))
	if err != nil || res.IsError {
		t.Fatalf("create failed: err=%v res=%+v", err, res)
	}
	skillFile := filepath.Join(dir, ".ggcode", "skills", "probe3128", "SKILL.md")
	got, err := os.ReadFile(skillFile)
	if err != nil {
		t.Fatalf("skill file not on disk: %v", err)
	}
	if !strings.Contains(string(got), "some text") {
		t.Errorf("skill body truncated: %q", string(got))
	}

	entries, _ := os.ReadDir(filepath.Dir(skillFile))
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("temp residue left behind: %s", e.Name())
		}
	}
}

func TestIssue3128_DebugLogExportCountsComplete(t *testing.T) {
	for i := 0; i < 3; i++ {
		debug.Log("probe3128marker", "entry %d", i)
	}

	tool := DebugLogTool{}
	res, err := tool.Execute(context.Background(), json.RawMessage(`{"action": "export", "keyword": "probe3128marker", "lines": 2000}`))
	if err != nil {
		t.Fatalf("export failed: %v", err)
	}
	if res.IsError {
		t.Fatalf("export reported error on the happy path: %s", res.Content)
	}
	if !strings.Contains(res.Content, "Exported 3 log entries") {
		t.Errorf("expected exactly 3 exported entries, got: %s", res.Content)
	}

	pathLine := ""
	for _, line := range strings.Split(res.Content, "\n") {
		if strings.HasPrefix(line, "/") || strings.HasPrefix(line, os.TempDir()) {
			pathLine = strings.TrimSpace(line)
		}
	}
	if pathLine == "" {
		t.Fatalf("no export path in result: %s", res.Content)
	}
	b, err := os.ReadFile(pathLine)
	if err != nil {
		t.Fatalf("exported file unreadable: %v", err)
	}
	if got := strings.Count(string(b), "[probe3128marker]"); got < 3 {
		t.Errorf("exported file has %d probe entries, want >= 3 (ring buffer is process-wide; exact count asserted in the result message)", got)
	}
}
