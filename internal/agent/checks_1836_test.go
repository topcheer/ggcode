package agent

import (
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

// #1836 case 3 pin: todo_write is NOT read-only - a todo_write + reads
// streak must not recommend low effort.
func Test1836TodoWriteNotReadOnly(t *testing.T) {
	if effortReadOnlyTools["todo_write"] {
		t.Fatal("todo_write must not be in the read-only list (planning turns need effort)")
	}
}

// #1836 case 2 pin: param-format edit failures don't count as recovery
// signals; genuine edit failures do.
func Test1836ParamFormatEditFailure(t *testing.T) {
	if !isParamFormatEditFailure("edit_file", "error: old_text not found in file") {
		t.Fatal("old_text-not-found must classify as param-format (no high-effort bump)")
	}
	if isParamFormatEditFailure("edit_file", "error: disk full while writing") {
		t.Fatal("genuine edit failure must still count as a recovery signal")
	}
	if isParamFormatEditFailure("grep", "error: not found") {
		t.Fatal("non-edit tools are out of scope for this classification")
	}
}

// #1836 case 1 pin: an override constructor honors provider-set effort.
func Test1836ConfigOverrideDetected(t *testing.T) {
	s := newAdaptiveEffortStateDetectOverride(fakeEffortProvider{effort: "high"})
	if !s.hasUserOverride() {
		t.Fatal("provider-level effort (config path) must mark the override")
	}
	s2 := newAdaptiveEffortStateDetectOverride(fakeEffortProvider{effort: ""})
	if s2.hasUserOverride() {
		t.Fatal("empty provider effort must leave the adapter active")
	}
}

type fakeEffortProvider struct {
	provider.Provider
	effort string
}

func (f fakeEffortProvider) SetReasoningEffort(e string) {}
func (f fakeEffortProvider) ReasoningEffort() string     { return f.effort }
