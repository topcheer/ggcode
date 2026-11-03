package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckWriteIntegrity_GoSyntaxError(t *testing.T) {

	badGo := "package main\n\nfunc broken( {\n\treturn nil\n"
	old := "package main\n\nfunc broken() {\n\treturn nil\n}\n"

	warning := checkWriteIntegrity("main.go", old, badGo)
	if warning == "" {
		t.Fatal("expected integrity warning for Go syntax error, got empty string")
	}
	if !strings.Contains(warning, "Post-write integrity check") {
		t.Errorf("warning should contain header, got: %s", warning)
	}
	if !strings.Contains(warning, "main.go") {
		t.Errorf("warning should reference file path, got: %s", warning)
	}
}

func TestCheckWriteIntegrity_ValidGoFile(t *testing.T) {
	goodGo := "package main\n\nfunc main() {}\n"
	warning := checkWriteIntegrity("main.go", "", goodGo)
	if warning != "" {
		t.Errorf("expected no warning for valid Go file, got: %s", warning)
	}
}

func TestCheckWriteIntegrity_NullBytes(t *testing.T) {
	content := "package main\x00\nfunc main() {}\n"
	warning := checkWriteIntegrity("main.go", "", content)
	if warning == "" {
		t.Fatal("expected warning for null bytes")
	}
	if !strings.Contains(warning, "null byte") {
		t.Errorf("warning should mention null bytes, got: %s", warning)
	}
}

func TestCheckWriteIntegrity_ContentLoss(t *testing.T) {

	warning := checkWriteIntegrity("main.go", "package main\nfunc main() {}\n", "")
	if warning == "" {
		t.Fatal("expected content loss warning")
	}
	if !strings.Contains(warning, "EMPTY file") {
		t.Errorf("warning should mention empty file, got: %s", warning)
	}
}

func TestCheckWriteIntegrity_NoWarningForNewFile(t *testing.T) {

	warning := checkWriteIntegrity("main.go", "", "package main\nfunc main() {}\n")
	if warning != "" {
		t.Errorf("expected no warning for new valid file, got: %s", warning)
	}
}

func TestCheckWriteIntegrity_NonGoFileSkipsSyntaxCheck(t *testing.T) {

	content := "{ this is not valid code but not Go either"
	warning := checkWriteIntegrity("readme.md", "old content", content)

	if warning != "" {
		t.Errorf("expected no warning for non-Go file, got: %s", warning)
	}
}

func TestCheckWriteIntegrity_WhitespaceOnlyContentLoss(t *testing.T) {

	warning := checkWriteIntegrity("main.go", "package main\n", "   \n  \t  ")
	if warning == "" {
		t.Fatal("expected content loss warning for whitespace-only result")
	}
	if !strings.Contains(warning, "EMPTY") {
		t.Errorf("warning should mention empty, got: %s", warning)
	}
}

func TestCheckGoSyntax_MultipleErrors(t *testing.T) {

	badGo := "package main\n\nfunc a( {\nfunc b( {\nfunc c( {\n"
	warnings := checkGoSyntax("multi.go", badGo)
	if len(warnings) == 0 {
		t.Fatal("expected syntax errors")
	}

	if len(warnings) > maxGoSyntaxErrors+1 {
		t.Errorf("too many warnings: %d (max should be %d+1)", len(warnings), maxGoSyntaxErrors)
	}
}

func TestCheckGoSyntax_ValidFile(t *testing.T) {
	goodGo := `package test

import "fmt"

func main() {
	fmt.Println("hello")
}
`
	warnings := checkGoSyntax("valid.go", goodGo)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings for valid Go, got: %v", warnings)
	}
}

func TestCheckWriteIntegrity_ErrorOrder(t *testing.T) {
	newGo := `package main
import "net/http"
func fetch(url string) error {
	resp, err := http.Get(url)
	defer resp.Body.Close()
	if err != nil {
		return err
	}
	return nil
}
`
	warning := checkWriteIntegrity("fetch.go", "", newGo)
	if warning == "" {
		t.Fatal("expected at least one warning, got empty string")
	}

	if !strings.Contains(warning, "http") && !strings.Contains(warning, "resp") {
		t.Errorf("warning should mention http-timeout or resp, got: %s", warning)
	}
}

func TestCheckWriteIntegrity_WarningCap(t *testing.T) {

	bad := "\x00\x00\x00"
	warning := checkWriteIntegrity("main.go", "package main\nfunc main(){}\n", bad)
	if warning == "" {
		t.Fatal("expected warning")
	}

	lines := strings.Split(warning, "\n")

	warningLines := lines[1:]

	indicator := 0
	for _, l := range warningLines {
		if strings.Contains(l, "more integrity warning(s) suppressed") {
			indicator++
		}
	}
	if indicator > 1 {
		t.Errorf("expected at most 1 suppression indicator, got %d", indicator)
	}
	if len(warningLines) > maxIntegrityWarnings+indicator {
		t.Errorf("expected at most %d warnings (plus indicator), got %d: %v", maxIntegrityWarnings, len(warningLines), warningLines)
	}
	if len(warningLines) == maxIntegrityWarnings+indicator && indicator == 1 {

		last := warningLines[len(warningLines)-1]
		if !strings.Contains(last, "2 more integrity warning(s) suppressed") {
			t.Errorf("indicator should report 2 suppressed warnings, got: %s", last)
		}
	}
}

func TestCheckWriteIntegrity_BarePanicSafety(t *testing.T) {

	src := `package mypkg

func validate(x int) int {
	if x < 0 {
		panic("negative value")
	}
	return x
}
`
	warning := checkWriteIntegrity("src/mypkg/validate.go", "", src)
	if warning == "" {
		t.Fatal("expected panic safety warning from checkWriteIntegrity")
	}
	if !strings.Contains(warning, "panic()") {
		t.Errorf("expected 'panic()' in warning, got: %s", warning)
	}
}

func TestCheckWriteIntegrity_HardcodedPathRegistered(t *testing.T) {

	old := "package mypkg\n\nfunc load() string { return \"\" }\n"
	newSrc := "package mypkg\n\nfunc load() string { return \"/Users/john/project/config.yaml\" }\n"
	warning := checkWriteIntegrity("src/mypkg/loader.go", old, newSrc)
	if warning == "" {
		t.Fatal("expected hardcoded-path warning from checkWriteIntegrity (detector must stay registered, see #516)")
	}
	if !strings.Contains(warning, "macOS home path") {
		t.Errorf("expected home path warning, got: %s", warning)
	}
}

func TestCheckWriteIntegrity_InterfaceDesignRegistered(t *testing.T) {

	oldCode := `package mypkg

type MyInterface interface {
	Method()
}

type MyImpl struct{}

func (m *MyImpl) Method() {}
`

	newCode := `package mypkg

type MyInterface interface {
	Method()
}

type MyImpl struct{}

func (m *MyImpl) Method() {}

type AnotherImpl struct{}

func (a *AnotherImpl) Method() {}
`
	warning := checkWriteIntegrity("src/mypkg/interfaces.go", oldCode, newCode)

	if strings.Contains(warning, "internal error") {
		t.Fatalf("interface-design check caused internal error (may not be properly registered): %s", warning)
	}
}

func TestCheckWriteIntegrityReadBack(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(p, []byte("actual-on-disk"), 0o644); err != nil {
		t.Fatal(err)
	}
	if w := checkWriteIntegrity(p, "old", "intended-new"); w == "" {
		t.Fatal("disk != written must surface a mismatch warning")
	}
	if err := os.WriteFile(p, []byte("intended-new"), 0o644); err != nil {
		t.Fatal(err)
	}

	if w := checkWriteIntegrity(p, "old", "intended-new"); w != "" {
		t.Fatalf("matching disk content must not warn, got %q", w)
	}
}
