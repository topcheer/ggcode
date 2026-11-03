package runeval

import (
	"encoding/json"
	"testing"
)

func TestIssue4341LargeIntegersStayDistinct(t *testing.T) {
	a := canonicalJSON(json.RawMessage(`{"id":9007199254740992}`))
	b := canonicalJSON(json.RawMessage(`{"id":9007199254740993}`))
	if a == b {
		t.Fatalf("distinct >2^53 integers must not canonicalize equal: %q == %q", a, b)
	}

	c := canonicalJSON(json.RawMessage(`{"items":[{"ts":9007199254740995}]}`))
	d := canonicalJSON(json.RawMessage(`{"items":[{"ts":9007199254740996}]}`))
	if c == d {
		t.Fatalf("nested distinct >2^53 integers must not canonicalize equal: %q == %q", c, d)
	}
}

func TestIssue4341CanonicalizationStillWorks(t *testing.T) {
	a := canonicalJSON(json.RawMessage(`{"b":2,"a":1}`))
	b := canonicalJSON(json.RawMessage(`{ "a" : 1 , "b" : 2 }`))
	if a != b {
		t.Fatalf("key-order/whitespace variants must canonicalize equal: %q != %q", a, b)
	}

	c := canonicalJSON(json.RawMessage(`{"n":42}`))
	d := canonicalJSON(json.RawMessage(`{"n":42}`))
	if c != d || c != `{"n":42}` {
		t.Fatalf("in-range integer canonical form broken: %q / %q", c, d)
	}
}

func TestIssue4341DegenerateInputs(t *testing.T) {
	if got := canonicalJSON(nil); got != "{}" {
		t.Fatalf("empty input must yield {}, got %q", got)
	}
	if got := canonicalJSON(json.RawMessage(`not-json`)); got != "not-json" {
		t.Fatalf("non-JSON input must degrade to raw string, got %q", got)
	}
}
