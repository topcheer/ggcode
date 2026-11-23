package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestIssue1084_FileOpsRecursiveDeleteWarned(t *testing.T) {
	a := &Agent{destructiveGuard: newGitDestructiveState()}
	args, _ := json.Marshal(map[string]any{
		"operations": []any{map[string]any{"action": "delete", "source": "/tmp/x", "recursive": true}},
	})
	w := a.checkGitDestructive("file_ops", args)
	if !strings.Contains(w, "recursive delete") && !strings.Contains(w, "CRITICAL") {
		t.Fatalf("expected critical warning for recursive file_ops delete, got %q", w)
	}

	argsNR, _ := json.Marshal(map[string]any{
		"operations": []any{map[string]any{"action": "delete", "source": "/tmp/x"}},
	})
	if w := a.checkGitDestructive("file_ops", argsNR); w != "" {
		t.Fatalf("expected no warning for non-recursive delete, got %q", w)
	}

	argsMk, _ := json.Marshal(map[string]any{
		"operations": []any{map[string]any{"action": "mkdir", "source": "/tmp/y"}},
	})
	if w := a.checkGitDestructive("file_ops", argsMk); w != "" {
		t.Fatalf("expected no warning for mkdir, got %q", w)
	}
}
