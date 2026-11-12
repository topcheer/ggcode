package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestExtractArgRationale pins #decision-audit capture semantics: present
// field extracted, absent/malformed yields empty, over-cap truncated.
func TestExtractArgRationale(t *testing.T) {
	cases := []struct {
		name string
		args string
		want string
	}{
		{"present", `{"pattern":"x","arg_rationale":"narrow read to preserve context"}`, "narrow read to preserve context"},
		{"absent", `{"pattern":"x"}`, ""},
		{"malformed json", `{"pattern":`, ""},
		{"wrong type", `{"arg_rationale":42}`, ""},
		{"empty", `{"arg_rationale":""}`, ""},
		{"whitespace trimmed", `{"arg_rationale":"  padded  "}`, "padded"},
	}
	for _, tc := range cases {
		if got := extractArgRationale(json.RawMessage(tc.args)); got != tc.want {
			t.Errorf("%s: want %q, got %q", tc.name, tc.want, got)
		}
	}
	long := strings.Repeat("r", 600)
	got := extractArgRationale(json.RawMessage(`{"arg_rationale":"` + long + `"}`))
	if runes := len([]rune(got)); runes != 500 {
		t.Errorf("cap: want 500 runes, got %d", runes)
	}
}
