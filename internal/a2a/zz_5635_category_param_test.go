package a2a

import (
	"encoding/json"
	"strings"
	"testing"
)

// #5635 take-ownership residual 1: the agent-facing schema must expose the
// EIGHTH face (#5645 plan face). The enum said "seven" long after plan
// landed - an agent could not discover or filter to it.
func TestIntrospectToolSchemaListsPlanFace(t *testing.T) {
	tt := NewIntrospectTool(nil, "", "")
	schema := string(tt.Parameters())
	if !strings.Contains(schema, `"plan"`) {
		t.Errorf("a2a_introspect schema faces enum must include \"plan\":\n%s", schema)
	}
	if strings.Contains(tt.Description(), "seven faces") {
		t.Errorf("tool description still says \"seven faces\":\n%s", tt.Description())
	}
	if !strings.Contains(tt.Description(), "plan") {
		t.Errorf("tool description must mention the plan face:\n%s", tt.Description())
	}
}

// #5635 user ruling (2): the formalized category pull parameter
// (mounts/state/content). ExpandCategoryFaces is the shared mapping.
func TestExpandCategoryFaces(t *testing.T) {
	cases := []struct {
		in   string
		want []string
		ok   bool
	}{
		{"", nil, true},
		{"mounts", []string{"env"}, true},
		{"state", []string{"status", "inflight", "children", "schedule", "plan"}, true},
		{"content", []string{"conversation", "activity"}, true},
		{"State", []string{"status", "inflight", "children", "schedule", "plan"}, true},
		{"bogus", nil, false},
	}
	for _, c := range cases {
		got, ok := ExpandCategoryFaces(c.in)
		if ok != c.ok {
			t.Errorf("ExpandCategoryFaces(%q) ok=%v, want %v", c.in, ok, c.ok)
		}
		if c.ok && strings.Join(got, ",") != strings.Join(c.want, ",") {
			t.Errorf("ExpandCategoryFaces(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// The JSON-RPC wire shape must carry category so the CLI/agent tool and
// server compose without drift.
func TestSnapshotRequestCategorySerialization(t *testing.T) {
	b, err := json.Marshal(SnapshotRequest{Category: "state"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"category":"state"`) {
		t.Errorf("request JSON missing category: %s", b)
	}
	s2 := `{"depth":"brief","category":"content"}`
	var req SnapshotRequest
	if err := json.Unmarshal([]byte(s2), &req); err != nil {
		t.Fatal(err)
	}
	if req.Category != "content" || req.Depth != DepthBrief {
		t.Errorf("decode: %+v", req)
	}
}
