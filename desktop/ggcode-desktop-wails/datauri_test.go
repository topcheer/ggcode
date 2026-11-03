package main

import "testing"

func TestDataURIMIME(t *testing.T) {
	cases := []struct{ meta, want string }{
		{"image/jpeg;base64", "image/jpeg"},
		{"image/png", "image/png"},
		{"text/plain", "text/plain"},
		{";base64", ""},
		{"base64", ""},
		{"", ""},
		{"charset=utf-8", ""},
	}
	for _, tt := range cases {
		if got := dataURIMIME(tt.meta); got != tt.want {
			t.Errorf("dataURIMIME(%q) = %q, want %q", tt.meta, got, tt.want)
		}
	}
}
