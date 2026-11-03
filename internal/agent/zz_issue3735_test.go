package agent

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
		`nextest(1)`,
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
		`xit("flaky")`,
		`it('works'); xit('flaky', ...)`,
		`xtest('math', () => {})`,
		`suite: xtest('a')`,
	}
	for _, s := range reals {
		if !containsAnySkipMarker(s) {
			t.Fatalf("real skip marker missed: %s", s)
		}
	}

	ag := NewAgent(nil, nil, "sys", 5)
	stats := &RunStats{}
	extractPathsFromToolCall("edit_file", json.RawMessage(
		`{"file_path":"a.spec.js","old_text":"it('flaky'","new_text":"xit('flaky'"}`), stats)
	if msg := ag.checkSpecGaming(stats, "fix the flaky spec"); msg == "" {
		t.Fatal("edit-injected xit( must still fire Pattern 2")
	}
}

func TestIssue3735_ContainsAnyLowersInput(t *testing.T) {

	if !containsAnySkipMarker(`T.SKIP("broken")`) {
		t.Fatal("uppercase t.SKIP( must be caught (case-insensitive promise)")
	}
	if !containsAnySkipMarker(`XIT("nope")`) {
		t.Fatal("uppercase standalone XIT( must be caught via boundary regexp")
	}
}
