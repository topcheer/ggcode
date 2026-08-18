package agent

// CHARACTERIZATION TEST for issue #730 (log output documents CURRENT buggy
// behavior - do not treat it as correct semantics). Comment and docstring
// MENTIONS of placeholder patterns (panic/raise/throw) fire the placeholder
// detector because substringLineMultiset has no comment stripping. 3 of 4
// cases emit warnings (Py comment, Py docstring, Go comment); the control
// case (real placeholder) also fires, as intended. When #730 is fixed,
// expect 0 warnings for the three mention cases and keep the control at 1.
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
		t.Logf("%s -> %d warning(s): %v", c.name, len(w), w)
	}
}
