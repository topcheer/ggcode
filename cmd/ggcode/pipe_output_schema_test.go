package main

import "testing"

// pipeOutputIsValidJSON probes: raw JSON, markdown-fenced JSON (fallback
// path tolerance), prose rejection.
func TestPipeOutputIsValidJSON(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"raw object", `{"summary":"ok"}`, true},
		{"raw array", `[1,2,3]`, true},
		{"fenced", "Here you go:\n```json\n{\"summary\":\"ok\"}\n```\n", true},
		{"fenced no lang", "```\n{\"a\":1}\n```", true},
		{"prose", "The summary is that everything worked.", false},
		{"empty", "", false},
		{"broken fence", "```json\n{\"a\":", false},
	}
	for _, c := range cases {
		if got := pipeOutputIsValidJSON(c.in); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}
