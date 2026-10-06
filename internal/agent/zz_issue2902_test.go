package agent

import (
	"strings"
	"testing"
)

// zz_issue2902_test.go - regression probes for #2902: Go string-literal
// CONTENTS must not reach the text-based insecure-pattern checks. Fixtures,
// error messages, and assertions like want := "InsecureSkipVerify: true"
// used to fire false [Security] warnings (JS #1063 and Python already strip
// strings; Go was the asymmetric gap).

func TestIssue2902StringLiteralContentsNotScanned(t *testing.T) {
	src := `package test

func TestDocs(t *testing.T) {
	want := "InsecureSkipVerify: true"
	errormsg := "InsecureSkipVerify: true is forbidden in production"
	msg := "SELECT " + "columns are fine in docs"
	if want != errormsg {
		t.Fatal(msg)
	}
}
`
	warnings := checkInsecurePatterns("probe_test.go", "", src)
	out := strings.Join(warnings, "\n")
	if out != "" {
		t.Fatalf("#2902: string-literal contents fired false security warnings:\n%s", out)
	}
}

func TestIssue2902RawStringMultilineNotScanned(t *testing.T) {
	src := "package test\n\nvar doc = `InsecureSkipVerify: true is banned.\nUse explicit TLS verification.\n`\n\nvar _ = doc\n"
	warnings := checkInsecurePatterns("probe_test.go", "", src)
	out := strings.Join(warnings, "\n")
	if out != "" {
		t.Fatalf("#2902: multi-line raw string contents fired false security warnings:\n%s", out)
	}
}

func TestIssue2902RealCodeStillDetected(t *testing.T) {
	src := `package test

import "crypto/tls"

func client() {
	c := &tls.Config{InsecureSkipVerify: true}
	_ = c
}
`
	warnings := checkInsecurePatterns("probe_test.go", "", src)
	out := strings.Join(warnings, "\n")
	if !strings.Contains(out, "TLS bypass") {
		t.Fatalf("#2902 fix over-stripped: real InsecureSkipVerify assignment must still be detected, got: %q", out)
	}
}

func TestIssue2902EscapedQuoteHandling(t *testing.T) {
	// The string contains an escaped quote; the stripper must not
	// terminate early and leak the remainder of the line as code.
	src := `package test

var s = "quoted: \" InsecureSkipVerify: true still inside string"
var _ = s
`
	warnings := checkInsecurePatterns("probe_test.go", "", src)
	out := strings.Join(warnings, "\n")
	if out != "" {
		t.Fatalf("#2902: escaped-quote string leaked contents to the scanner:\n%s", out)
	}
}

func TestIssue2902SQLConcatInStringNotFlagged(t *testing.T) {
	// A bare doc mention of SELECT inside one string (no concatenation on
	// the line at all) must not be flagged. Note: a literal+literal concat
	// like "SELECT " + " docs" still reads as a concatenation signal post-
	// strip ("" + "") - distinguishing it needs operand analysis and is left
	// out deliberately; the fix targets mention-only false positives.
	src := `package test

func msg() string { return "we never SELECT from user input here" }
`
	warnings := checkInsecurePatterns("probe_test.go", "", src)
	out := strings.Join(warnings, "\n")
	if out != "" {
		t.Fatalf("#2902: doc-only string mention flagged as SQL injection:\n%s", out)
	}
}
