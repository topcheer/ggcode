package agent

// #3735 probes: "xit("/"xtest(" as plain substrings hit the tail of every
// mainstream exit call (os.Exit / sys.exit / process.exit / C exit), firing
// a spec-gaming accusation on normal error handling and burning the
// fired-once budget. The boundary forms must catch real Jest/Mocha skips
// while letting exit calls through - on ALL three consumer paths
// (containsAnySkipMarker / hasSkipMarkersInCommands / hasSkipMarkersInEdits).

import (
	"encoding/json"
	"testing"
)

func TestIssue3735_ExitCallsNotFlagged(t *testing.T) {
	exits := []string{
		`if err != nil { fmt.Fprintln(os.Stderr, err); os.Exit(1) }`,
		`sys.exit(0)`,
		`process.exit(1)`,
		`exit(EXIT_FAILURE)`,
		`os.Exit(code)`,
		`nextest(1)`, // xtest( suffix collision, per the filer
	}
	for _, s := range exits {
		if containsAnySkipMarker(s) {
			t.Fatalf("exit-family call falsely flagged: %s", s)
		}
		if hasSkipMarkersInCommands([]string{s}) {
			t.Fatalf("exit-family command falsely flagged: %s", s)
		}
		if hasSkipMarkersInEdits([]string{s}) {
			t.Fatalf("exit-family edit falsely flagged: %s", s)
		}
	}
}

func TestIssue3735_RealSkipStillCaught(t *testing.T) {
	reals := []string{
		`xit("flaky")`,                   // standalone
		`it('works'); xit('flaky', ...)`, // after punctuation
		`xtest('math', () => {})`,        // standalone
		`suite: xtest('a')`,              // after colon-space
	}
	for _, s := range reals {
		if !containsAnySkipMarker(s) {
			t.Fatalf("real skip marker missed: %s", s)
		}
	}
	// Edit path end-to-end: injecting xit( via edit_file must fire.
	ag := NewAgent(nil, nil, "sys", 5)
	stats := &RunStats{}
	extractPathsFromToolCall("edit_file", json.RawMessage(
		`{"file_path":"a.spec.js","old_text":"it('flaky'","new_text":"xit('flaky'"}`), stats)
	if msg := ag.checkSpecGaming(stats, "fix the flaky spec"); msg == "" {
		t.Fatal("edit-injected xit( must still fire Pattern 2")
	}
}

func TestIssue3735_ContainsAnyLowersInput(t *testing.T) {
	// Doc comment promised case-insensitive; the old shape only lowered the
	// marker. Mixed-case real markers must hit.
	if !containsAnySkipMarker(`T.SKIP("broken")`) {
		t.Fatal("uppercase t.SKIP( must be caught (case-insensitive promise)")
	}
	if !containsAnySkipMarker(`XIT("nope")`) {
		t.Fatal("uppercase standalone XIT( must be caught via boundary regexp")
	}
}
