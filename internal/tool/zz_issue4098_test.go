package tool

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestIssue4098SlugCollisionDisclosesOverwrite(t *testing.T) {
	tt := &KnowledgeGraphTool{WorkingDir: t.TempDir()}

	add := func(id, title string) string {
		p := &kgParams{Action: "add", ID: id, Title: title, Type: "note", Content: "x"}
		b, _ := json.Marshal(p)
		r, err := tt.Execute(nil, b)
		if err != nil {
			t.Fatalf("execute: %v", err)
		}
		return r.Content
	}

	add("c-api", "C++ API")

	out := add("c-api", "c api notes")
	if !strings.Contains(out, "already existed with title") {
		t.Fatalf("overwrite of a different-title node must disclose, got: %s", out)
	}

	out2 := add("c-api", "c api notes")
	if strings.Contains(out2, "already existed") {
		t.Fatalf("same-title refresh must not disclose, got: %s", out2)
	}
}
