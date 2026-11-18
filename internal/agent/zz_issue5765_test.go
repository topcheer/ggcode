package agent

import "testing"

// #5765: "applied>=1" must fail when a batch_replace-style result reports
// zero applied changes (all targets skipped / no matches) even though
// IsError is false (batch_replace signals failure via FilesError>0 only).
func Test5765AppliedCheckZeroMatchesFails(t *testing.T) {
	skippedOnly := `{"files_changed":0,"total_matches":0,"files_skipped":2,"files_error":0,"summary":"2 files: 0 changed (0 total matches), 2 skipped, 0 errors"}`
	passed, detail := evaluateExpectCheck("applied>=1", skippedOnly, false)
	if passed {
		t.Fatalf("zero-applied result must fail applied>=1, got pass (detail=%q)", detail)
	}
}

func Test5765AppliedCheckWithMatchesPasses(t *testing.T) {
	changed := `{"files_changed":2,"total_matches":3,"files_skipped":0,"files_error":0}`
	if passed, _ := evaluateExpectCheck("applied>=1", changed, false); !passed {
		t.Fatal("result with applied changes must pass applied>=1")
	}
}

func Test5765AppliedCheckNonJSONFallsBack(t *testing.T) {
	// Non-JSON content: parseAppliedCount returns ok=false -> falls back to
	// the error-bit-only verdict (isError=false -> pass, pre-5765 behavior).
	if passed, _ := evaluateExpectCheck("applied>=1", "plain text result", false); !passed {
		t.Fatal("non-JSON content must fall back to error-bit-only verdict")
	}
}

func Test5765AppliedCheckErrorStillFails(t *testing.T) {
	if passed, _ := evaluateExpectCheck("applied>=1", "{}", true); passed {
		t.Fatal("error result must still fail applied>=1")
	}
}
