package main

import "testing"

// #426: data URI MIME must be parsed from the URI itself and the ;base64
// marker respected — non-base64 (URL-encoded) URIs must be transcoded, not
// shipped as raw percent-encoded text labeled base64.
func TestDataURIMIME(t *testing.T) {
	cases := []struct{ meta, want string }{
		{"image/jpeg;base64", "image/jpeg"},
		{"image/png", "image/png"},
		{"text/plain", "text/plain"},
		{";base64", ""},       // no MIME declared
		{"base64", ""},        // legacy marker without MIME
		{"", ""},              // empty meta
		{"charset=utf-8", ""}, // parameter only, no type — not a MIME
	}
	for _, tt := range cases {
		if got := dataURIMIME(tt.meta); got != tt.want {
			t.Errorf("dataURIMIME(%q) = %q, want %q", tt.meta, got, tt.want)
		}
	}
}
