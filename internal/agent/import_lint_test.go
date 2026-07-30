package agent

import (
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
