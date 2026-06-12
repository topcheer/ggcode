//go:build !integration

package tunnel

import "testing"

func TestNormalizeReasoningChunk(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "empty", in: "", want: ""},
		{name: "whitespace", in: " \n\t ", want: ""},
		{name: "redacted", in: RedactedReasoningSentinel, want: RedactedReasoningPlaceholder},
		{name: "text", in: "plan step", want: "plan step"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NormalizeReasoningChunk(tt.in); got != tt.want {
				t.Fatalf("NormalizeReasoningChunk(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
