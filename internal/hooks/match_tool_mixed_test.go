package hooks

import "testing"

// #413: mixed-syntax match patterns (function-call + pipe alternatives)
// must fire instead of silently never matching.
func TestMatchToolMixedSyntax(t *testing.T) {
	cases := []struct {
		pattern, tool, input string
		want                 bool
	}{
		// Old code sliced patArgs="*)|bas" from "edit_file(*)|bash" —
		// Contains was always false → hook never fired (#413).
		{"edit_file(*)|bash", "edit_file", `{"file_path":"/a"}`, true},
		{"edit_file(*)|bash", "bash", `ls`, true},
		{"edit_file(*)|bash", "read_file", `x`, false},
		{"bash|edit_file(*)", "edit_file", `y`, true},
		{"edit_file(prefix*)|write_file", "edit_file", `{"content":"prefix-here"}`, true},
		{"edit_file(*prefix)|write_file", "write_file", `z`, true},
		// Pure forms keep working.
		{"edit_file(*)", "edit_file", `{}`, true},
		{"read_file|grep", "grep", `pattern`, true},
		{"read_file", "read_file", ``, true},
	}
	for _, tt := range cases {
		if got := matchTool(tt.pattern, tt.tool, tt.input); got != tt.want {
			t.Errorf("matchTool(%q, %q, %q) = %v, want %v", tt.pattern, tt.tool, tt.input, got, tt.want)
		}
	}
}

// #413: filterEnviron must drop inherited GGCODE_* keys so injected values
// are never shadowed by stale ones from a chained-hook environment.
func TestFilterEnviron(t *testing.T) {
	environ := []string{
		"PATH=/usr/bin",
		"GGCODE_HOOK_PAYLOAD=stale-from-parent",
		"GGCODE_TOOL_NAME=old-tool",
		"HOME=/root",
	}
	got := filterEnviron(environ, "GGCODE_HOOK_PAYLOAD", "GGCODE_TOOL_NAME")
	want := []string{"PATH=/usr/bin", "HOME=/root"}
	if len(got) != len(want) {
		t.Fatalf("filterEnviron returned %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("filterEnviron[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
