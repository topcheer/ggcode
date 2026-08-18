package agent

// CHARACTERIZATION TEST for issue #728 (do not treat log output as correct
// semantics). Documents the CURRENT buggy behavior: Go/JS block-comment
// BODY lines (continuation lines starting with '*') survive the #723
// comment stripping and still fire the insecure-pattern detectors (3 of 4
// cases emit warnings; Python is clean - it has no block comments).
// When #728 is fixed, add assertions expecting 0 warnings for all Go/JS
// cases as the regression guard.
//
// Repro: go test -tags goolm -run 'TestR165BlockCommentBodyFP' -v ./internal/agent/

import "testing"

func TestR165BlockCommentBodyFP(t *testing.T) {
	cases := []struct {
		name string
		file string
		code string
	}{
		{"Go block body TLS", "main.go", "package main\n\n/*\n * InsecureSkipVerify: true must never be used\n */\nfunc f() {}\n"},
		{"JS block body eval", "app.js", "/*\n * eval(userInput) is dangerous here\n */\n"},
		{"JS block body innerHTML", "app.js", "/*\n * el.innerHTML = a + b is XSS\n */\n"},
		{"Py hash body md5", "app.py", "# comment\n# hashlib.md5(password) weak\n"},
	}
	for _, c := range cases {
		w := checkInsecurePatterns(c.file, "", c.code)
		t.Logf("%s -> %d warning(s): %v", c.name, len(w), w)
	}
}
