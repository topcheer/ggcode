package hooks

import "testing"

// #429: malformed tool patterns must not silently match everything.
func TestMatchToolSingleMalformedPattern(t *testing.T) {
	cases := []struct{ pattern string }{
		{"edit_file(x"},    // missing closing paren
		{"edit_file("},     // nothing after paren
		{"edit_file(path"}, // missing closing paren with args
	}
	for _, c := range cases {
		if matchToolSingle(c.pattern, "edit_file", `{"file_path":"/x"}`) {
			t.Errorf("malformed pattern %q must NOT match", c.pattern)
		}
	}
	// Well-formed forms still work.
	if !matchToolSingle("edit_file()", "edit_file", "anything") {
		t.Error("bare tool() form should match all")
	}
	if !matchToolSingle("edit_file(path*)", "edit_file", `{"file_path":"/x"}`) {
		t.Error("prefix form should match")
	}
}
