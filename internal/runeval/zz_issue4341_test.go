package runeval

import "testing"

// #4341: canonicalJSON must keep integers > 2^53 distinct. Decoding via
// float64 rounded adjacent large integers (9007199254740992/...93) into one
// canonical string, colliding distinct calls into a single callKey and
// inflating /runreport duplicate-group and wasted-repeat counts.
func TestCanonicalJSONLargeIntegersStayDistinct(t *testing.T) {
	a := canonicalJSON([]byte(`{"id":9007199254740992}`))
	b := canonicalJSON([]byte(`{"id":9007199254740993}`))
	if a == b {
		t.Fatalf("adjacent >2^53 integers collided: both canonicalized to %s", a)
	}
	want := `{"id":9007199254740993}`
	if b != want {
		t.Fatalf("large integer not preserved verbatim: got %s want %s", b, want)
	}
}

// Regression guard: normalization still collapses key-order/whitespace-only
// differences into one key.
func TestCanonicalJSONStillNormalizesKeyOrder(t *testing.T) {
	a := canonicalJSON([]byte(`{"b":1,"a":2}`))
	b := canonicalJSON([]byte("{\n  \"a\" : 2,\n  \"b\":1\n}"))
	if a != b {
		t.Fatalf("key-order/whitespace variants diverged: %q vs %q", a, b)
	}
}

// Trailing garbage after the first JSON value must degrade to the raw string
// (json.Unmarshal rejected it; the UseNumber decoder must keep rejecting it).
func TestCanonicalJSONTrailingGarbageDegradesToRaw(t *testing.T) {
	raw := []byte(`{"a":1} oops`)
	if got := canonicalJSON(raw); got != string(raw) {
		t.Fatalf("trailing garbage canonicalized: got %q", got)
	}
}

// Floats and nested structures still canonicalize to sorted-key form.
func TestCanonicalJSONNestedAndFloats(t *testing.T) {
	a := canonicalJSON([]byte(`{"z":{"b":1.5,"a":[1,2]},"y":true}`))
	want := `{"y":true,"z":{"a":[1,2],"b":1.5}}`
	if a != want {
		t.Fatalf("nested canonical form mismatch: got %s want %s", a, want)
	}
}

func TestCanonicalJSONEmptyAndInvalid(t *testing.T) {
	if got := canonicalJSON(nil); got != "{}" {
		t.Fatalf("empty input: got %q want {}", got)
	}
	raw := []byte(`not json at all`)
	if got := canonicalJSON(raw); got != string(raw) {
		t.Fatalf("invalid input should degrade to raw: got %q", got)
	}
}
