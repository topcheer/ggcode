package tool

import (
	"encoding/json"
	"testing"
)

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

	got = coerceNum(t, "-9007199254740993")
	if string(got) != "-9007199254740993" {
		t.Fatalf("coerceNumber precision loss (neg): got %s", got)
	}

	got = coerceNum(t, "3.14")
	if string(got) != "3.14" {
		t.Fatalf("float path regressed: got %s, want 3.14", got)
	}
}

func TestIssue4376CoerceIntegerRejectsAmbiguousHugeFloatSpelling(t *testing.T) {

	out, ok := coerceInteger(json.RawMessage(`"9007199254740993"`))
	if !ok || string(out) != "9007199254740993" {
		t.Fatalf("integer fast path broke: ok=%v out=%s", ok, out)
	}

	out, ok = coerceInteger(json.RawMessage(`"9007199254740993.0"`))
	if ok {
		t.Fatalf("coerceInteger accepted ambiguous >2^53 float spelling: %s", out)
	}

	out, ok = coerceInteger(json.RawMessage(`"42.0"`))
	if !ok || string(out) != "42" {
		t.Fatalf("integral float tolerance broke: ok=%v out=%s", ok, out)
	}
}
