package agent

import (
	"testing"
)

func TestCheckDeprecatedAPI_IoutilReadFile(t *testing.T) {
	old := `package main
import "fmt"
func main() { fmt.Println("hello") }
`
	new := `package main
import (
	"fmt"
	"io/ioutil"
)
func main() {
	data, err := ioutil.ReadFile("test.txt")
	fmt.Println(string(data), err)
}
`
	result := checkDeprecatedAPI("test.go", old, new)
	if result == "" {
		t.Fatal("expected deprecated API warning for ioutil import and ReadFile")
	}
	if !contains(result, "io/ioutil") {
		t.Errorf("expected io/ioutil in warning, got: %s", result)
	}
	if !contains(result, "deprecated") {
		t.Errorf("expected 'deprecated' in warning, got: %s", result)
	}
	if !contains(result, "os.ReadFile") {
		t.Errorf("expected os.ReadFile replacement suggestion, got: %s", result)
	}
}

func TestCheckDeprecatedAPI_RandSeed(t *testing.T) {
	old := `package main
func init() {}
`
	new := `package main
import "math/rand"
func init() {
	rand.Seed(42)
}
`
	result := checkDeprecatedAPI("test.go", old, new)
	if result == "" {
		t.Fatal("expected deprecated API warning for rand.Seed")
	}
	if !contains(result, "rand.Seed") {
		t.Errorf("expected rand.Seed in warning, got: %s", result)
	}
}

func TestCheckDeprecatedAPI_StringsTitle(t *testing.T) {
	old := `package main
func process(s string) string { return s }
`
	new := `package main
import "strings"
func process(s string) string {
	return strings.Title(s)
}
`
	result := checkDeprecatedAPI("test.go", old, new)
	if result == "" {
		t.Fatal("expected deprecated API warning for strings.Title")
	}
	if !contains(result, "strings.Title") {
		t.Errorf("expected strings.Title in warning")
	}
}

func TestCheckDeprecatedAPI_OSSeekConstants(t *testing.T) {
	old := `package main
import "os"
func seek(f *os.File) { f.Seek(0, 0) }
`
	new := `package main
import "os"
func seek(f *os.File) {
	f.Seek(0, os.SEEK_SET)
}
`
	result := checkDeprecatedAPI("test.go", old, new)
	if result == "" {
		t.Fatal("expected deprecated API warning for os.SEEK_SET")
	}
	if !contains(result, "os.SEEK_SET") {
		t.Errorf("expected os.SEEK_SET in warning")
	}
}

func TestCheckDeprecatedAPI_NoDeprecated(t *testing.T) {
	new := `package main
import (
	"fmt"
	"os"
)
func main() {
	data, err := os.ReadFile("test.txt")
	fmt.Println(string(data), err)
}
`
	result := checkDeprecatedAPI("test.go", "", new)
	if result != "" {
		t.Errorf("expected no warning for modern API usage, got: %s", result)
	}
}

func TestCheckDeprecatedAPI_DeltaAware(t *testing.T) {
	// ioutil already in old content should NOT be flagged
	content := `package main
import (
	"io/ioutil"
)
func read() {
	data, _ := ioutil.ReadFile("test.txt")
	_ = data
}
`
	result := checkDeprecatedAPI("test.go", content, content)
	if result != "" {
		t.Errorf("expected no warning for pre-existing deprecated API (delta-aware), got: %s", result)
	}
}

func TestCheckDeprecatedAPI_NonGoFile(t *testing.T) {
	result := checkDeprecatedAPI("test.py", "", `import os`)
	if result != "" {
		t.Errorf("expected empty result for non-Go file, got: %s", result)
	}
}

func TestCheckDeprecatedAPI_SyntaxError(t *testing.T) {
	new := `package main
import "io/ioutil"
this is not valid go
`
	result := checkDeprecatedAPI("test.go", "", new)
	if result != "" {
		t.Errorf("expected empty result for file with syntax errors, got: %s", result)
	}
}

func TestCheckDeprecatedAPI_IoutilAliased(t *testing.T) {
	// Test ioutil with custom alias
	old := `package main
func read() {}
`
	new := `package main
import iout "io/ioutil"
func read() {
	_ = iout.ReadFile("test.txt")
}
`
	result := checkDeprecatedAPI("test.go", old, new)
	if result == "" {
		t.Fatal("expected warning for aliased ioutil import")
	}
	if !contains(result, "io/ioutil") {
		t.Errorf("expected io/ioutil in warning, got: %s", result)
	}
}

func TestCheckDeprecatedAPI_IoutilAllFunctions(t *testing.T) {
	functions := []string{"ReadFile", "WriteFile", "ReadAll", "ReadDir", "TempFile", "TempDir", "NopCloser", "Discard"}
	for _, fn := range functions {
		old := "package main\n"
		new := "package main\nimport \"io/ioutil\"\nfunc main() {\n  _ = ioutil." + fn + "\n}"
		// Use ioutil directly without import for non-ReadFile/WriteFile
		new = "package main\nimport (\n\"io/ioutil\"\n\"bytes\"\n)\nfunc main() {\n  _, _ = ioutil." + fn + "(nil)\n  _ = bytes.NewBuffer(nil)\n}"
		result := checkDeprecatedAPI("test.go", old, new)
		// At minimum, the import itself should be flagged
		if result == "" {
			t.Errorf("expected warning for ioutil.%s", fn)
		}
	}
}

// contains is defined in reflection_test.go
