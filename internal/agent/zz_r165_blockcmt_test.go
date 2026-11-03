package agent

import "testing"

func TestR165BlockCommentBodyFP(t *testing.T) {
	cases := []struct {
		name     string
		file     string
		code     string
		wantWarn int
	}{

		{"Go block body TLS", "main.go", "package main\n\n/*\n * InsecureSkipVerify: true must never be used\n */\nfunc f() {}\n", 0},
		{"JS block body eval", "app.js", "/*\n * eval(userInput) is dangerous here\n */\n", 0},
		{"JS block body innerHTML", "app.js", "/*\n * el.innerHTML = a + b is XSS\n */\n", 0},
		{"Py hash body md5", "app.py", "# comment\n# hashlib.md5(password) weak\n", 0},

		{"Go mixed open then body", "main.go", "x := 1 /* note\n * InsecureSkipVerify: true\n */\n", 0},

		{"Go one-line block", "main.go", "/* InsecureSkipVerify: true */\n", 0},

		{"Go code after close", "main.go", "/* comment\n */ tlsConfig.InsecureSkipVerify = true\n", 1},

		{"Go unclosed block", "main.go", "code()\n/*\nInsecureSkipVerify: true\nstill comment\n", 0},
	}
	for _, c := range cases {
		w := checkInsecurePatterns(c.file, "", c.code)
		if len(w) != c.wantWarn {
			t.Errorf("%s: got %d warning(s), want %d: %v", c.name, len(w), c.wantWarn, w)
		}
	}
}

func TestR165RealCodeStillWarns(t *testing.T) {
	if w := checkInsecurePatterns("main.go", "",
		"package main\nfunc f() { c := tls.Config{InsecureSkipVerify: true} }\n"); len(w) == 0 {
		t.Error("Go real InsecureSkipVerify: expected warning, got none")
	}
	if w := checkInsecurePatterns("app.js", "", "eval(userInput);\n"); len(w) == 0 {
		t.Error("JS real eval(): expected warning, got none")
	}
	if w := checkInsecurePatterns("app.js", "", "el.innerHTML = a + b;\n"); len(w) == 0 {
		t.Error("JS real innerHTML concat: expected warning, got none")
	}
}
