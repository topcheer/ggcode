package agent

import "testing"

// #3050: the repeated-sequence (SOP) fingerprint must compare target file
// paths, not just tool names. A normal cross-file read -> edit -> verify
// loop (three DIFFERENT files) must not fire the consolidation suggestion.
func TestIssue3050_CrossFileLoopDoesNotFire(t *testing.T) {
	v := newToolSequenceValidator()
	files := []string{"/w/a.go", "/w/b.go", "/w/c.go"}
	for _, f := range files {
		for _, tc := range []struct {
			tool string
			args map[string]interface{}
		}{
			{"read_file", map[string]interface{}{"path": f}},
			{"edit_file", map[string]interface{}{"file_path": f, "old_text": "x", "new_text": "y"}},
			{"run_command", map[string]interface{}{"command": "go test ./..."}},
		} {
			if g := v.record(mkToolCall(tc.tool, tc.args), 1); g != "" {
				t.Fatalf("cross-file loop must not fire SOP suggestion, got: %q", g)
			}
		}
	}
	if v.hintsGiven["repeated_sequence"] {
		t.Fatal("repeated_sequence hint must stay unset for cross-file loops")
	}
}

// #3050 control: the SAME tool sequence against the SAME targets three
// times is a genuine hand-re-issued SOP and must still fire.
func TestIssue3050_SameTargetLoopStillFires(t *testing.T) {
	v := newToolSequenceValidator()
	fired := false
	for round := 0; round < 3; round++ {
		seq := []struct {
			tool string
			args map[string]interface{}
		}{
			{"read_file", map[string]interface{}{"path": "/w/a.go"}},
			{"edit_file", map[string]interface{}{"file_path": "/w/a.go", "old_text": "x", "new_text": "y"}},
			{"run_command", map[string]interface{}{"command": "go test ./internal/w/"}},
		}
		for _, tc := range seq {
			if g := v.record(mkToolCall(tc.tool, tc.args), 1); g != "" {
				if !v.hintsGiven["repeated_sequence"] {
					t.Fatalf("unexpected non-SOP guidance: %q", g)
				}
				fired = true
			}
		}
	}
	if !fired || !v.hintsGiven["repeated_sequence"] {
		t.Fatal("same-target sequence repeated 3x must fire the SOP suggestion")
	}
}
