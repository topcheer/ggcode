package agent

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

func issue2813Validator(t *testing.T) *toolSequenceValidator {
	t.Helper()
	v := newToolSequenceValidator()
	if v == nil {
		t.Fatal("validator constructor unavailable")
	}
	return v
}

func TestIssue2813EditBetweenReadAndGrepExempt(t *testing.T) {
	v := issue2813Validator(t)
	v.record(provider.ToolCallDelta{Name: "read_file", Arguments: []byte(`{"path":"/a.go"}`)}, 1)
	v.record(provider.ToolCallDelta{Name: "edit_file", Arguments: []byte(`{"file_path":"/a.go","old_text":"x","new_text":"y"}`)}, 2)
	g := v.record(provider.ToolCallDelta{Name: "grep", Arguments: []byte(`{"pattern":"foo","path":"/a.go"}`)}, 3)
	if strings.Contains(g, "already read") {
		t.Errorf("edit-then-grep verify flow still warned: %q (#2813 recurrence)", g)
	}
}

func TestIssue2813RangedReadExempt(t *testing.T) {
	v := issue2813Validator(t)
	v.record(provider.ToolCallDelta{Name: "read_file", Arguments: []byte(`{"path":"/big.go","offset":100,"limit":50}`)}, 1)
	g := v.record(provider.ToolCallDelta{Name: "grep", Arguments: []byte(`{"pattern":"foo","path":"/big.go"}`)}, 2)
	if strings.Contains(g, "already read") {
		t.Errorf("ranged-read then grep still warned: %q", g)
	}
}

func TestIssue2813FullReadStillWarns(t *testing.T) {
	v := issue2813Validator(t)
	v.record(provider.ToolCallDelta{Name: "read_file", Arguments: []byte(`{"path":"/a.go"}`)}, 1)
	g := v.record(provider.ToolCallDelta{Name: "grep", Arguments: []byte(`{"pattern":"foo","path":"/a.go"}`)}, 2)
	if !strings.Contains(g, "already read") {
		t.Error("full-read then grep should still warn (guidance lost)")
	}
}

func TestIssue2813UnrelatedEditDoesNotExempt(t *testing.T) {
	v := issue2813Validator(t)
	v.record(provider.ToolCallDelta{Name: "read_file", Arguments: []byte(`{"path":"/a.go"}`)}, 1)
	v.record(provider.ToolCallDelta{Name: "edit_file", Arguments: []byte(`{"file_path":"/other.go","old_text":"x","new_text":"y"}`)}, 2)
	g := v.record(provider.ToolCallDelta{Name: "grep", Arguments: []byte(`{"pattern":"foo","path":"/a.go"}`)}, 3)
	if !strings.Contains(g, "already read") {
		t.Error("unrelated edit must not exempt the read-then-grep warning")
	}
}
