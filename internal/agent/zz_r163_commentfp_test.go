package agent

// CHARACTERIZATION TEST for issue #723 (do not treat assertions as correct
// semantics). It documents the CURRENT buggy behavior: 8 insecure-pattern
// checks fire on pure comment lines. When #723 is fixed, INVERT the
// assertions - every case below must then expect 0 warnings (the two
// control cases already assert 0 and must keep passing unchanged).
//
// Repro: go test -tags goolm -run 'TestR163Comment' -v ./internal/agent/
//
// R163 verification: comment-line false positives in checkInsecurePatterns.
// Fix #278 added comment handling ONLY to the SQL check; #274 ONLY to Python
// verify=False. These tests verify which sibling checks still fire on pure
// comments.

import "testing"

func TestR163CommentFPGo(t *testing.T) {
	cases := []struct {
		name string
		line string
	}{
		{"InsecureSkipVerify comment", "// InsecureSkipVerify: true should never be used in production"},
		{"md5 comment", "// md5.New() for password hashing is forbidden by policy"},
		{"exec.Command comment", "// exec.Command(\"sh\", \"-c\", user + input) is dangerous"},
	}
	for _, c := range cases {
		w := checkInsecurePatterns("main.go", "", "package main\n\nfunc f() {\n\t"+c.line+"\n}\n")
		t.Logf("%s -> %d warning(s): %v", c.name, len(w), w)
	}
}

func TestR163CommentFPJS(t *testing.T) {
	cases := []struct {
		name string
		line string
	}{
		{"eval comment", "// eval(userInput) is a code-injection risk"},
		{"innerHTML comment", "// el.innerHTML = name + suffix is an XSS risk"},
		{"rejectUnauthorized comment", "// rejectUnauthorized: false disables TLS checks"},
	}
	for _, c := range cases {
		w := checkInsecurePatterns("app.js", "", c.line+"\n")
		t.Logf("%s -> %d warning(s): %v", c.name, len(w), w)
	}
}

func TestR163CommentFPPython(t *testing.T) {
	cases := []struct {
		name string
		line string
	}{
		{"md5 comment", "# hashlib.md5(password) is weak hashing"},
		{"shell comment", "# subprocess with shell=True + user input is injection"},
		{"verify control (fixed by #274)", "# requests.get(url, verify=False) is insecure"},
	}
	for _, c := range cases {
		w := checkInsecurePatterns("app.py", "", c.line+"\n")
		t.Logf("%s -> %d warning(s): %v", c.name, len(w), w)
	}
}

// Control: SQL comment line IS handled (fix #278) - should produce 0 warnings.
func TestR163CommentControlSQL(t *testing.T) {
	w := checkInsecurePatterns("main.go", "", "package main\n\n// q := \"SELECT * FROM t\" + x is injection\n")
	t.Logf("SQL comment control -> %d warning(s): %v", len(w), w)
	if len(w) != 0 {
		t.Errorf("control failed: SQL comment should not warn after #278")
	}
}
