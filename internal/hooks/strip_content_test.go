package hooks

import (
	"strings"
	"testing"
)

func TestStripContentValuesRecursive(t *testing.T) {
	in := `{"file_path":"/other/x","edits":[{"old_text":"a","new_text":"see internal/agent fix"}]}`
	out := stripContentValues(in)
	if strings.Contains(out, "internal/") {
		t.Fatalf("nested new_text must be stripped, got %s", out)
	}
	if strings.Contains(out, "old_text") {
		t.Fatalf("old_text is not a content-like field? check contentValueFields - got %s", out)
	}

	if !strings.Contains(out, "/other/x") {
		t.Fatalf("file_path must survive the strip, got %s", out)
	}

	in2 := `{"files":[{"path":"a.go","content":"internal/x"}]}`
	out2 := stripContentValues(in2)
	if strings.Contains(out2, "internal/") {
		t.Fatalf("nested files[].content must be stripped, got %s", out2)
	}
	if !strings.Contains(out2, "a.go") {
		t.Fatalf("files[].path must survive, got %s", out2)
	}
}
