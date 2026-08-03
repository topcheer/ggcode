package agent

import (
	"strings"
	"testing"
)

func TestCheckInsecurePatterns_GoTLSBypass(t *testing.T) {
	old := "package main\n"
	new := "package main\n" +
		"import \"crypto/tls\"\n" +
		"cfg := &tls.Config{InsecureSkipVerify: true}\n"

	warnings := checkInsecurePatterns("test.go", old, new)
	if len(warnings) == 0 {
		t.Fatal("expected TLS bypass warning for InsecureSkipVerify: true")
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "TLS bypass") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected TLS bypass warning, got: %v", warnings)
	}
}

func TestCheckInsecurePatterns_GoTLSBypassFalse(t *testing.T) {
	old := "package main\n"
	new := "package main\n" +
		"import \"crypto/tls\"\n" +
		"cfg := &tls.Config{InsecureSkipVerify: false}\n"

	warnings := checkInsecurePatterns("test.go", old, new)
	for _, w := range warnings {
		if strings.Contains(w, "TLS bypass") {
			t.Fatalf("should not flag InsecureSkipVerify: false, got: %s", w)
		}
	}
}

func TestCheckInsecurePatterns_GoSQLInjection(t *testing.T) {
	old := "package main\n"
	new := "package main\n" +
		"query := \"SELECT * FROM users WHERE name = \" + name\n"

	warnings := checkInsecurePatterns("test.go", old, new)
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "SQL injection") {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected SQL injection warning for string concatenation in query")
	}
}

func TestCheckInsecurePatterns_GoCommandInjection(t *testing.T) {
	old := "package main\n"
	new := "package main\n" +
		"exec.Command(\"/bin/sh\", \"-c\", \"echo \" + userInput)\n"

	warnings := checkInsecurePatterns("test.go", old, new)
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "command injection") {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected command injection warning")
	}
}

func TestCheckInsecurePatterns_GoMathRandForToken(t *testing.T) {
	old := "package main\n"
	new := "package main\n" +
		"import (\n" +
		"\"math/rand\"\n" +
		")\n" +
		"func genToken() int {\n" +
		"	token := rand.Intn(999999)\n" +
		"	return token\n" +
		"}\n"

	warnings := checkInsecurePatterns("test.go", old, new)
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "weak crypto") && strings.Contains(w, "token") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected weak crypto warning for math/rand used with token, got: %v", warnings)
	}
}

func TestCheckInsecurePatterns_JSRejectUnauthorized(t *testing.T) {
	old := "// old code\n"
	new := "const agent = new https.Agent({\n" +
		"  rejectUnauthorized: false\n" +
		"});\n"

	warnings := checkInsecurePatterns("app.js", old, new)
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "TLS bypass") {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected TLS bypass warning for rejectUnauthorized: false")
	}
}

func TestCheckInsecurePatterns_JSEval(t *testing.T) {
	old := "// old\n"
	new := "var result = eval(userInput);\n"

	warnings := checkInsecurePatterns("app.js", old, new)
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "code injection") {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected code injection warning for eval()")
	}
}

func TestCheckInsecurePatterns_PythonVerifyFalse(t *testing.T) {
	old := "# old code\n"
	new := "import requests\n" +
		"resp = requests.get(url, verify=False)\n"

	warnings := checkInsecurePatterns("app.py", old, new)
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "TLS bypass") {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected TLS bypass warning for verify=False")
	}
}

func TestCheckInsecurePatterns_PythonHashlibMD5(t *testing.T) {
	old := "# old code\n"
	new := "import hashlib\n" +
		"password_hash = hashlib.md5(password.encode()).hexdigest()\n"

	warnings := checkInsecurePatterns("app.py", old, new)
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "weak crypto") {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected weak crypto warning for MD5 password hashing")
	}
}

func TestCheckInsecurePatterns_DeltaAware(t *testing.T) {
	content := "package main\n" +
		"cfg := &tls.Config{InsecureSkipVerify: true}\n"

	// Same content in old and new - should not report.
	warnings := checkInsecurePatterns("test.go", content, content)
	if len(warnings) > 0 {
		t.Fatalf("expected no warnings for pre-existing patterns (delta-aware), got: %v", warnings)
	}
}

func TestCheckInsecurePatterns_UnsupportedExt(t *testing.T) {
	warnings := checkInsecurePatterns("readme.md", "", "# Hello\nInsecureSkipVerify: true\n")
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings for .md file, got: %v", warnings)
	}
}

func TestCheckInsecurePatterns_EmptyContent(t *testing.T) {
	warnings := checkInsecurePatterns("test.go", "", "")
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings for empty content, got: %v", warnings)
	}
}
