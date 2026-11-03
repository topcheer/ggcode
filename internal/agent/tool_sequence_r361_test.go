package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

func r361Call(name string) provider.ToolCallDelta {
	return provider.ToolCallDelta{Name: name, Arguments: json.RawMessage(`{}`)}
}

func TestRepeatedSequence_FiresOnThirdRepetition(t *testing.T) {
	v := newToolSequenceValidator()
	var guidance string

	sop := []string{"run_command", "edit_file", "run_command"}
	for rep := 0; rep < 3; rep++ {
		for _, tool := range sop {
			guidance = v.record(r361Call(tool), rep)
		}
	}
	if !strings.Contains(guidance, "repeated 3 times") ||
		!strings.Contains(guidance, "cmd_snippet") {
		t.Fatalf("third SOP repetition must suggest consolidation, got %q", guidance)
	}

	if g := v.record(r361Call("run_command"), 9); g != "" {
		t.Fatalf("must fire at most once per run, got %q", g)
	}
}

func TestRepeatedSequence_TwoRepetitionsSilent(t *testing.T) {
	v := newToolSequenceValidator()
	var guidance string
	sop := []string{"grep", "read_file", "edit_file"}
	for rep := 0; rep < 2; rep++ {
		for _, tool := range sop {
			guidance = v.record(r361Call(tool), rep)
		}
	}
	if strings.Contains(guidance, "repeated") {
		t.Fatalf("two repetitions must stay silent, got %q", guidance)
	}
}

func TestRepeatedSequence_SingleToolRepeatExcluded(t *testing.T) {
	v := newToolSequenceValidator()
	var guidance string
	for i := 0; i < 9; i++ {
		guidance = v.record(r361Call("grep"), i)
	}
	if strings.Contains(guidance, "repeated") {
		t.Fatalf("single-tool repeat must be excluded, got %q", guidance)
	}
}
