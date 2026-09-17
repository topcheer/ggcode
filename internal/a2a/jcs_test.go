package a2a

import (
	"testing"
)

// Vectors from RFC 8785 Appendix B (number formatting per ECMAScript
// Number::toString).
func TestJCSNumbers(t *testing.T) {
	cases := []struct{ in, want string }{
		{`333333333.33333329`, `333333333.3333333`},
		{`1E30`, `1e+30`},
		{`4.50`, `4.5`},
		{`2e-3`, `0.002`},
		{`0.000000000000000000000000001`, `1e-27`},
		{`0.000000000000000000000000003`, `3e-27`},
		{`10000000000000000000000000000`, `1e+28`},
		{`1000000000000000000000`, `1e+21`},
		{`0`, `0`},
		{`-0`, `0`},
		{`0.5`, `0.5`},
		{`-1.5`, `-1.5`},
	}
	for _, c := range cases {
		got, err := jcsCanonicalize([]byte(c.in))
		if err != nil {
			t.Fatalf("canonicalize %s: %v", c.in, err)
		}
		if string(got) != c.want {
			t.Errorf("number %s: got %s, want %s", c.in, got, c.want)
		}
	}
}

func TestJCSStrings(t *testing.T) {
	cases := []struct{ name, raw, want string }{
		{"control escaped", `"\u000f"`, `"\u000f"`},
		{"newline normalized", `"\u000a"`, `"\n"`},
		{"non-ascii raw", `"€"`, `"€"`},
		{"quote escaped", `"a\"b"`, `"a\"b"`},
		{"backslash escaped", `"a\\b"`, `"a\\b"`},
		{"newline escape kept", `"a\nb"`, `"a\nb"`},
	}
	for _, c := range cases {
		got, err := jcsCanonicalize([]byte(c.raw))
		if err != nil {
			t.Fatalf("%s: canonicalize: %v", c.name, err)
		}
		if string(got) != c.want {
			t.Errorf("%s: got %s, want %s", c.name, got, c.want)
		}
	}
}

func TestJCSKeyOrder(t *testing.T) {
	// UTF-16 code-unit ordering (RFC 8785 §3.2.3): "" (0x00) < "1" (0x31)
	// < "A" (0x41) < "ö" (0xF6) < "€" (0x20AC).
	raw := "{\"€\":1,\"ö\":2,\"\":3,\"1\":4,\"A\":5}"
	got, err := jcsCanonicalize([]byte(raw))
	if err != nil {
		t.Fatalf("canonicalize: %v", err)
	}
	want := "{\"\":3,\"1\":4,\"A\":5,\"ö\":2,\"€\":1}"
	if string(got) != want {
		t.Errorf("key order: got %s, want %s", got, want)
	}
}

func TestJCSStructure(t *testing.T) {
	// Whitespace stripped, arrays keep order, nested objects sorted.
	raw := "{\n  \"b\": [ {\"z\": true, \"a\": null} ], \"a\": 1 }"
	got, err := jcsCanonicalize([]byte(raw))
	if err != nil {
		t.Fatalf("canonicalize: %v", err)
	}
	want := `{"a":1,"b":[{"a":null,"z":true}]}`
	if string(got) != want {
		t.Errorf("structure: got %s, want %s", got, want)
	}
}

func TestJCSInvalidInput(t *testing.T) {
	if _, err := jcsCanonicalize([]byte(`not json`)); err == nil {
		t.Error("expected error for invalid JSON")
	}
}
