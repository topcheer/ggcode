package agent

// #3621 probe: camelToSnakeLower must split acronym runs at the standard
// boundary. "HTTPServer" produced the single token "httpserver" (boundary
// only before an uppercase that FOLLOWS a non-uppercase), which never
// matched conventional test names like "test_http_server" - exported
// functions with acronym-prefixed names were misreported untested.

import "testing"

func TestIssue3621_AcronymRunBoundary(t *testing.T) {
	cases := map[string]string{
		"HTTPServer":  "http_server",
		"XMLParser":   "xml_parser",
		"ParseJSON":   "parse_json",
		"ServeHTTP":   "serve_http",
		"HTTP2Server": "http2_server",
	}
	for in, want := range cases {
		if got := camelToSnakeLower(in); got != want {
			t.Errorf("camelToSnakeLower(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIssue3621_PlainCamelCaseUnchanged(t *testing.T) {
	cases := map[string]string{
		"GetUser":       "get_user",
		"lower":         "lower",
		"GetUserByName": "get_user_by_name",
		"HTTPS":         "https", // pure acronym run: no internal boundary
	}
	for in, want := range cases {
		if got := camelToSnakeLower(in); got != want {
			t.Errorf("camelToSnakeLower(%q) = %q, want %q (regression)", in, got, want)
		}
	}
}
