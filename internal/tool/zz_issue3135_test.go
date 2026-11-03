package tool

import (
	"encoding/json"
	"testing"
)

func enums3135(vals ...string) []json.RawMessage {
	out := make([]json.RawMessage, 0, len(vals))
	for _, v := range vals {
		out = append(out, json.RawMessage(`"`+v+`"`))
	}
	return out
}

func TestIssue3135_OnNoFlipBlocked(t *testing.T) {
	if got := findBestEnumMatch("on", enums3135("no", "off")); got != "" {
		t.Fatalf("on -> %q silently flipped; want no correction", got)
	}
	if got := findBestEnumMatch("no", enums3135("on", "off")); got != "" {
		t.Fatalf("no -> %q silently flipped; want no correction", got)
	}
}

func TestIssue3135_ShortValueOnlyDistance1SameHead(t *testing.T) {

	if got := findBestEnumMatch("of", enums3135("off")); got != "off" {
		t.Fatalf("of -> %q; want off (legitimate typo)", got)
	}

	if got := findBestEnumMatch("of", enums3135("on", "off")); got != "" {
		t.Fatalf("of -> %q; want no correction (ambiguous)", got)
	}

	if got := findBestEnumMatch("ON", enums3135("on", "no")); got != "on" {
		t.Fatalf("ON -> %q; want on via case-insensitive exact match", got)
	}
}

func TestIssue3135_LongTypoStillCorrected(t *testing.T) {
	if got := findBestEnumMatch("overwite", enums3135("overwrite", "append", "fail")); got != "overwrite" {
		t.Fatalf("overwite -> %q; want overwrite (typo correction regressed)", got)
	}

	if got := findBestEnumMatch("dist-shanhai", enums3135("dist-shanghai", "dist-beijing")); got != "dist-shanghai" {
		t.Fatalf("dist-shanhai -> %q; want dist-shanghai", got)
	}
}

func TestIssue3135_MediumNeighborBeyondCapBlocked(t *testing.T) {

	if got := findBestEnumMatch("dist-sha", enums3135("dist-shanghai", "dist-beijing")); got != "" {
		t.Fatalf("dist-sha -> %q; want no correction (beyond cap)", got)
	}
}

func TestIssue3135_UniqueClosestStillCorrected(t *testing.T) {
	if got := findBestEnumMatch("disabl", enums3135("disabled", "disable")); got != "disable" {
		t.Fatalf("disabl -> %q; want disable (unique closest)", got)
	}
}

func TestIssue3135_ExitCodeFromWrappedErr(t *testing.T) {

	if got := exitCodeFromErr(errNonExit3135{}); got != -1 {
		t.Fatalf("non-ExitError should give -1, got %d", got)
	}
}

type errNonExit3135 struct{}

func (errNonExit3135) Error() string { return "not an exit error" }
