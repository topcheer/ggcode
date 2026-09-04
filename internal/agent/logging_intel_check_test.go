package agent

import (
	"strings"
	"testing"
)

func TestCheckLoggingIntel_GoSensitiveVarInLogCall(t *testing.T) {
	newContent := `package api

import "log"

func auth() {
	token := getAPIToken()
	log.Printf("auth token: %s", token)
}
`
	warnings := checkLoggingIntel("internal/api/auth.go", "", newContent)
	if len(warnings) == 0 {
		t.Fatal("expected warnings for sensitive var in log call")
	}
	if !strings.Contains(warnings[0], "sensitive") {
		t.Errorf("unexpected warning: %s", warnings[0])
	}
}

func TestCheckLoggingIntel_GoPasswordInLogCall(t *testing.T) {
	newContent := `package db

import "log"

func connect(password string) {
	log.Printf("connecting with password: %s", password)
}
`
	warnings := checkLoggingIntel("internal/db/conn.go", "", newContent)
	if len(warnings) == 0 {
		t.Fatal("expected warning for password in log call")
	}
}

func TestCheckLoggingIntel_NoWarningForNonSensitiveVar(t *testing.T) {
	newContent := `package util

import "log"

func process(name string, count int) {
	log.Printf("processing %s, count=%d", name, count)
}
`
	warnings := checkLoggingIntel("internal/util/process.go", "", newContent)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings for non-sensitive vars, got: %v", warnings)
	}
}

func TestCheckLoggingIntel_NoWarningForSensitiveInFormatString(t *testing.T) {
	// "password" appears only inside the string literal, not as a variable arg
	newContent := `package auth

import "log"

func verify(user string) {
	log.Printf("checking password for user %s", user)
}
`
	warnings := checkLoggingIntel("internal/auth/verify.go", "", newContent)
	if len(warnings) != 0 {
		t.Errorf("expected no warning when 'password' is only in format string, got: %v", warnings)
	}
}

func TestCheckLoggingIntel_FatalInLibraryPackage(t *testing.T) {
	newContent := `package helper

import "log"

func loadConfig() {
	if err != nil {
		log.Fatal("failed to load config")
	}
}
`
	warnings := checkLoggingIntel("internal/helper/config.go", "", newContent)
	if len(warnings) == 0 {
		t.Fatal("expected warning for log.Fatal in non-main package")
	}
	if !strings.Contains(warnings[0], "Fatal") {
		t.Errorf("expected Fatal warning, got: %s", warnings[0])
	}
}

func TestCheckLoggingIntel_NoWarningForFatalInMainPackage(t *testing.T) {
	newContent := `package main

import "log"

func main() {
	if err != nil {
		log.Fatal("failed to start")
	}
}
`
	warnings := checkLoggingIntel("cmd/myapp/main.go", "", newContent)
	if len(warnings) != 0 {
		t.Errorf("expected no warning for log.Fatal in main package, got: %v", warnings)
	}
}

func TestCheckLoggingIntel_DeltaAware(t *testing.T) {
	oldContent := `package api

import "log"

func auth() {
	token := getToken()
	log.Printf("token: %s", token)
}
`
	// Same content - no new patterns introduced
	warnings := checkLoggingIntel("internal/api/auth.go", oldContent, oldContent)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings for unchanged content, got: %v", warnings)
	}
}

func TestCheckLoggingIntel_DeltaAwareNewPattern(t *testing.T) {
	oldContent := `package api

import "log"

func auth() {
	name := "test"
	log.Printf("user: %s", name)
}
`
	newContent := oldContent + `
func auth2() {
	secret := getSecret()
	log.Printf("secret: %s", secret)
}
`
	warnings := checkLoggingIntel("internal/api/auth.go", oldContent, newContent)
	if len(warnings) == 0 {
		t.Fatal("expected warning for newly introduced sensitive var")
	}
}

func TestCheckLoggingIntel_PanicInLibraryPackage(t *testing.T) {
	newContent := `package server

import "log"

func handle() {
	log.Panic("unexpected state")
}
`
	warnings := checkLoggingIntel("internal/server/handle.go", "", newContent)
	if len(warnings) == 0 {
		t.Fatal("expected warning for log.Panic in non-main package")
	}
}

func TestCheckLoggingIntel_EmptyContent(t *testing.T) {
	warnings := checkLoggingIntel("internal/api/auth.go", "", "")
	if warnings != nil {
		t.Errorf("expected nil for empty content, got: %v", warnings)
	}
}

func TestCheckLoggingIntel_NonSourceFile(t *testing.T) {
	newContent := `log.Printf("token: %s", token)`
	warnings := checkLoggingIntel("README.md", "", newContent)
	if warnings != nil {
		t.Errorf("expected nil for non-source file, got: %v", warnings)
	}
}

func TestCheckLoggingIntel_TestFileExempt(t *testing.T) {
	newContent := `package api

import "log"

func auth() {
	token := getToken()
	log.Printf("token: %s", token)
}
`
	warnings := checkLoggingIntel("testdata/auth.go", "", newContent)
	if warnings != nil {
		t.Errorf("expected nil for testdata dir, got: %v", warnings)
	}
}

func TestCheckLoggingIntel_JSConsoleSensitiveVar(t *testing.T) {
	newContent := `function login() {
	const password = document.getElementById("pw").value;
	console.log("attempting login", password);
}
`
	warnings := checkLoggingIntel("src/login.js", "", newContent)
	if len(warnings) == 0 {
		t.Fatal("expected warning for sensitive var in console.log")
	}
}

func TestCheckLoggingIntel_JSConsoleNonSensitive(t *testing.T) {
	newContent := `function greet(name) {
	console.log("hello", name);
}
`
	warnings := checkLoggingIntel("src/greet.js", "", newContent)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings for non-sensitive JS var, got: %v", warnings)
	}
}

func TestCheckLoggingIntel_StructuredLogger(t *testing.T) {
	newContent := `package auth

import "log/slog"

func login() {
	apiKey := getAPIKey()
	slog.Info("login attempt", "apiKey", apiKey)
}
`
	warnings := checkLoggingIntel("internal/auth/login.go", "", newContent)
	if len(warnings) == 0 {
		t.Fatal("expected warning for sensitive var in slog call")
	}
}

func TestCheckLoggingIntel_MultipleSensitiveArgs(t *testing.T) {
	newContent := `package auth

import "log"

func verify(token string, password string) {
	log.Printf("token=%s password=%s", token, password)
}
`
	warnings := checkLoggingIntel("internal/auth/verify.go", "", newContent)
	if len(warnings) == 0 {
		t.Fatal("expected warning for multiple sensitive vars")
	}
}

func TestCheckLoggingIntel_MaxWarningsCap(t *testing.T) {
	// Generate many sensitive log lines to test the cap
	var lines []string
	lines = append(lines, "package api", "", `import "log"`, "")
	for i := 0; i < 20; i++ {
		lines = append(lines, `func f`+string(rune('A'+i))+`() {
	token := getToken()
	log.Printf("token: %s", token)
}`)
	}
	newContent := strings.Join(lines, "\n")
	warnings := checkLoggingIntel("internal/api/big.go", "", newContent)
	if len(warnings) > maxLogIntelWarnings {
		t.Errorf("expected at most %d warnings, got %d", maxLogIntelWarnings, len(warnings))
	}
}

func TestHasSensitiveVarRef_InStringOnly(t *testing.T) {
	args := `"checking password for user"`
	matches := []string{"password"}
	if hasSensitiveVarRef(args, matches, false) {
		t.Error("expected false when sensitive name is only in string literal")
	}
}

func TestHasSensitiveVarRef_AsIdentifier(t *testing.T) {
	args := `"format", password`
	matches := []string{"password"}
	if !hasSensitiveVarRef(args, matches, false) {
		t.Error("expected true when sensitive name is a bare identifier")
	}
}

func TestStripStringLiterals(t *testing.T) {
	tests := []struct {
		input  string
		expect string
	}{
		{`"hello world"`, ``},
		{`"a", var, "b"`, `, var, `},
		{`key="value"`, `key=`},
		{`'single'`, ``},
		{"`backtick`", ``},
	}
	for _, tt := range tests {
		got := stripStringLiterals(tt.input)
		if got != tt.expect {
			t.Errorf("stripStringLiterals(%q) = %q, want %q", tt.input, got, tt.expect)
		}
	}
}

func TestExtractGoCallArgs(t *testing.T) {
	tests := []struct {
		input  string
		expect string
	}{
		{`log.Printf("hello", name)`, `"hello", name`},
		{`log.Fatal("msg")`, `"msg"`},
		{`noParens`, ``},
		{`log.Printf(nested(a, b))`, `nested(a, b)`},
	}
	for _, tt := range tests {
		got := extractGoCallArgs(tt.input)
		if got != tt.expect {
			t.Errorf("extractGoCallArgs(%q) = %q, want %q", tt.input, got, tt.expect)
		}
	}
}

func TestTruncateForLog(t *testing.T) {
	short := "hello"
	if got := truncateForLog(short, 80); got != short {
		t.Errorf("truncateForLog(short) = %q, want %q", got, short)
	}
	long := strings.Repeat("x", 100)
	got := truncateForLog(long, 80)
	if !strings.HasSuffix(got, "...") {
		t.Errorf("expected truncated string to end with ...")
	}
	if len(got) != 80 {
		t.Errorf("expected length 80, got %d", len(got))
	}
}

// #1098 Bug 1: word boundaries - should NOT flag variables like tokenCount, maxTokens
func TestCheckLoggingIntel_WordBoundariesNoFalsePositive(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		wantWarn bool
	}{
		{
			name: "tokenCount should NOT trigger",
			content: `package counter

import "log"

func report() {
	tokenCount := 42
	maxTokens := 1000
	log.Printf("tokens: %d/%d", tokenCount, maxTokens)
}
`,
			wantWarn: false,
		},
		{
			name: "actual token var SHOULD trigger",
			content: `package auth

import "log"

func login() {
	token := "abc123"
	log.Printf("token: %s", token)
}
`,
			wantWarn: true,
		},
		{
			name: "tokenizerCount should NOT trigger",
			content: `package parser

import "log"

func parse() {
	tokenizerCount := 5
	log.Printf("using %d tokenizers", tokenizerCount)
}
`,
			wantWarn: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			warnings := checkLoggingIntel("test.go", "", tt.content)
			hasWarn := len(warnings) > 0
			if hasWarn != tt.wantWarn {
				t.Errorf("checkLoggingIntel() warnings=%v, wantWarn=%v", warnings, tt.wantWarn)
			}
		})
	}
}

// #1098 Bug 2: init() filtering - log.Fatal in init() should NOT be flagged
func TestCheckLoggingIntel_InitFuncNotFlagged(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		wantWarn bool
	}{
		{
			name: "log.Fatal in init() should NOT trigger",
			content: `package config

import "log"

func init() {
	if os.Getenv("API_KEY") == "" {
		log.Fatal("API_KEY required")
	}
}
`,
			wantWarn: false,
		},
		{
			name: "log.Fatal in regular func SHOULD trigger",
			content: `package lib

import "log"

func process() {
	if err != nil {
		log.Fatal("process failed")
	}
}
`,
			wantWarn: true,
		},
		{
			name: "log.Fatal in init() but also in regular func - regular func triggers",
			content: `package main

import "log"

func init() {
	if missing {
		log.Fatal("missing config")
	}
}

func main() {
	if err != nil {
		log.Fatal("error")  // this SHOULD trigger (but main is exempt anyway)
	}
}
`,
			wantWarn: false, // main package is exempt
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			warnings := checkLoggingIntel("test.go", "", tt.content)
			hasWarn := len(warnings) > 0
			if hasWarn != tt.wantWarn {
				t.Errorf("checkLoggingIntel() warnings=%v, wantWarn=%v", warnings, tt.wantWarn)
			}
		})
	}
}

// #1098 Bug 3: comment stripping - comments should not trigger false positives
func TestCheckLoggingIntel_CommentStripping(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		wantWarn bool
	}{
		{
			name: "comment about log.Fatal should NOT trigger",
			content: `package lib

// do NOT call log.Fatal() in library code
// log.Fatal is only for main packages

func process() {
	// log.Fatal("bad")  // commented out, should not trigger
	return nil
}
`,
			wantWarn: false,
		},
		{
			name: "license header + log.Fatal in non-main should trigger",
			content: `// Copyright 2024 Example Corp
// Licensed under MIT

package lib

import "log"

func process() {
	log.Fatal("should trigger")
}
`,
			wantWarn: true,
		},
		{
			name: "multi-line comment with sensitive names",
			content: `package util

/*
 * This function handles password reset
 * Do NOT log the password directly
 */

func reset(pw string) {
	// safe logging
	log.Printf("reset for user")
}
`,
			wantWarn: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			warnings := checkLoggingIntel("test.go", "", tt.content)
			hasWarn := len(warnings) > 0
			if hasWarn != tt.wantWarn {
				t.Errorf("checkLoggingIntel() warnings=%v, wantWarn=%v", warnings, tt.wantWarn)
			}
		})
	}
}

// #1098 Bug 3: test stripGoComments helper
func TestStripGoComments(t *testing.T) {
	tests := []struct {
		input  string
		expect string
	}{
		{
			input:  "// single line comment\nfunc f() {}",
			expect: "\nfunc f() {}",
		},
		{
			// #1109 Item A2: newlines inside block comments are preserved so
			// reported line numbers stay aligned with the original source.
			input:  "/* multi\nline */func f() {}",
			expect: "\nfunc f() {}",
		},
		{
			input:  "func f() { // comment\n}",
			expect: "func f() { \n}",
		},
		{
			input:  "// comment 1\n// comment 2\nfunc f() {}",
			expect: "\n\nfunc f() {}",
		},
	}
	for _, tt := range tests {
		got := stripGoComments(tt.input)
		if got != tt.expect {
			t.Errorf("stripGoComments(%q) = %q, want %q", tt.input, got, tt.expect)
		}
	}
}

// #1098 Bug 2: test stripInitFuncs helper
func TestStripInitFuncs(t *testing.T) {
	tests := []struct {
		input  string
		expect string
	}{
		{
			input:  "func init() { log.Fatal(\"config\") }",
			expect: "",
		},
		{
			input:  "func init() {\n\tsetup()\n}\nfunc main() {}",
			expect: "\nfunc main() {}",
		},
		{
			input:  "func other() {}\nfunc init() { f() }",
			expect: "func other() {}\n",
		},
	}
	for _, tt := range tests {
		got := stripInitFuncs(tt.input)
		if got != tt.expect {
			t.Errorf("stripInitFuncs(%q) = %q, want %q", tt.input, got, tt.expect)
		}
	}
}

// TestStripLiterals_1581 pins #1581-A: Go raw-string ${} is literal text
// (stripped whole, no false sensitive-ref), JS template ${} stays live.
func TestStripLiterals_1581(t *testing.T) {
	goRaw := stripStringLiteralsFor("log.Printf(`curl -H \"Authorization: Bearer ${GITHUB_TOKEN}\"`)", false)
	if strings.Contains(strings.ToUpper(goRaw), "GITHUB_TOKEN") {
		t.Fatalf("Go raw string must strip ${} whole, got %q", goRaw)
	}
	jsTpl := stripStringLiteralsFor("console.log(`Bearer ${accessToken}`)", true)
	if !strings.Contains(jsTpl, "accessToken") {
		t.Fatalf("JS template interpolation must survive, got %q", jsTpl)
	}
}
