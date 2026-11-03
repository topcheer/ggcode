package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestGitTagDeleteRejectsLeadingDashName(t *testing.T) {
	tool := GitTag{WorkingDir: t.TempDir()}
	for _, name := range []string{"-d", "--delete", "-f"} {
		raw, _ := json.Marshal(map[string]interface{}{
			"action": "delete",
			"name":   name,
		})
		res, err := tool.Execute(context.Background(), json.RawMessage(raw))
		if err != nil {
			t.Fatalf("Execute(%q): %v", name, err)
		}
		if !res.IsError {
			t.Fatalf("dash-prefixed delete name %q must be rejected (was: fake success or silent no-op), content: %v", name, res.Content)
		}
		if !strings.Contains(string(res.Content), "leading dash") {
			t.Fatalf("error must name the reason for %q, got: %s", name, res.Content)
		}
	}
}
