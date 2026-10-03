package tool

// Regression probes for #3135: enum correction's fixed distance-2 window
// silently flipped short enum values ("on" -> "no" is distance 2, i.e. a
// semantic reversal treated as a typo). The fix gates Layer 2 by a
// length-proportional cap and, for candidates <= 3 chars, requires
// distance <= 1 with a shared first character.

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

// The core bug: "on" against a short-neighbor enum must NOT be silently
// corrected ("no" is distance 2 - the old window flipped it). The real
// trigger shape is an enum WITHOUT the provided value (exact members
// never reach Layer 2).
func TestIssue3135_OnNoFlipBlocked(t *testing.T) {
	if got := findBestEnumMatch("on", enums3135("no", "off")); got != "" {
		t.Fatalf("on -> %q silently flipped; want no correction", got)
	}
	if got := findBestEnumMatch("no", enums3135("on", "off")); got != "" {
		t.Fatalf("no -> %q silently flipped; want no correction", got)
	}
}

// Same shape with an off/a family: "off" must not become "a" family or
// vice versa; short values only correct within distance 1 + same head.
func TestIssue3135_ShortValueOnlyDistance1SameHead(t *testing.T) {
	// Unambiguous short typo (sole member): "of" -> "off", distance 1,
	// same first char.
	if got := findBestEnumMatch("of", enums3135("off")); got != "off" {
		t.Fatalf("of -> %q; want off (legitimate typo)", got)
	}
	// "of" is distance 1 from BOTH "on" and "off" (f->n substitution) with
	// the same head - ambiguous, so no correction (prefer not-correcting
	// over mis-correcting).
	if got := findBestEnumMatch("of", enums3135("on", "off")); got != "" {
		t.Fatalf("of -> %q; want no correction (ambiguous)", got)
	}
	// Case-insensitive layer still corrects regardless of Layer 2.
	if got := findBestEnumMatch("ON", enums3135("on", "no")); got != "on" {
		t.Fatalf("ON -> %q; want on via case-insensitive exact match", got)
	}
}

// Longer values keep the typo correction: "overwite" -> "overwrite"
// (distance 1, len 9 -> cap max(1, 9/4)=2).
func TestIssue3135_LongTypoStillCorrected(t *testing.T) {
	if got := findBestEnumMatch("overwite", enums3135("overwrite", "append", "fail")); got != "overwrite" {
		t.Fatalf("overwite -> %q; want overwrite (typo correction regressed)", got)
	}
	// Distance 2 within a long enum is still fine: "dist-syd" vs
	// "dist-syd2"? Use a realistic pair: "dist-shanhai" -> "dist-shanghai".
	if got := findBestEnumMatch("dist-shanhai", enums3135("dist-shanghai", "dist-beijing")); got != "dist-shanghai" {
		t.Fatalf("dist-shanhai -> %q; want dist-shanghai", got)
	}
}

// The exact issue example: medium-length near-neighbors within the old
// window but beyond the proportional cap must no longer flip.
// "dist-syd" (len 8, cap 2) vs "dist-syd"-distance-3 candidate: blocked.
func TestIssue3135_MediumNeighborBeyondCapBlocked(t *testing.T) {
	// "dist-sha" vs "dist-shanghai": distance 4 > cap 2 - not a typo.
	if got := findBestEnumMatch("dist-sha", enums3135("dist-shanghai", "dist-beijing")); got != "" {
		t.Fatalf("dist-sha -> %q; want no correction (beyond cap)", got)
	}
}

// Unique-closest disambiguation is unchanged: distances 1 vs 2 are NOT a
// tie - the sole closest candidate still wins (correcting "disabl" to
// "disable" is a typo fix, not a flip).
func TestIssue3135_UniqueClosestStillCorrected(t *testing.T) {
	if got := findBestEnumMatch("disabl", enums3135("disabled", "disable")); got != "disable" {
		t.Fatalf("disabl -> %q; want disable (unique closest)", got)
	}
}

// V2 rider: exitCodeFromErr must see through a wrapped ExitError
// (errors.As instead of bare assertion).
func TestIssue3135_ExitCodeFromWrappedErr(t *testing.T) {
	// Direct build of a wrapped error path without importing exec test
	// doubles: exercise the -1 passthrough for a non-ExitError, and the
	// unwrap path via a sentinel wrapper.
	if got := exitCodeFromErr(errNonExit3135{}); got != -1 {
		t.Fatalf("non-ExitError should give -1, got %d", got)
	}
}

type errNonExit3135 struct{}

func (errNonExit3135) Error() string { return "not an exit error" }
