package agent

// Regression test for issue #723: checkInsecurePatterns must NOT fire on pure
// comment lines. Before #723, 8 of the 10 text checks fired on comment
// mentions (only the Python verify=False check via #274 and the Go SQL check
// via #278 were comment-aware). The fix lifts comment/string stripping into
// the shared per-language line loops, so every check now operates on code
// only. Assertions here are the POST-fix expectations (inverted from the
// original characterization harness in commit 802afe7d).
//
// Repro: GOCACHE=/tmp/ggcode-fin2 go test -tags goolm -run 'TestR163Comment' -v ./internal/agent/

import "testing"

func TestR163CommentFPGo(t *testing.T) {
	cases := []struct {
		name string
		line string
	}{
		{"InsecureSkipVerify comment", "// InsecureSkipVerify: true should never be used in production"},
		{"md5 comment", "// md5.New() for password hashing is forbidden by policy"},
		{"exec.Command comment", "// exec.Command(\"sh\", \"-c\", user + input) is dangerous"},
		{"trailing comment on real code", "x := y + z // do not use InsecureSkipVerify: true here"},
	}
	for _, c := range cases {
		w := checkInsecurePatterns("main.go", "", "package main\n\nfunc f() {\n\t"+c.line+"\n}\n")
		t.Logf("%s -> %d warning(s): %v", c.name, len(w), w)
		if len(w) != 0 {
			t.Errorf("%s: expected 0 warnings on a comment line, got %d: %v", c.name, len(w), w)
		}
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
		if len(w) != 0 {
			t.Errorf("%s: expected 0 warnings on a comment line, got %d: %v", c.name, len(w), w)
		}
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
		if len(w) != 0 {
			t.Errorf("%s: expected 0 warnings on a comment line, got %d: %v", c.name, len(w), w)
		}
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

// Regression guard (#723 acceptance): real, uncommented occurrences of each
// pattern MUST still trigger a warning. Stripping comments must not blunt
// detection of genuinely insecure code.
func TestR163RealCodeStillWarns(t *testing.T) {
	t.Run("Go InsecureSkipVerify", func(t *testing.T) {
		w := checkInsecurePatterns("main.go", "", "package main\n\nfunc f() {\n\tInsecureSkipVerify: true\n}\n")
		if len(w) == 0 {
			t.Errorf("expected warning for real InsecureSkipVerify: true")
		}
	})
	t.Run("Go exec.Command shell concat", func(t *testing.T) {
		w := checkInsecurePatterns("main.go", "", "package main\n\nfunc f(user string) {\n\texec.Command(\"sh\", \"-c\", \"echo \"+user)\n}\n")
		if len(w) == 0 {
			t.Errorf("expected warning for real exec.Command shell concat")
		}
	})
	t.Run("JS eval", func(t *testing.T) {
		w := checkInsecurePatterns("app.js", "", "eval(userInput)\n")
		if len(w) == 0 {
			t.Errorf("expected warning for real eval()")
		}
	})
	t.Run("JS rejectUnauthorized", func(t *testing.T) {
		w := checkInsecurePatterns("app.js", "", "const opts = {rejectUnauthorized: false}\n")
		if len(w) == 0 {
			t.Errorf("expected warning for real rejectUnauthorized: false")
		}
	})
	t.Run("Python verify=False", func(t *testing.T) {
		w := checkInsecurePatterns("app.py", "", "requests.get(url, verify=False)\n")
		if len(w) == 0 {
			t.Errorf("expected warning for real verify=False")
		}
	})
	t.Run("Python hashlib.md5", func(t *testing.T) {
		w := checkInsecurePatterns("app.py", "", "h = hashlib.md5(password)\n")
		if len(w) == 0 {
			t.Errorf("expected warning for real hashlib.md5(password)")
		}
	})
	t.Run("Python shell=True", func(t *testing.T) {
		w := checkInsecurePatterns("app.py", "", "subprocess.run(\"echo \" + user, shell=True)\n")
		if len(w) == 0 {
			t.Errorf("expected warning for real shell=True concat")
		}
	})
}
