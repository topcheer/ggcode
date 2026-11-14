package main

import "testing"

// #5602: fallback tightening - only URL-shaped inputs take the
// direct-endpoint path; seat NAMES (even with underscores/roles) never do.
func TestLooksLikeEndpoint(t *testing.T) {
	for _, yes := range []string{"http://1.2.3.4:8080", "https://host.tld", "1.2.3.4:8080", "localhost:9700"} {
		if !looksLikeEndpoint(yes) {
			t.Errorf("%q should be endpoint-shaped", yes)
		}
	}
	for _, no := range []string{"dev_ggcode", "list", "dev_ggcode agent", "seat name with space", ""} {
		if looksLikeEndpoint(no) {
			t.Errorf("%q must NOT be endpoint-shaped (silent bogus-URL fallback regression)", no)
		}
	}
}
