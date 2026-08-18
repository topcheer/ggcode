package agent

// REGRESSION TEST for issue #730 (fixed): comment and docstring MENTIONS
// of placeholder patterns (panic/raise/throw) must NOT fire the placeholder
// detector — substringLineMultiset comparison now runs on comment-stripped
// content (fix #730, same family as #723/#728). The three mention cases
// must emit 0 warnings; the control case (real placeholder) must stay at 1.
//
// Repro: go test -tags goolm -run 'TestR166PlaceholderCommentFP' -v ./internal/agent/

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
