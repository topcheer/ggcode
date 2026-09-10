package tool

import (
	"encoding/json"
	"testing"
)

// #1692 case 3: query with tags filters (the parameter docs promise it).
// #1692 case 4: same-slug different-title adds disambiguate instead of
// silently overwriting.
func Test1692KnowledgeGraphTagsAndSlug(t *testing.T) {
	tt := &KnowledgeGraphTool{WorkingDir: t.TempDir()}

	// Two different titles, same slug "c-api".
	add := func(title, content string) string {
		p := &kgParams{Action: "add", Title: title, Content: content, Type: "note"}
		var raw json.RawMessage
		b, _ := json.Marshal(p)
		raw = b
		r, err := tt.Execute(nil, raw)
		_ = err
		return r.Content
	}
	add("C++ API", "first content")
	add("c api", "second content")

	s, err := tt.load()
	if err != nil {
		t.Fatal(err)
	}
	if s.Nodes["c-api"] == nil || s.Nodes["c-api"].Content != "first content" {
		t.Fatalf("original node must be preserved, got %+v", s.Nodes["c-api"])
	}
	if s.Nodes["c-api-2"] == nil || s.Nodes["c-api-2"].Content != "second content" {
		t.Fatalf("colliding node must be disambiguated, got %+v", s.Nodes["c-api-2"])
	}

	// Same title again: legitimate update path, still one node.
	add("C++ API", "updated")
	s2, _ := tt.load()
	if s2.Nodes["c-api-3"] != nil {
		t.Fatal("same-title re-add must update, not disambiguate")
	}
	if s2.Nodes["c-api"].Content != "updated" {
		t.Fatalf("same-title re-add must update in place, got %q", s2.Nodes["c-api"].Content)
	}

	// Tags filter: query with tags only returns nodes carrying them.
	tagged := &kgParams{Action: "add", Title: "tagged node", Content: "x", Type: "note", Tags: []string{"alpha"}}
	b2, _ := json.Marshal(tagged)
	tt.Execute(nil, b2)
	q := &kgParams{Action: "query", Query: "", Tags: []string{"alpha"}}
	b3, _ := json.Marshal(q)
	r, err := tt.Execute(nil, b3)
	if err != nil {
		t.Fatal(err)
	}
	if r.IsError || !contains(r.Content, "tagged node") {
		t.Fatalf("tags filter must return the tagged node, got %q", r.Content)
	}
	if contains(r.Content, "c api") || contains(r.Content, "C++ API") {
		t.Fatalf("tags filter must exclude untagged nodes, got %q", r.Content)
	}
}
