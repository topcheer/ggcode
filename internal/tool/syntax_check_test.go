package tool

import (
	"strings"
	"testing"
)

func TestSyntaxCheckGoValid(t *testing.T) {
	validGo := []byte(`package main

import "fmt"

func main() {
	fmt.Println("hello")
}
`)
	result := syntaxCheck("/tmp/test.go", validGo)
	if result != "" {
		t.Errorf("expected empty result for valid Go, got: %s", result)
	}
}

func TestSyntaxCheckGoMissingBrace(t *testing.T) {
	// Missing closing brace — a common syntax error.
	invalidGo := []byte(`package main

func main() {
	fmt.Println("missing brace")
`)
	result := syntaxCheck("/tmp/test.go", invalidGo)
	if result == "" {
		t.Fatal("expected non-empty result for invalid Go with missing brace")
	}
	if !strings.Contains(result, "Syntax Error") {
		t.Errorf("expected 'Syntax Error' in result, got: %s", result)
	}
	if !strings.Contains(result, "will not compile") {
		t.Errorf("expected 'will not compile' in result, got: %s", result)
	}
}

func TestSyntaxCheckGoUnclosedString(t *testing.T) {
	invalidGo := []byte(`package main

func main() {
	x := "unclosed string
}
`)
	result := syntaxCheck("/tmp/test.go", invalidGo)
	if result == "" {
		t.Fatal("expected non-empty result for invalid Go with unclosed string")
	}
	if !strings.Contains(result, "Syntax Error") {
		t.Errorf("expected 'Syntax Error' in result, got: %s", result)
	}
}

func TestSyntaxCheckGoNotGoFile(t *testing.T) {
	// Non-Go file should return empty.
	result := syntaxCheck("/tmp/test.py", []byte("def foo(:\n  pass"))
	if result != "" {
		t.Errorf("expected empty result for non-Go file, got: %s", result)
	}
}

func TestSyntaxCheckGoValidAfterFormat(t *testing.T) {
	// This tests that formatGoBytes + syntaxCheck work together.
	// Code that is valid but unformatted should NOT trigger a syntax error.
	validUnformatted := []byte(`package main
import "fmt"
func main(){
fmt.Println("test")
}
`)
	_, _ = formatGoBytes("/tmp/test.go", validUnformatted)
	result := syntaxCheck("/tmp/test.go", validUnformatted)
	if result != "" {
		t.Errorf("expected empty result for valid (unformatted) Go, got: %s", result)
	}
}

func TestSyntaxCheckJSONValid(t *testing.T) {
	validJSON := []byte(`{"name": "test", "value": 42}`)
	result := syntaxCheck("/tmp/test.json", validJSON)
	if result != "" {
		t.Errorf("expected empty result for valid JSON, got: %s", result)
	}
}

func TestSyntaxCheckJSONInvalid(t *testing.T) {
	// Missing closing brace.
	invalidJSON := []byte(`{"name": "test", "value": 42`)
	result := syntaxCheck("/tmp/test.json", invalidJSON)
	if result == "" {
		t.Fatal("expected non-empty result for invalid JSON")
	}
	if !strings.Contains(result, "invalid JSON") {
		t.Errorf("expected 'invalid JSON' in result, got: %s", result)
	}
}

func TestSyntaxCheckJSONTrailingComma(t *testing.T) {
	invalidJSON := []byte(`{"name": "test", "items": [1, 2, 3,],}`)
	result := syntaxCheck("/tmp/test.json", invalidJSON)
	if result == "" {
		t.Fatal("expected non-empty result for JSON with trailing comma")
	}
}

func TestSyntaxCheckJSONEmpty(t *testing.T) {
	// Empty content should return empty (no warning).
	result := syntaxCheck("/tmp/test.json", []byte(""))
	if result != "" {
		t.Errorf("expected empty result for empty JSON, got: %s", result)
	}
	result = syntaxCheck("/tmp/test.json", []byte("   \n\n"))
	if result != "" {
		t.Errorf("expected empty result for whitespace-only JSON, got: %s", result)
	}
}

func TestSyntaxCheckUnsupportedExtension(t *testing.T) {
	// Unsupported file types should return empty.
	result := syntaxCheck("/tmp/test.yaml", []byte("invalid: ["))
	if result != "" {
		t.Errorf("expected empty result for unsupported extension, got: %s", result)
	}
	result = syntaxCheck("/tmp/test.md", []byte("# broken heading"))
	if result != "" {
		t.Errorf("expected empty result for .md file, got: %s", result)
	}
}

func TestSyntaxCheckGoFatalError(t *testing.T) {
	// Completely broken file — parser returns nil file.
	brokenGo := []byte(`!!! this is not Go at all`)
	result := syntaxCheck("/tmp/test.go", brokenGo)
	if result == "" {
		t.Fatal("expected non-empty result for completely broken Go")
	}
	// Should contain the parser's error message about expected 'package'.
	if !strings.Contains(result, "expected 'package'") {
		t.Errorf("expected 'expected package' error message, got: %s", result)
	}
}

func TestSyntaxCheckGoErrorPathTrimmed(t *testing.T) {
	// Error messages should use basename, not full path.
	invalidGo := []byte(`package main
func foo() {`)
	result := syntaxCheck("/some/very/long/path/to/file.go", invalidGo)
	if !strings.Contains(result, "file.go") {
		t.Errorf("expected basename in error, got: %s", result)
	}
	// Should NOT contain the full path.
	if strings.Contains(result, "/some/very/long/path/") {
		t.Errorf("expected basename only, but full path found in: %s", result)
	}
}

// Test that syntaxCheck is fast enough for large files (non-blocking requirement).
func TestSyntaxCheckGoPerformance(t *testing.T) {
	// Generate a large valid Go file (~5000 lines).
	var sb strings.Builder
	sb.WriteString("package main\n\n")
	for i := 0; i < 1000; i++ {
		sb.WriteString("func f")
		sb.WriteString(string(rune('A' + i%26)))
		sb.WriteString(string(rune('A' + (i+1)%26)))
		sb.WriteString("() int { return ")
		sb.WriteString(string(rune('0' + i%10)))
		sb.WriteString(" }\n")
	}
	data := []byte(sb.String())
	// This should complete in well under 100ms.
	result := syntaxCheck("/tmp/large.go", data)
	if result != "" {
		t.Errorf("expected empty result for valid large Go file, got: %s", result)
	}
}
