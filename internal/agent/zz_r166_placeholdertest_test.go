package agent

import "testing"

func TestR166PlaceholderCommentFP(t *testing.T) {
	cases := []struct {
		name string
		file string
		code string
	}{
		{"Py comment mentions raise", "app.py", "# for unsupported ops we raise NotImplementedError here\nvalue = 1\n"},
		{"Py docstring documents raise", "app.py", "def f():\n    \"\"\"Will raise NotImplementedError if unsupported.\"\"\"\n    return 1\n"},
		{"Go comment mentions panic", "main.go", "package main\n\n// legacy path used to panic(\"not implemented\") before v2\nfunc f() {}\n"},
		{"Control: real placeholder py", "app.py", "def f(x):\n    raise NotImplementedError\n"},
	}
	for _, c := range cases {
		w := checkPlaceholderCode(c.file, "", c.code)
		if c.name == "Control: real placeholder py" {
			if len(w) != 1 {
				t.Errorf("%s: expected exactly 1 warning, got %d: %v", c.name, len(w), w)
			}
			continue
		}
		if len(w) != 0 {
			t.Errorf("%s (issue #730 FP): expected 0 warnings, got %d: %v", c.name, len(w), w)
		}
	}
}
