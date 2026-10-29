package tool

import (
	"encoding/json"
	"testing"
)

// #4376 regression: string-encoded numbers must not lose precision on a
// float64 round-trip.
//
// Path 1 (coerceNumber): "9007199254740993" (2^53+1) previously came back
// as 9007199254740992 via ParseFloat+FormatFloat.
//
// Path 2 (coerceInteger float-tolerance branch): "9007199254740993.0"
// previously reached int64(f) with the same off-by-one.

func coerceNum(t *testing.T, s string) json.RawMessage {
	t.Helper()
	out, ok := coerceNumber(json.RawMessage(`"` + s + `"`))
	if !ok {
		t.Fatalf("coerceNumber(%q) unexpectedly rejected", s)
	}
	return out
}

func TestIssue4376CoerceNumberKeepsLargeIntegerExact(t *testing.T) {
	got := coerceNum(t, "9007199254740993")
	if string(got) != "9007199254740993" {
		t.Fatalf("coerceNumber precision loss: got %s, want 9007199254740993", got)
	}
	// Negative twin.
	got = coerceNum(t, "-9007199254740993")
	if string(got) != "-9007199254740993" {
		t.Fatalf("coerceNumber precision loss (neg): got %s", got)
	}
	// Genuine floats still take the float path unchanged.
	got = coerceNum(t, "3.14")
	if string(got) != "3.14" {
		t.Fatalf("float path regressed: got %s, want 3.14", got)
	}
}

func TestIssue4376CoerceIntegerRejectsAmbiguousHugeFloatSpelling(t *testing.T) {
	// Exact large integer string still works (ParseInt fast path).
	out, ok := coerceInteger(json.RawMessage(`"9007199254740993"`))
	if !ok || string(out) != "9007199254740993" {
		t.Fatalf("integer fast path broke: ok=%v out=%s", ok, out)
	}
	// A FLOAT spelling of a >2^53 integer is inherently ambiguous in
	// float64: reject (type error) instead of silently converting to the
	// neighbouring value.
	out, ok = coerceInteger(json.RawMessage(`"9007199254740993.0"`))
	if ok {
		t.Fatalf("coerceInteger accepted ambiguous >2^53 float spelling: %s", out)
	}
	// In-range integral float spelling still coerces.
	out, ok = coerceInteger(json.RawMessage(`"42.0"`))
	if !ok || string(out) != "42" {
		t.Fatalf("integral float tolerance broke: ok=%v out=%s", ok, out)
	}
}
