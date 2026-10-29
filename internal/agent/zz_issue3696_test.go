package agent

// #3696 probe: spec-gaming Pattern 2 must cover the EDIT path. The detector
// previously scanned only run_command/start_command inputs, so injecting a
// test skip marker via edit_file (the agent's primary edit tool) bypassed it
// entirely - exactly the bypass the detector's own header comment promised
// to cover ("in commands and file edits").
//
// Pin A (bypass closes): edit_file adding t.Skip + a paired source-file
// edit (Pattern 1 quiet) fires Pattern 2.
// Pin B (no FP): marker-free edits never fire.
// Pin C (wiring): extractPathsFromToolCall records added text from
// edit_file/multi_file_edit/write_file/notebook_edit into EditContents.
// Pin D (old_text exempt): removal-side text is not recorded.

import (
	"encoding/json"
	"strings"
	"testing"
)

func issue3696Stats() *RunStats { return &RunStats{} }

func TestIssue3696_EditInjectedSkipMarkerDetected(t *testing.T) {
	ag := NewAgent(nil, nil, "sys", 5)
	s := issue3696Stats()
	// The issue's scenario A: paired edits, test file gets the skip marker.
	extractPathsFromToolCall("edit_file", json.RawMessage(
		`{"file_path":"parser_test.go","old_text":"func TestParse(t *testing.T) {","new_text":"func TestParse(t *testing.T) { t.Skip(\"flaky\")"}`), s)
	extractPathsFromToolCall("edit_file", json.RawMessage(
		`{"file_path":"parser.go","old_text":"a","new_text":"b"}`), s)

	if msg := ag.checkSpecGaming(s, "fix the failing test"); msg == "" {
		t.Fatal("edit-injected t.Skip not detected (Pattern 2 edit path dead)")
	} else if !strings.Contains(msg, "skip") && !strings.Contains(msg, "Skip") {
		t.Fatalf("wrong warning fired: %s", msg)
	}
}

func TestIssue3696_CleanEditsNoWarning(t *testing.T) {
	ag := NewAgent(nil, nil, "sys", 5)
	s := issue3696Stats()
	extractPathsFromToolCall("edit_file", json.RawMessage(
		`{"file_path":"parser.go","old_text":"a","new_text":"return nil, fmt.Errorf(\"boom: %w\", err)"}`), s)
	extractPathsFromToolCall("edit_file", json.RawMessage(
		`{"file_path":"parser.go","old_text":"b","new_text":"c"}`), s)

	if msg := ag.checkSpecGaming(s, "fix the bug"); msg != "" {
		t.Fatalf("clean edit falsely flagged: %s", msg)
	}
}

func TestIssue3696_WiringCapturesAddedText(t *testing.T) {
	s := issue3696Stats()
	extractPathsFromToolCall("edit_file", json.RawMessage(`{"path":"a.go","new_text":"t.Skip(\"x\")"}`), s)
	extractPathsFromToolCall("write_file", json.RawMessage(`{"path":"b.go","content":"@pytest.mark.skip"}`), s)
	extractPathsFromToolCall("multi_file_edit", json.RawMessage(
		`{"files":[{"path":"c.go","new_text":"d"},"not-a-map",{"path":"d.go","content":"ok"}]}`), s)
	extractPathsFromToolCall("notebook_edit", json.RawMessage(
		`{"notebook_path":"n.ipynb","new_source":["x = 1","pytest.skip(\"why\")"]}`), s)
	extractPathsFromToolCall("run_command", json.RawMessage(`{"command":"true"}`), s)

	if len(s.EditContents) != 5 {
		t.Fatalf("EditContents = %d entries, want 5: %+q", len(s.EditContents), s.EditContents)
	}
	if !hasSkipMarkersInEdits(s.EditContents) {
		t.Fatal("wired EditContents must carry the markers for Pattern 2")
	}
}

func TestIssue3696_OldTextNotRecorded(t *testing.T) {
	s := issue3696Stats()
	// Removing a marker (remediation): old_text carries it, new_text clean.
	extractPathsFromToolCall("edit_file", json.RawMessage(
		`{"file_path":"p_test.go","old_text":"t.Skip(\"flaky\")","new_text":"// fixed properly"}`), s)
	// The clean replacement IS recorded (EditContents holds all added
	// text by design); the marker must not, because it lived in old_text.
	if len(s.EditContents) != 1 {
		t.Fatalf("want exactly the clean new_text recorded, got: %+q", s.EditContents)
	}
	if strings.Contains(s.EditContents[0], "Skip") {
		t.Fatalf("old_text marker leaked into EditContents: %+q", s.EditContents)
	}
	if hasSkipMarkersInEdits(s.EditContents) {
		t.Fatal("removal edit must not fire Pattern 2")
	}
}
