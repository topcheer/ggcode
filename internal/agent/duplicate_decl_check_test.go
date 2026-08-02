package agent

import (
	"strings"
	"testing"
)

func TestCheckDuplicateDeclarations_GoDuplicateFunction(t *testing.T) {
	old := `package main

func foo() int { return 1 }
func bar() int { return 2 }
`
	newContent := `package main

func foo() int { return 1 }
func bar() int { return 2 }
func foo() int { return 3 } // duplicate!
`
	result := checkDuplicateDeclarations("main.go", old, newContent)
	if result == "" {
		t.Fatal("expected duplicate function warning, got empty")
	}
	if !strings.Contains(result, "function") || !strings.Contains(result, "foo") {
		t.Errorf("expected warning about duplicate function foo, got: %s", result)
	}
}

func TestCheckDuplicateDeclarations_GoDuplicateMethod(t *testing.T) {
	old := `package main

type Server struct{}

func (s *Server) Start() error { return nil }
func (s *Server) Stop() error  { return nil }
`
	newContent := `package main

type Server struct{}

func (s *Server) Start() error { return nil }
func (s *Server) Stop() error  { return nil }
func (s *Server) Start() error { return nil } // duplicate method
`
	result := checkDuplicateDeclarations("server.go", old, newContent)
	if result == "" {
		t.Fatal("expected duplicate method warning, got empty")
	}
	if !strings.Contains(result, "method") {
		t.Errorf("expected warning about duplicate method, got: %s", result)
	}
}

func TestCheckDuplicateDeclarations_GoDuplicateType(t *testing.T) {
	old := `package main

type Config struct {
	Port int
}
`
	newContent := `package main

type Config struct {
	Port int
}

type Config struct { // duplicate type
	Port int
	Host string
}
`
	result := checkDuplicateDeclarations("config.go", old, newContent)
	if result == "" {
		t.Fatal("expected duplicate type warning, got empty")
	}
	if !strings.Contains(result, "type") || !strings.Contains(result, "Config") {
		t.Errorf("expected warning about duplicate type Config, got: %s", result)
	}
}

func TestCheckDuplicateDeclarations_GoDuplicateConst(t *testing.T) {
	old := `package main

const MaxRetries = 3
`
	newContent := `package main

const MaxRetries = 3
const MaxRetries = 5 // duplicate!
`
	result := checkDuplicateDeclarations("constants.go", old, newContent)
	if result == "" {
		t.Fatal("expected duplicate const warning, got empty")
	}
	if !strings.Contains(result, "MaxRetries") {
		t.Errorf("expected warning about duplicate const MaxRetries, got: %s", result)
	}
}

func TestCheckDuplicateDeclarations_NoFalsePositiveDifferentMethods(t *testing.T) {
	// Methods with the same name on different types are NOT duplicates.
	old := `package main

type Dog struct{}
type Cat struct{}

func (d *Dog) Speak() string { return "woof" }
`
	newContent := `package main

type Dog struct{}
type Cat struct{}

func (d *Dog) Speak() string { return "woof" }
func (c *Cat) Speak() string { return "meow" } // NOT a duplicate — different receiver
`
	result := checkDuplicateDeclarations("animals.go", old, newContent)
	if result != "" {
		t.Errorf("expected no duplicate warning for methods on different types, got: %s", result)
	}
}

func TestCheckDuplicateDeclarations_PreExistingDuplicateNotFlagged(t *testing.T) {
	// If the old content already had duplicates, don't re-flag them
	// (they're pre-existing, not introduced by this edit).
	old := `package main

func foo() {}
func foo() {}
`
	newContent := `package main

func foo() {}
func foo() {}
`
	result := checkDuplicateDeclarations("main.go", old, newContent)
	if result != "" {
		t.Errorf("expected no warning for pre-existing duplicates, got: %s", result)
	}
}

func TestCheckDuplicateDeclarations_NoChangeNoFlag(t *testing.T) {
	old := `package main

func unique() {}
func other() {}
`
	newContent := old
	result := checkDuplicateDeclarations("main.go", old, newContent)
	if result != "" {
		t.Errorf("expected no warning when no duplicates exist, got: %s", result)
	}
}

func TestCheckDuplicateDeclarations_TestFileSkipped(t *testing.T) {
	old := `package main

func helper() {}
`
	newContent := `package main

func helper() {}
func helper() {}
`
	result := checkDuplicateDeclarations("main_test.go", old, newContent)
	if result != "" {
		t.Errorf("expected test files to be skipped, got: %s", result)
	}
}

func TestCheckDuplicateDeclarations_EmptyContent(t *testing.T) {
	result := checkDuplicateDeclarations("main.go", "", "")
	if result != "" {
		t.Errorf("expected empty result for empty content, got: %s", result)
	}
}

// --- Python tests ---

func TestCheckDuplicateDeclarations_PythonDuplicateFunction(t *testing.T) {
	old := `def process(data):
    return data

def validate(item):
    return True
`
	newContent := `def process(data):
    return data

def validate(item):
    return True

def process(data):  # duplicate!
    return data + 1
`
	result := checkDuplicateDeclarations("app.py", old, newContent)
	if result == "" {
		t.Fatal("expected duplicate function warning for Python, got empty")
	}
	if !strings.Contains(result, "function") || !strings.Contains(result, "process") {
		t.Errorf("expected warning about duplicate function process, got: %s", result)
	}
}

func TestCheckDuplicateDeclarations_PythonDuplicateClass(t *testing.T) {
	old := `class User:
    pass
`
	newContent := `class User:
    pass

class User:  # duplicate!
    pass
`
	result := checkDuplicateDeclarations("models.py", old, newContent)
	if result == "" {
		t.Fatal("expected duplicate class warning for Python, got empty")
	}
	if !strings.Contains(result, "class") || !strings.Contains(result, "User") {
		t.Errorf("expected warning about duplicate class User, got: %s", result)
	}
}

// --- JavaScript/TypeScript tests ---

func TestCheckDuplicateDeclarations_JSDuplicateFunction(t *testing.T) {
	old := `function calculate(x) {
    return x * 2;
}
`
	newContent := `function calculate(x) {
    return x * 2;
}

function calculate(x) {  // duplicate!
    return x * 3;
}
`
	result := checkDuplicateDeclarations("utils.js", old, newContent)
	if result == "" {
		t.Fatal("expected duplicate function warning for JS, got empty")
	}
	if !strings.Contains(result, "calculate") {
		t.Errorf("expected warning about duplicate function calculate, got: %s", result)
	}
}

func TestCheckDuplicateDeclarations_TSDuplicateClass(t *testing.T) {
	old := `export class Config {
    port: number;
}
`
	newContent := `export class Config {
    port: number;
}

export class Config {  // duplicate!
    port: number;
    host: string;
}
`
	result := checkDuplicateDeclarations("config.ts", old, newContent)
	if result == "" {
		t.Fatal("expected duplicate class warning for TS, got empty")
	}
}

func TestCheckDuplicateDeclarations_UnsupportedLanguage(t *testing.T) {
	result := checkDuplicateDeclarations("Makefile", "", "all: build\n")
	if result != "" {
		t.Errorf("expected empty result for unsupported language, got: %s", result)
	}
}

// --- Integration test via checkWriteIntegrity ---

func TestCheckWriteIntegrity_DuplicateDeclWarning(t *testing.T) {
	old := `package main

func handler() error { return nil }
`
	newContent := `package main

func handler() error { return nil }
func handler() error { return nil } // duplicate
`
	result := checkWriteIntegrity("handler.go", old, newContent)
	if result == "" {
		t.Fatal("expected write integrity warning for duplicate declaration")
	}
	if !strings.Contains(result, "Duplicate") {
		t.Errorf("expected duplicate warning in integrity check, got: %s", result)
	}
}
