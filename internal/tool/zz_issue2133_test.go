package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestGitTagCreateRejectsLeadingDashName(t *testing.T) {
	tool := GitTag{WorkingDir: t.TempDir()}
	raw, _ := json.Marshal(map[string]interface{}{
		"action": "create",
		"name":   "-d",
		"commit": "v1.0",
	})
	res, err := tool.Execute(context.Background(), json.RawMessage(raw))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError {
		t.Fatalf("dash-prefixed name must be rejected, got content: %v", res.Content)
	}
	if !strings.Contains(string(res.Content), "leading dash") {
		t.Fatalf("error must name the reason, got: %s", res.Content)
	}

	raw2, _ := json.Marshal(map[string]interface{}{
		"action": "create",
		"name":   "-f",
	})
	res2, err := tool.Execute(context.Background(), json.RawMessage(raw2))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res2.IsError {
		t.Fatal("-f name must be rejected (silent force-move shape)")
	}
}
