package tool

import (
	"encoding/json"
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
