package agent

// Regression tests for GitHub issue #1109 - internal/agent/logging_intel_check.go
//
// Sub-items covered:
//   A1: stripGoComments must track string/rune/raw-string literals so comment
//       markers inside string values do not swallow real code (under-report).
//   A2: findGoSensitiveLogArgs must strip comments first so log calls wrapped
//       in block comments stop producing sensitive_log_arg false positives.
//   B:  checkLoggingIntel delta must compare content-anchored instance keys
//       instead of assuming old instances form an ordered prefix.
//   C:  stripInitFuncs must use balanced brace scanning so nested blocks
//       inside init() no longer truncate stripping at the first '}'.

import (
	"strings"
	"testing"
)

// ---------- A1: string literals inside stripGoComments ----------

func TestStripGoCommentsStringLiterals1109(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		expect string
	}{
		{
			name:   "double quoted block-comment marker",
			input:  `msg := "template /* placeholder"` + "\nlog.Fatal(\"x\")",
			expect: "msg := \"template /* placeholder\"\nlog.Fatal(\"x\")",
		},
		{
			name:   "URL slash-slash not treated as comment",
			input:  "u := \"http://example.com\"; log.Fatal(\"x\")",
			expect: "u := \"http://example.com\"; log.Fatal(\"x\")",
		},
		{
			name:   "single quoted rune with slash",
			input:  "c := '/'; log.Fatal(\"x\")",
			expect: "c := '/'; log.Fatal(\"x\")",
		},
		{
			name:   "raw string with slash-slash",
			input:  "p := `/etc//passwd`; log.Fatal(\"x\")",
			expect: "p := `/etc//passwd`; log.Fatal(\"x\")",
		},
		{
			name:   "escaped quote does not close string early",
			input:  `s := "a\" // b"; log.Fatal("x")`,
			expect: `s := "a\" // b"; log.Fatal("x")`,
		},
		{
			name:   "real comments still stripped after string",
			input:  "s := \"ok\"\n// real comment\n/* also real */\nlog.Fatal(\"x\")",
			expect: "s := \"ok\"\n\n\nlog.Fatal(\"x\")",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripGoComments(tt.input)
			if got != tt.expect {
				t.Errorf("stripGoComments(%q) = %q, want %q", tt.input, got, tt.expect)
			}
		})
	}
}

func TestIssue1109A1StringMarkerDoesNotSwallowFatal(t *testing.T) {
	newContent := `package lib

import "log"

func render(tpl string) {
	msg := "template /* placeholder"
	log.Println(msg)
}

func boom() {
	log.Fatal("real failure")
}
`
	warnings := checkLoggingIntel("internal/lib/render.go", "", newContent)
	if len(warnings) == 0 {
		t.Fatalf("expected fatal_in_library warning for real log.Fatal after string containing /* marker, got none")
	}
	if !strings.Contains(warnings[0], "Fatal") {
		t.Errorf("warnings[0] = %q, want fatal-family warning", warnings[0])
	}
}

func TestIssue1109A1SameLineURLThenFatal(t *testing.T) {
	newContent := `package lib

import "log"

func ping(u string) {
	u = "http://example.com/path"; log.Fatal("x")
}
`
	warnings := checkLoggingIntel("internal/lib/ping.go", "", newContent)
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "Fatal") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected fatal_in_library warning when URL // appears on same line as log.Fatal, got %v", warnings)
	}
}

// ---------- A2: block-commented sensitive log call ----------

func TestIssue1109A2BlockCommentSensitiveNoFalsePositive(t *testing.T) {
	newContent := `package auth

import "log"

func verify(user string) {
	/*
	log.Printf("token=%s", token)
	*/
	log.Printf("user=%s", user)
}
`
	warnings := checkLoggingIntel("internal/auth/verify.go", "", newContent)
	for _, w := range warnings {
		if strings.Contains(w, "sensitive_log_arg") || strings.Contains(w, "token") && strings.Contains(w, "line 6") {
			t.Errorf("unexpected sensitive-log-arg false positive from block comment: %v", warnings)
			return
		}
	}
}

// ---------- B: content-anchored delta ----------

func TestIssue1109BDeleteOldAddTwoNewSensitiveNotPrefixAssumed(t *testing.T) {
	oldContent := `package api

import "log"

func handler(req string) {
	token := getToken()
	log.Printf("req=%s token=%s", req, token)
}
`
	// Delete the old instance (now at line 3 area shifted), add two new ones.
	newContent := `package api

import "log"

func handler(req string) {
	password := getPassword()
	log.Printf("old=%s", password)
}

func cleanup() {
	apiKey := getAPIKey()
	log.Printf("cleanup key=%s", apiKey)
}
`
	warnings := checkLoggingIntel("internal/api/auth.go", oldContent, newContent)
	if len(warnings) < 2 {
		t.Fatalf("content-anchored delta should flag BOTH new instances (got %d): %v", len(warnings), warnings)
	}
	joined := strings.Join(warnings, "|")
	if !strings.Contains(joined, "cleanup") {
		t.Errorf("second new instance (cleanup apiKey) missing from warnings: %v", warnings)
	}
}

func TestIssue1109BUnchangedInstancesNotRefFlagged(t *testing.T) {
	content := `package api

import "log"

func handler(req string) {
	token := getToken()
	log.Printf("req=%s token=%s", req, token)
}
`
	if warnings := checkLoggingIntel("internal/api/auth.go", content, content); len(warnings) != 0 {
		t.Errorf("unchanged file must produce zero warnings, got %v", warnings)
	}
}

func TestIssue1109BFatalDeltaDistinguishesByLine(t *testing.T) {
	oldContent := `package lib

import "log"

func a() {
	log.Fatal("err")
}
`
	// Remove old fatal, add two new fatals elsewhere.
	newContent := `package lib

import "log"

func b() {
	doWork()
	log.Fatal("boom1")
}

func c() {
	log.Panic("boom2")
}
`
	warnings := checkLoggingIntel("internal/lib/x.go", oldContent, newContent)
	fatalWarns := 0
	for _, w := range warnings {
		if strings.Contains(w, "Fatal") {
			fatalWarns++
		}
	}
	if fatalWarns != 2 {
		t.Errorf("content-anchored delta should flag both NEW fatal instances, got %d of %v", fatalWarns, warnings)
	}
}

func TestIssue1109BRetainedFatalNotRefFlagged(t *testing.T) {
	oldContent := `package lib

import "log"

func a() {
	log.Fatal("err")
}

func b() {
	x()
	log.Panicln("p")
}
`
	warnings := checkLoggingIntel("internal/lib/y.go", oldContent, oldContent+"\n// trailing note\n")
	if len(warnings) != 0 {
		t.Errorf("retained instances across unrelated edit must not re-flag, got %v", warnings)
	}
}

// ---------- C: balanced-brace init stripping ----------

func TestStripInitFuncsNestedBlocks1109(t *testing.T) {
	input := "func init() {\n\tif cfg.Enabled {\n\t\tdo(cfg)\n\t}\n\tif cfg.Strict {\n\t\tpanic(\"strict\")\n\t}\n}\nfunc main() {}"
	got := stripInitFuncs(input)
	want := "\nfunc main() {}"
	if got != want {
		t.Errorf("stripInitFuncs nested-block input = %q, want %q", got, want)
	}
}

func TestStripInitFuncsMultipleInits1109(t *testing.T) {
	input := "a()\nfunc init() { f(1); g() }\nmiddle()\nfunc init(){\n\th()\n}\ntail()"
	got := stripInitFuncs(input)
	// Removal leaves the newline that separated each init body from its
	// surroundings, matching the pre-fix ReplaceAllString behavior.
	want := "a()\n\nmiddle()\n\ntail()"
	if got != want {
		t.Errorf("stripInitFuncs multi-init input = %q, want %q", got, want)
	}
}

func TestIssue1109CInitNestedBlockFatalWhitelisted(t *testing.T) {
	newContent := `package lib

import (
	"log"
	"os"
)

var cfg = load()

func load() *Config { return nil }

func init() {
	if cfg == nil {
		os.Exit(1)
	}
	if cfg.Verbose {
		setupVerbose()
	}
	log.SetFlags(0)
}

func regularPath() {
	log.Fatal("not allowed in library")
}
`
	warnings := checkLoggingIntel("internal/lib/boot.go", "", newContent)
	if len(warnings) != 1 {
		t.Fatalf("#1109 Item C: expected exactly one warning (regularPath log.Fatal), got %v", warnings)
	}
	if !strings.Contains(warnings[0], "Fatal") {
		t.Errorf("warning should be fatal_in_library, got %q", warnings[0])
	}
}

func TestIssue1109CUnterminatedInitLeftUntouched(t *testing.T) {
	input := "prefix func init() { neverClosed( "
	got := stripInitFuncs(input)
	if got != input {
		t.Errorf("unterminated init body should be left untouched: got %q", got)
	}
}
