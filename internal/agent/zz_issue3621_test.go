package agent

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
		"HTTPS":         "https",
	}
	for in, want := range cases {
		if got := camelToSnakeLower(in); got != want {
			t.Errorf("camelToSnakeLower(%q) = %q, want %q (regression)", in, got, want)
		}
	}
}
