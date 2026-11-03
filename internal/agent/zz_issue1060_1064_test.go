package agent

import (
	"strings"
	"testing"
)

func TestIssue1060_FStringShellTrueDetected(t *testing.T) {
	warnings := checkInsecurePatterns("app.py", "", "subprocess.run(f\"ls {path}\", shell=True)\n")
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "command injection") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected command injection warning for f-string + shell=True, got %v", warnings)
	}
}

func TestIssue1061_LiteralEvalAndModelEvalNotFlagged(t *testing.T) {
	for _, src := range []string{
		"val = ast.literal_eval(s)\n",
		"loss = model.eval()\n",
	} {
		warnings := checkInsecurePatterns("app.py", "", src)
		for _, w := range warnings {
			if strings.Contains(w, "code injection") {
				t.Fatalf("false positive code injection for %q: %v", src, warnings)
			}
		}
	}

	warnings := checkInsecurePatterns("app.py", "", "result = eval(user_input)\n")
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "code injection") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected code injection warning for eval(user_input), got %v", warnings)
	}
}

func TestIssue1062_GoGetterShapesNotFlagged(t *testing.T) {
	for _, src := range []string{
		"if c.InsecureSkipVerify { return true }\n",
		"return c.InsecureSkipVerify == true\n",
	} {
		warnings := checkInsecurePatterns("x.go", "", src)
		for _, w := range warnings {
			if strings.Contains(w, "TLS bypass") {
				t.Fatalf("false positive TLS bypass for %q: %v", src, warnings)
			}
		}
	}

	for _, src := range []string{
		"tls := &tls.Config{InsecureSkipVerify: true}\n",
		"c.InsecureSkipVerify = true\n",
	} {
		warnings := checkInsecurePatterns("x.go", "", src)
		found := false
		for _, w := range warnings {
			if strings.Contains(w, "TLS bypass") {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected TLS bypass warning for %q, got %v", src, warnings)
		}
	}
}

func TestIssue1063_JSStringLiteralMentionsNotFlagged(t *testing.T) {
	for _, src := range []string{
		"const desc = \"avoid eval() here\";\n",
		"const note = 'NODE_TLS_REJECT_UNAUTHORIZED=0 disables TLS';\n",
	} {
		warnings := checkInsecurePatterns("app.js", "", src)
		if len(warnings) > 0 {
			t.Fatalf("expected no warnings for %q, got %v", src, warnings)
		}
	}

	warnings := checkInsecurePatterns("app.js", "", "eval(userInput);\n")
	if len(warnings) == 0 {
		t.Fatal("expected code injection warning for eval(userInput)")
	}
}

func TestIssue1064_TurkeyNotSensitiveAndInlineBlockCommentStripped(t *testing.T) {

	if isSecuritySensitiveName("turkey") || isSecuritySensitiveName("keyboard") {
		t.Fatal("turkey/keyboard must not be security-sensitive (#1064-A5)")
	}
	if !isSecuritySensitiveName("apiKey") || !isSecuritySensitiveName("encryption_key") || !isSecuritySensitiveName("key") {
		t.Fatal("apiKey/encryption_key/key must remain security-sensitive (#1064-A5)")
	}

	warnings := checkInsecurePatterns("app.py", "", "turkey = random.random()\n")
	for _, w := range warnings {
		if strings.Contains(w, "weak crypto") {
			t.Fatalf("false positive weak crypto for turkey: %v", warnings)
		}
	}

	warnings = checkInsecurePatterns("x.go", "", "cfg := getConfig() {/* InsecureSkipVerify: true */}\n")
	for _, w := range warnings {
		if strings.Contains(w, "TLS bypass") {
			t.Fatalf("false positive TLS bypass from inline block comment: %v", warnings)
		}
	}

	warnings = checkInsecurePatterns("app.py", "", "subprocess.run(cmd, shell=True)  # see also + operator\n")
	for _, w := range warnings {
		if strings.Contains(w, "command injection") {
			t.Fatalf("false positive command injection from trailing comment plus: %v", warnings)
		}
	}
}

func TestIssue1064_A8_NewExtensionsDispatched(t *testing.T) {

	for _, f := range []string{"a.mts", "b.cjs", "c.cts"} {
		warnings := checkInsecurePatterns(f, "", "eval(userInput);\n")
		if len(warnings) == 0 {
			t.Fatalf("expected eval( warning to fire for %s", f)
		}
	}

	warnings := checkInsecurePatterns("a.pyw", "", "requests.get(url, verify=False)\n")
	if len(warnings) == 0 {
		t.Fatal("expected verify=False warning to fire for .pyw")
	}
}
