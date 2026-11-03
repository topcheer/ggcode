package hooks

import "testing"

func TestMatchToolSingleMalformedPattern(t *testing.T) {
	cases := []struct{ pattern string }{
		{"edit_file(x"},
		{"edit_file("},
		{"edit_file(path"},
	}
	for _, c := range cases {
		if matchToolSingle(c.pattern, "edit_file", `{"file_path":"/x"}`) {
			t.Errorf("malformed pattern %q must NOT match", c.pattern)
		}
	}

	if !matchToolSingle("edit_file()", "edit_file", "anything") {
		t.Error("bare tool() form should match all")
	}
	if !matchToolSingle("edit_file(path*)", "edit_file", `{"file_path":"/x"}`) {
		t.Error("prefix form should match")
	}
}
