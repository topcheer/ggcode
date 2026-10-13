package tool

import (
	"encoding/json"
	"strings"
	"testing"
)

// #4098: doAdd's disclosure note compared against the post-mutation title
// (same pointer), so an explicit-id add overwriting a DIFFERENT-title node
// was completely silent. The disclosure must compare against the captured
// pre-update title.
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

	// Seed: node c-api / "C++ API" via explicit id.
	add("c-api", "C++ API")

	// Overwrite with a DIFFERENT title under the same explicit id.
	out := add("c-api", "c api notes")
	if !strings.Contains(out, "already existed with title") {
		t.Fatalf("overwrite of a different-title node must disclose, got: %s", out)
	}

	// Same-title refresh stays quiet.
	out2 := add("c-api", "c api notes")
	if strings.Contains(out2, "already existed") {
		t.Fatalf("same-title refresh must not disclose, got: %s", out2)
	}
}
