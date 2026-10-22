package tool

import (
	"encoding/json"
	"strings"
	"testing"
)

// zz_issue4376_test.go pins the #4376 fix: string-encoded integer values must
// not round-trip through float64 anywhere in CoerceArguments, because the
// round-trip silently turns 2^53+1 into 2^53 (a value-off-by-one that stays
// schema-valid, so the tool executes with wrong data).

const bigOdd = "9007199254740993" // 2^53+1: not representable in float64

// coerceField runs CoerceArguments with a single-field schema and returns the
// coerced raw value for that field.
func coerceField(t *testing.T, fieldType, rawVal string) json.RawMessage {
	t.Helper()
	schema := json.RawMessage(`{"properties":{"v":{"type":"` + fieldType + `"}}}`)
	args := json.RawMessage(`{"v":` + rawVal + `}`)
	out := CoerceArguments(schema, args)
	var m map[string]json.RawMessage
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("coerced args no longer parse as JSON: %v\nraw: %s", err, out)
	}
	return m["v"]
}

func TestIssue4376NumberFieldBigOddIntegerStringStaysExact(t *testing.T) {
	got := coerceField(t, "number", `"`+bigOdd+`"`)
	if string(got) != bigOdd {
		t.Fatalf("number field: %s must coerce to exactly %s, got %s (float64 round-trip precision loss)",
			bigOdd, bigOdd, got)
	}
}

func TestIssue4376IntegerFieldFloatDotFormStaysExact(t *testing.T) {
	got := coerceField(t, "integer", `"`+bigOdd+`.0"`)
	if string(got) != bigOdd {
		t.Fatalf("integer field: %q must coerce to exactly %s, got %s", bigOdd+".0", bigOdd, got)
	}
}

func TestIssue4376IntegerFieldExponentZeroFormStaysExact(t *testing.T) {
	got := coerceField(t, "integer", `"`+bigOdd+`e0"`)
	if string(got) != bigOdd {
		t.Fatalf("integer field: %q must coerce to exactly %s, got %s", bigOdd+"e0", bigOdd, got)
	}
}

func TestIssue4376NegativeBigOddInteger(t *testing.T) {
	if got := exactDecimal("-" + bigOdd + ".0"); got != "-"+bigOdd {
		t.Fatalf("exactDecimal(-%s.0) = %q, want -%s", bigOdd, got, bigOdd)
	}
	got := coerceField(t, "number", `"-`+bigOdd+`"`)
	if string(got) != "-"+bigOdd {
		t.Fatalf("number field: -%s must coerce exactly, got %s", bigOdd, got)
	}
}

func TestIssue4376ExactDecimalUnit(t *testing.T) {
	cases := []struct{ in, want string }{
		{"42", "42"},        // plain (also handled by ParseInt upstream)
		{"42.0", "42"},      // dot-zeros
		{"42.000", "42"},    // multiple zeros
		{"1e3", "1000"},     // positive exponent
		{"1.5e1", "15"},     // exponent shifts the point
		{"1E3", "1000"},     // capital E
		{"+42.0", "42"},     // explicit plus
		{"-7.00e2", "-700"}, // negative + exponent
		{"0.0", "0"},        // zero
		{"3.14", ""},        // non-integral -> not exact
		{"1.5e1x", ""},      // garbage suffix
		{"1e-1", ""},        // sub-unit value
		{"", ""},            // empty
		{"abc", ""},         // not numeric
		{"9223372036854775807", "9223372036854775807"}, // MaxInt64 exact
		{"9223372036854775808", ""},                    // MaxInt64+1: out of int64 -> float fallback
		{"9223372036854775808.0", ""},                  // same, float-form
		{"1e300", ""},                                  // far out of int64 range
	}
	for _, c := range cases {
		if got := exactDecimal(c.in); got != c.want {
			t.Errorf("exactDecimal(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestIssue4376RegressionsPreserved(t *testing.T) {
	// number field with a genuine float still goes through float formatting.
	if got := coerceField(t, "number", `"3.14"`); string(got) != "3.14" {
		t.Fatalf(`number "3.14" should stay 3.14, got %s`, got)
	}
	// integer field with non-integral float is still rejected (unchanged value).
	if got := coerceField(t, "integer", `"3.7"`); string(got) != `"3.7"` {
		t.Fatalf(`integer "3.7" must stay uncoerced (type error downstream), got %s`, got)
	}
	// integer overflow guard (#261/#296) still rejects 1e300.
	if got := coerceField(t, "integer", `"1e300"`); string(got) != `"1e300"` {
		t.Fatalf(`integer "1e300" must stay uncoerced, got %s`, got)
	}
	// number "inf" guard (#3109) still rejects.
	if got := coerceField(t, "number", `"inf"`); string(got) != `"inf"` {
		t.Fatalf(`number "inf" must stay uncoerced, got %s`, got)
	}
	// already-numeric values pass through untouched.
	if got := coerceField(t, "number", bigOdd); string(got) != bigOdd {
		t.Fatalf("numeric %s must pass through unchanged, got %s", bigOdd, got)
	}
}

func TestIssue4376ArrayItemsBigOddInteger(t *testing.T) {
	// #568 recursion path must inherit the exact path too.
	schema := json.RawMessage(`{"properties":{"ids":{"type":"array","items":{"type":"number"}}}}`)
	args := json.RawMessage(`{"ids":["` + bigOdd + `","42"]}`)
	out := CoerceArguments(schema, args)
	if !strings.Contains(string(out), bigOdd) {
		t.Fatalf("array-of-number coercion lost %s: %s", bigOdd, out)
	}
	if strings.Contains(string(out), "9007199254740992") {
		t.Fatalf("array-of-number coercion silently rounded %s to 9007199254740992: %s", bigOdd, out)
	}
}
