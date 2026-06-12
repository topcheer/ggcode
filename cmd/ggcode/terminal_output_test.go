package main

import "testing"

func TestNormalizeTTYLineEndings(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "plain lf", in: "a\nb\n", want: "a\r\nb\r\n"},
		{name: "existing crlf", in: "a\r\nb\r\n", want: "a\r\nb\r\n"},
		{name: "mixed", in: "a\nb\r\nc\n", want: "a\r\nb\r\nc\r\n"},
	}

	for _, tt := range tests {
		if got := normalizeTTYLineEndings(tt.in); got != tt.want {
			t.Fatalf("%s: got %q want %q", tt.name, got, tt.want)
		}
	}
}
