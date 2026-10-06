package agent

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

// zz_issue2813_test.go guards against the read-then-grep false-positive
// (#2813): Pattern 4 warned unconditionally, including after edits to the
// file (verify-phase re-grounding) and after ranged reads that never had the
// full content.

func issue2813Validator(t *testing.T) *toolSequenceValidator {
	t.Helper()
	v := newToolSequenceValidator()
	if v == nil {
		t.Fatal("validator constructor unavailable")
	}
	return v
}

// TestIssue2813EditBetweenReadAndGrepExempt: read → edit → grep must NOT
// warn (the "you already have the content" premise is stale).
func TestIssue2813EditBetweenReadAndGrepExempt(t *testing.T) {
	v := issue2813Validator(t)
	v.record(provider.ToolCallDelta{Name: "read_file", Arguments: []byte(`{"path":"/a.go"}`)}, 1)
	v.record(provider.ToolCallDelta{Name: "edit_file", Arguments: []byte(`{"file_path":"/a.go","old_text":"x","new_text":"y"}`)}, 2)
	g := v.record(provider.ToolCallDelta{Name: "grep", Arguments: []byte(`{"pattern":"foo","path":"/a.go"}`)}, 3)
	if strings.Contains(g, "already read") {
		t.Errorf("edit-then-grep verify flow still warned: %q (#2813 recurrence)", g)
	}
}

// TestIssue2813RangedReadExempt: a read with offset (or limit) never held
// the full content - grepping to locate a section must not warn.
func TestIssue2813RangedReadExempt(t *testing.T) {
	v := issue2813Validator(t)
	v.record(provider.ToolCallDelta{Name: "read_file", Arguments: []byte(`{"path":"/big.go","offset":100,"limit":50}`)}, 1)
	g := v.record(provider.ToolCallDelta{Name: "grep", Arguments: []byte(`{"pattern":"foo","path":"/big.go"}`)}, 2)
	if strings.Contains(g, "already read") {
		t.Errorf("ranged-read then grep still warned: %q", g)
	}
}

// TestIssue2813FullReadStillWarns: the legitimate case (default full read,
// no edits) keeps the guidance.
func TestIssue2813FullReadStillWarns(t *testing.T) {
	v := issue2813Validator(t)
	v.record(provider.ToolCallDelta{Name: "read_file", Arguments: []byte(`{"path":"/a.go"}`)}, 1)
	g := v.record(provider.ToolCallDelta{Name: "grep", Arguments: []byte(`{"pattern":"foo","path":"/a.go"}`)}, 2)
	if !strings.Contains(g, "already read") {
		t.Error("full-read then grep should still warn (guidance lost)")
	}
}

// TestIssue2813UnrelatedEditDoesNotExempt: an edit to a DIFFERENT file does
// not invalidate the read of X.
func TestIssue2813UnrelatedEditDoesNotExempt(t *testing.T) {
	v := issue2813Validator(t)
	v.record(provider.ToolCallDelta{Name: "read_file", Arguments: []byte(`{"path":"/a.go"}`)}, 1)
	v.record(provider.ToolCallDelta{Name: "edit_file", Arguments: []byte(`{"file_path":"/other.go","old_text":"x","new_text":"y"}`)}, 2)
	g := v.record(provider.ToolCallDelta{Name: "grep", Arguments: []byte(`{"pattern":"foo","path":"/a.go"}`)}, 3)
	if !strings.Contains(g, "already read") {
		t.Error("unrelated edit must not exempt the read-then-grep warning")
	}
}
