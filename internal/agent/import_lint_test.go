package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckGoImports_UnusedImport(t *testing.T) {
	src := `package main

import (
	"fmt"
	"strings"
)

func main() {
	fmt.Println("hello")
}
`
	warnings := checkGoImports("main.go", src)
	if len(warnings) == 0 {
		t.Fatal("expected unused import warning for strings")
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "Unused import") && strings.Contains(w, "strings") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected unused import warning for 'strings', got: %v", warnings)
	}
}

func TestCheckGoImports_AllImportsUsed(t *testing.T) {
	src := `package main

import (
	"fmt"
	"strings"
)

func main() {
	fmt.Println(strings.ToUpper("hello"))
}
`
	warnings := checkGoImports("main.go", src)
	for _, w := range warnings {
		if strings.Contains(w, "Unused import") {
			t.Errorf("unexpected unused import warning when all imports are used: %s", w)
		}
	}
}

func TestCheckGoImports_MissingImport(t *testing.T) {
	src := `package main

func main() {
	x := strconv.Itoa(42)
	_ = x
}
`
	warnings := checkGoImports("main.go", src)
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "Likely missing import") && strings.Contains(w, "strconv") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected missing import warning for strconv, got: %v", warnings)
	}
}

func TestCheckGoImports_MissingImportAlreadyImported(t *testing.T) {
	src := `package main

import (
	"strconv"
)

func main() {
	x := strconv.Itoa(42)
	_ = x
}
`
	warnings := checkGoImports("main.go", src)
	for _, w := range warnings {
		if strings.Contains(w, "missing import") {
			t.Errorf("should not flag strconv as missing when already imported: %s", w)
		}
	}
}

func TestCheckGoImports_BlankImportNotFlagged(t *testing.T) {
	src := `package main

import (
	_ "image/png"
)

func main() {}
`
	warnings := checkGoImports("main.go", src)
	for _, w := range warnings {
		if strings.Contains(w, "Unused import") {
			t.Errorf("blank import should not be flagged as unused: %s", w)
		}
	}
}

func TestCheckGoImports_DotImportNotFlagged(t *testing.T) {
	src := `package main

import (
	. "fmt"
)

func main() {
	Println("hello")
}
`
	warnings := checkGoImports("main.go", src)
	for _, w := range warnings {
		if strings.Contains(w, "Unused import") {
			t.Errorf("dot import should not be flagged as unused: %s", w)
		}
	}
}

func TestCheckGoImports_AliasedImport(t *testing.T) {
	src := `package main

import (
	myfmt "fmt"
)

func main() {
	myfmt.Println("hello")
}
`
	warnings := checkGoImports("main.go", src)
	for _, w := range warnings {
		if strings.Contains(w, "Unused import") {
			t.Errorf("aliased import should not be flagged as unused: %s", w)
		}
	}
}

func TestCheckGoImports_UnusedAliasedImport(t *testing.T) {
	src := `package main

import (
	myfmt "fmt"
	"os"
)

func main() {
	os.Exit(0)
}
`
	warnings := checkGoImports("main.go", src)
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "Unused import") && strings.Contains(w, "myfmt") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected unused import warning for myfmt alias, got: %v", warnings)
	}
}

func TestCheckGoImports_SyntaxErrorSkipped(t *testing.T) {
	src := `package main

import "fmt"

func broken( {
	fmt.Println("hello")
}
`
	warnings := checkGoImports("main.go", src)
	if warnings != nil {
		t.Errorf("should return nil for syntax errors, got: %v", warnings)
	}
}

func TestCheckGoImports_NoFalsePositiveLocalVar(t *testing.T) {
	// 'json' as a local variable name should not trigger missing import warning
	// if not used as a package qualifier
	src := `package main

func main() {
	json := map[string]interface{}{"key": "val"}
	_ = json
}
`
	warnings := checkGoImports("main.go", src)
	// 'json' is used as an assignment target, not as a package qualifier.
	// SelectorExpr detection should not fire since json["key"] uses index, not selection.
	for _, w := range warnings {
		if strings.Contains(w, "missing import") && strings.Contains(w, "json") {
			t.Errorf("local variable 'json' should not trigger missing import: %s", w)
		}
	}
}

func TestCheckGoImports_IntegrationWithWriteIntegrity(t *testing.T) {
	// Test that checkWriteIntegrity picks up import issues
	src := `package main

import (
	"fmt"
	"strings"
)

func main() {
	fmt.Println("hello")
}
`
	warning := checkWriteIntegrity("main.go", "", src)
	if warning == "" {
		t.Fatal("expected write integrity warning for unused import")
	}
	if !strings.Contains(warning, "Unused import") {
		t.Errorf("warning should mention unused import, got: %s", warning)
	}
}

func TestCheckGoImports_EmptyFile(t *testing.T) {
	warnings := checkGoImports("main.go", "")
	if warnings != nil {
		t.Errorf("expected nil for empty file, got: %v", warnings)
	}
}

func TestCheckGoImports_NonGoFile(t *testing.T) {
	// Should be called with Go files only, but verify it handles gracefully
	warnings := checkGoImports("main.py", "print('hello')")
	if warnings != nil {
		t.Errorf("expected nil for non-parseable content, got: %v", warnings)
	}
}

func TestIsVersionSegment(t *testing.T) {
	cases := map[string]bool{
		"v2":       true,
		"v3":       true,
		"v10":      true,
		"v":        false,
		"vA":       false,
		"lipgloss": false,
		"fmt":      false,
		"":         false,
	}
	for input, expected := range cases {
		got := isVersionSegment(input)
		if got != expected {
			t.Errorf("isVersionSegment(%q) = %v, want %v", input, got, expected)
		}
	}
}

func TestCheckGoImports_VersionedPathNoFalsePositive(t *testing.T) {
	// "charm.land/lipgloss/v2" has path segment "v2" which is NOT the package name.
	// The code uses "lipgloss.Width()" but the analyzer would see "v2" as the name
	// and incorrectly report it as unused. This test verifies the fix.
	src := `package main

import (
	"charm.land/lipgloss/v2"
)

func main() {
	_ = lipgloss.Width("hello")
}
`
	warnings := checkGoImports("main.go", src)
	for _, w := range warnings {
		if strings.Contains(w, "Unused import") && strings.Contains(w, "v2") {
			t.Errorf("versioned path should not trigger false unused import: %s", w)
		}
	}
}

func TestParseGoModRequires(t *testing.T) {
	content := `module github.com/topcheer/ggcode

go 1.26

require (
	github.com/charmbracelet/lipgloss/v2 v2.0.0
	github.com/sirupsen/logrus v1.9.0
	golang.org/x/mod v0.20.0 // indirect
	github.com/topcheer/ggcode/internal v0.0.0
)
`
	result := parseGoModRequires(content, "github.com/topcheer/ggcode")
	// lipgloss from a versioned module path
	if p, ok := result["lipgloss"]; !ok {
		t.Errorf("expected lipgloss in go.mod import map, got: %v", result)
	} else if p != "github.com/charmbracelet/lipgloss/v2" {
		t.Errorf("lipgloss path = %q, want github.com/charmbracelet/lipgloss/v2", p)
	}
	// logrus from a direct module path
	if p, ok := result["logrus"]; !ok {
		t.Errorf("expected logrus in go.mod import map")
	} else if p != "github.com/sirupsen/logrus" {
		t.Errorf("logrus path = %q, want github.com/sirupsen/logrus", p)
	}
	// indirect deps should be skipped
	if _, ok := result["mod"]; ok {
		t.Errorf("indirect dep golang.org/x/mod should not be in import map")
	}
	// internal modules should be skipped
	if _, ok := result["internal"]; ok {
		t.Errorf("internal module should not be in import map")
	}
}

func TestLoadGoModImports_Caching(t *testing.T) {
	dir := t.TempDir()
	goMod := filepath.Join(dir, "go.mod")
	err := os.WriteFile(goMod, []byte("module test.example/mymod\n\ngo 1.26\n\nrequire (\n\tgithub.com/sirupsen/logrus v1.9.0\n)\n"), 0644)
	if err != nil {
		t.Fatal(err)
	}
	// Clear cache for this dir
	goModCacheMu.Lock()
	delete(goModCacheStore, dir)
	goModCacheMu.Unlock()

	result := loadGoModImports(dir)
	if result == nil {
		t.Fatal("expected non-nil import map from go.mod")
	}
	if p, ok := result["logrus"]; !ok || p != "github.com/sirupsen/logrus" {
		t.Errorf("logrus not found correctly in go.mod import map: %v", result)
	}

	// Second call should use cache and return same result
	result2 := loadGoModImports(dir)
	if p, ok := result2["logrus"]; !ok || p != "github.com/sirupsen/logrus" {
		t.Errorf("cached call returned different result: %v", result2)
	}
}

func TestLoadGoModImports_NoGoMod(t *testing.T) {
	dir := t.TempDir()
	goModCacheMu.Lock()
	delete(goModCacheStore, dir)
	goModCacheMu.Unlock()
	result := loadGoModImports(dir)
	if result != nil {
		t.Errorf("expected nil result when no go.mod exists, got: %v", result)
	}
}

func TestCheckGoImportsWithDir_MissingThirdPartyImport(t *testing.T) {
	dir := t.TempDir()
	goMod := filepath.Join(dir, "go.mod")
	// Create a go.mod with a third-party dependency
	err := os.WriteFile(goMod, []byte(`module test.example/mymod

go 1.26

require (
	github.com/sirupsen/logrus v1.9.0
)
`), 0644)
	if err != nil {
		t.Fatal(err)
	}

	// Clear cache
	goModCacheMu.Lock()
	delete(goModCacheStore, dir)
	goModCacheMu.Unlock()

	// Source that uses logrus without importing it
	src := `package main

func main() {
	_ = logrus.New()
}
`
	warnings := checkGoImportsWithDir("main.go", src, dir)
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "Likely missing import") && strings.Contains(w, "logrus") {
			found = true
			if !strings.Contains(w, "github.com/sirupsen/logrus") {
				t.Errorf("warning should suggest github.com/sirupsen/logrus, got: %s", w)
			}
		}
	}
	if !found {
		t.Errorf("expected missing import warning for logrus, got: %v", warnings)
	}
}

func TestCheckGoImportsWithDir_NoFalsePositiveForLowercase(t *testing.T) {
	dir := t.TempDir()
	goMod := filepath.Join(dir, "go.mod")
	err := os.WriteFile(goMod, []byte(`module test.example/mymod

go 1.26

require (
	github.com/somepkg/logger v1.0.0
)
`), 0644)
	if err != nil {
		t.Fatal(err)
	}
	goModCacheMu.Lock()
	delete(goModCacheStore, dir)
	goModCacheMu.Unlock()

	// Source uses logger as a package reference without importing it.
	// Since logger is in go.mod, this SHOULD be detected as missing import.
	src := `package main

func main() {
	_ = logger.Println("hello")
}
`
	warnings := checkGoImportsWithDir("main.go", src, dir)
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "Likely missing import") && strings.Contains(w, "logger") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected missing import for logger, got: %v", warnings)
	}
}

func TestCheckGoImportsWithDir_EmptyWorkingDir(t *testing.T) {
	// With empty workingDir, should behave exactly like checkGoImports (stdlib only)
	src := `package main

func main() {
	_ = fmt.Println("hello")
}
`
	warnings := checkGoImportsWithDir("main.go", src, "")
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "Likely missing import") && strings.Contains(w, "fmt") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected missing import warning for fmt with empty workingDir")
	}
}
