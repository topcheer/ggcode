package tool

import (
	"encoding/json"
	"strconv"
	"testing"
)

// TestCoerceIntegerFloatOverflow verifies that float strings outside int64
// range are not coerced (converting them is undefined and silently produced
// garbage values downstream, #261).
func TestCoerceIntegerFloatOverflow(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantOK  bool
		wantVal string
	}{
		{name: "huge exponent float", input: `"1e300"`, wantOK: false},
		{name: "overflowing integer digits", input: `"99999999999999999999"`, wantOK: false},
		{name: "valid integer string", input: `"123"`, wantOK: true, wantVal: "123"},
		{name: "valid float-as-string", input: `"42.0"`, wantOK: true, wantVal: "42"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := coerceInteger(json.RawMessage(tt.input))
			if ok != tt.wantOK {
				t.Fatalf("coerceInteger(%s) ok = %v, want %v (result %s)", tt.input, ok, tt.wantOK, got)
			}
			if tt.wantOK && string(got) != tt.wantVal {
				t.Fatalf("coerceInteger(%s) = %s, want %s", tt.input, got, tt.wantVal)
			}
			if !tt.wantOK && string(got) != tt.input {
				t.Fatalf("coerceInteger(%s) should return input unchanged, got %s", tt.input, got)
			}
		})
	}
}

// TestCoerceIntegerNanAndBoundary verifies NaN and 2^63 are rejected (#296):
// math.MaxInt64 converts to float64 as exactly 2^63, so `>` let it through,
// and NaN compares false with everything.
func TestCoerceIntegerNanAndBoundary(t *testing.T) {
	cases := []struct {
		in   string
		want bool // whether coercion succeeds
	}{
		{"9223372036854775808", false}, // 2^63 exactly
		{"NaN", false},
		{"nan", false},
		{"-9223372036854775808", true}, // MinInt64 is a legal int64 (ParseInt path)
		{"123", true},
		{"42.0", true},
	}
	for _, c := range cases {
		got, ok := coerceInteger(json.RawMessage(strconv.Quote(c.in)))
		if ok != c.want {
			t.Errorf("coerceInteger(%q): ok=%v, want %v (got %s)", c.in, ok, c.want, got)
		}
	}
}
