package context

// #3726 probe: phase-2b (allFilesSuperseded) must require CONTENT coverage
// for the multi-file read's OTHER paths, mirroring phase-2 - not mere
// existence of a later read. A later PARTIAL re-read of one path must not
// mark the whole multi-file read superseded.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

func addToolPair3726(m *Manager, toolName, id, input, output string) {
	m.Add(provider.Message{
		Role: "assistant",
		Content: []provider.ContentBlock{{
			Type: "tool_use", ToolID: id, ToolName: toolName, Input: json.RawMessage(input),
		}},
	})
	m.Add(provider.Message{
		Role:    "user",
		Content: []provider.ContentBlock{provider.ToolResultBlock(id, output, false)},
	})
}

func TestIssue3726_PartialLaterReadDoesNotSupersedeMultiFile(t *testing.T) {
	m := NewManager(1000000)
	// t1: multi_file_read [A, B] full.
	addToolPair3726(m, "multi_file_read", "mf",
		`{"files":[{"path":"/a.go"},{"path":"/b.go"}]}`, strings.Repeat("M", 2000))
	// t2: full read of A (phase-2 trigger: mf's A path covered).
	addToolPair3726(m, "read_file", "ra", `{"path":"/a.go"}`, strings.Repeat("A", 800))
	// t3: PARTIAL read of B - a 200-line slice B's full read already
	// covered. Old phase-2b counted mere existence of this later read.
	addToolPair3726(m, "read_file", "rb", `{"path":"/b.go","offset":100,"limit":200}`, strings.Repeat("B", 800))

	freed := m.CompactSupersededReads()
	if freed != 0 {
		t.Fatalf("partial later read of one path must NOT supersede the whole multi-file read, freed=%d", freed)
	}
	for _, msg := range m.Messages() {
		for _, b := range msg.Content {
			if b.Type == "tool_result" && b.ToolID == "mf" && strings.HasPrefix(b.Output, "[superseded:") {
				t.Fatal("multi-file read must not be marked superseded")
			}
		}
	}
}

func TestIssue3726_FullLaterReadsStillSupersedeMultiFile(t *testing.T) {
	m := NewManager(1000000)
	addToolPair3726(m, "multi_file_read", "mf2",
		`{"files":[{"path":"/c.go"},{"path":"/d.go"}]}`, strings.Repeat("M", 2000))
	addToolPair3726(m, "read_file", "rc", `{"path":"/c.go"}`, strings.Repeat("C", 800))
	addToolPair3726(m, "read_file", "rd", `{"path":"/d.go"}`, strings.Repeat("D", 800))

	freed := m.CompactSupersededReads()
	if freed <= 0 {
		t.Fatal("full later reads of every path must still supersede the multi-file read")
	}
	superseded := false
	for _, msg := range m.Messages() {
		for _, b := range msg.Content {
			if b.Type == "tool_result" && b.ToolID == "mf2" && strings.HasPrefix(b.Output, "[superseded:") {
				superseded = true
			}
		}
	}
	if !superseded {
		t.Fatal("expected mf2 to be marked superseded")
	}
}
