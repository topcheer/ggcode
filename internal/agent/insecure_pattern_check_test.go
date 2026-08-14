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

// fix #243: aliased crypto/rand import must not be misclassified as math/rand.
func TestCheckInsecurePatterns_CryptoRandAliasNotFlagged(t *testing.T) {
	old := "package main\n"
	new := "package main\n" +
		"import crand \"crypto/rand\"\n" +
		"func genToken() ([]byte, error) {\n" +
		"\ttoken := make([]byte, 16)\n" +
		"\t_, err := crand.Read(token)\n" +
		"\treturn token, err\n" +
		"}\n"

	warnings := checkInsecurePatterns("test.go", old, new)
	for _, w := range warnings {
		if strings.Contains(w, "weak crypto") {
			t.Fatalf("expected no weak crypto warning for aliased crypto/rand, got: %s", w)
		}
	}
}

// fix #243: plain crypto/rand import is also secure and must not be flagged.
func TestCheckInsecurePatterns_CryptoRandDefaultImportNotFlagged(t *testing.T) {
	old := "package main\n"
	new := "package main\n" +
		"import \"crypto/rand\"\n" +
		"func genKey() ([]byte, error) {\n" +
		"\tkey := make([]byte, 32)\n" +
		"\t_, err := rand.Read(key)\n" +
		"\treturn key, err\n" +
		"}\n"

	warnings := checkInsecurePatterns("test.go", old, new)
	for _, w := range warnings {
		if strings.Contains(w, "weak crypto") {
			t.Fatalf("expected no weak crypto warning for crypto/rand, got: %s", w)
		}
	}
}

// fix #243: aliased math/rand must still be flagged (regression guard).
func TestCheckInsecurePatterns_MathRandAliasStillFlagged(t *testing.T) {
	old := "package main\n"
	new := "package main\n" +
		"import mrand \"math/rand\"\n" +
		"func gen() int {\n" +
		"\ttoken := mrand.Intn(999999)\n" +
		"\treturn token\n" +
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
		t.Fatalf("expected weak crypto warning for aliased math/rand with token, got: %v", warnings)
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

// ---- fix #245: Python verify=False false negatives ----

// httpx calls carry no "requests"/"ssl"/"session" context word; the old
// three-condition AND silently missed them.
func TestCheckInsecurePatterns_PythonHttpxVerifyFalse(t *testing.T) {
	old := "# old code\n"
	new := "import httpx\n" +
		"resp = httpx.get(url, verify=False)\n"

	warnings := checkInsecurePatterns("app.py", old, new)
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "TLS bypass") {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected TLS bypass warning for httpx verify=False")
	}
}

// aiohttp TCPConnector(ssl=False) must be caught by the ssl= form.
func TestCheckInsecurePatterns_PythonAiohttpSSLFalse(t *testing.T) {
	old := "# old code\n"
	new := "connector = aiohttp.TCPConnector(ssl=False)\n"

	warnings := checkInsecurePatterns("app.py", old, new)
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "TLS bypass") {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected TLS bypass warning for TCPConnector(ssl=False)")
	}
}

// verify=0 without spaces was missed by the old "= 0" substring check.
func TestCheckInsecurePatterns_PythonVerifyZero(t *testing.T) {
	old := "# old code\n"
	new := "resp = httpx.get(url, verify=0)\n"

	warnings := checkInsecurePatterns("app.py", old, new)
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "TLS bypass") {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected TLS bypass warning for verify=0")
	}
}

// ---- fix #245: Go SQL false positives ----

// `i++` on a line containing SQL keywords is not concatenation.
func TestCheckInsecurePatterns_GoSQLIncrementNotConcat(t *testing.T) {
	old := "package main\n"
	new := "package main\n" +
		"for i := 0; i < n; i++ {\n" +
		"\tq := \"SELECT * FROM users\"; db.Query(q); i++\n" +
		"\ttotal += count\n" +
		"}\n"

	warnings := checkInsecurePatterns("test.go", old, new)
	for _, w := range warnings {
		if strings.Contains(w, "SQL injection") {
			t.Fatalf("i++/+= must not count as concatenation, got: %s", w)
		}
	}
}

// Full-line comments containing SQL keywords must be skipped.
func TestCheckInsecurePatterns_GoSQLCommentNotFlagged(t *testing.T) {
	old := "package main\n"
	new := "package main\n" +
		"// q := \"SELECT * FROM users\" + name\n" +
		"/* INSERT INTO logs VALUES (\" + x) */\n"

	warnings := checkInsecurePatterns("test.go", old, new)
	for _, w := range warnings {
		if strings.Contains(w, "SQL injection") {
			t.Fatalf("comment lines must not be flagged, got: %s", w)
		}
	}
}

// INSERT ... VALUES has no FROM clause and must still be recognized.
func TestCheckInsecurePatterns_GoSQLInsertValues(t *testing.T) {
	old := "package main\n"
	new := "package main\n" +
		"q := \"INSERT INTO users VALUES ('\" + name + \"')\"\n"

	warnings := checkInsecurePatterns("test.go", old, new)
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "SQL injection") {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected SQL injection warning for INSERT ... VALUES concatenation")
	}
}

// ---- fix #245: JS rejectUnauthorized / NODE_TLS false positives ----

// `timeout: 0` on a rejectUnauthorized line must not trigger the "0" check.
func TestCheckInsecurePatterns_JSTimeoutZeroNotFlagged(t *testing.T) {
	old := "// old\n"
	new := "const agent = new https.Agent({ rejectUnauthorized: true, timeout: 0 });\n"

	warnings := checkInsecurePatterns("app.js", old, new)
	for _, w := range warnings {
		if strings.Contains(w, "TLS bypass") {
			t.Fatalf("timeout: 0 must not trigger TLS bypass warning, got: %s", w)
		}
	}
}

// Comparison guards (=== / !==) against NODE_TLS_REJECT_UNAUTHORIZED='0'
// are legitimate code and must not warn.
func TestCheckInsecurePatterns_JSNodeTLSComparisonNotFlagged(t *testing.T) {
	old := "// old\n"
	new := "if (process.env.NODE_TLS_REJECT_UNAUTHORIZED === '0') {\n" +
		"  warnInsecureMode();\n" +
		"} else if (process.env.NODE_TLS_REJECT_UNAUTHORIZED !== '0') {\n" +
		"  ok();\n" +
		"}\n"

	warnings := checkInsecurePatterns("app.js", old, new)
	for _, w := range warnings {
		if strings.Contains(w, "TLS bypass") {
			t.Fatalf("NODE_TLS === '0' comparison must not warn, got: %s", w)
		}
	}
}

// Assigning the disabling value (with or without quotes) must warn.
func TestCheckInsecurePatterns_JSNodeTLSAssignmentFlagged(t *testing.T) {
	old := "// old\n"
	new := "process.env.NODE_TLS_REJECT_UNAUTHORIZED = '0';\n"

	warnings := checkInsecurePatterns("app.js", old, new)
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "TLS bypass") {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected TLS bypass warning for NODE_TLS_REJECT_UNAUTHORIZED = '0'")
	}
}

// rejectUnauthorized: 0 (numeric form) must warn via the tightened regex.
func TestCheckInsecurePatterns_JSRejectUnauthorizedZero(t *testing.T) {
	old := "// old\n"
	new := "const agent = new https.Agent({ rejectUnauthorized: 0 });\n"

	warnings := checkInsecurePatterns("app.js", old, new)
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "TLS bypass") {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected TLS bypass warning for rejectUnauthorized: 0")
	}
}
