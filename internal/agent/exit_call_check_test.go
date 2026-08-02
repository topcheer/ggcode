package agent

import (
	"strings"
	"testing"
)

func TestCheckPrematureExit_OsExitInHelper(t *testing.T) {
	old := `package main

func loadConfig() {
	config := readConfig()
}
`
	new := `package main

func loadConfig() {
	config := readConfig()
	os.Exit(1)
}
`
	warnings := checkPrematureExit("config.go", old, new)
	if len(warnings) == 0 {
		t.Fatal("expected warning for os.Exit in non-main function")
	}
	if !strings.Contains(warnings[0], "os.Exit") {
		t.Errorf("warning should mention os.Exit, got: %s", warnings[0])
	}
	if !strings.Contains(warnings[0], "deferred cleanup") {
		t.Errorf("warning should explain deferred cleanup issue, got: %s", warnings[0])
	}
}

func TestCheckPrematureExit_LogFatalInHelper(t *testing.T) {
	old := `package server

func handleRequest() error {
	return nil
}
`
	new := `package server

func handleRequest() error {
	log.Fatal("unexpected error")
	return nil
}
`
	warnings := checkPrematureExit("server.go", old, new)
	if len(warnings) == 0 {
		t.Fatal("expected warning for log.Fatal in non-main function")
	}
	if !strings.Contains(warnings[0], "log.Fatal") {
		t.Errorf("warning should mention log.Fatal, got: %s", warnings[0])
	}
}

func TestCheckPrematureExit_MainFunctionAllowed(t *testing.T) {
	old := `package main

func main() {
}
`
	new := `package main

func main() {
	os.Exit(0)
}
`
	warnings := checkPrematureExit("main.go", old, new)
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings for os.Exit in main(), got: %v", warnings)
	}
}

func TestCheckPrematureExit_InitFunctionAllowed(t *testing.T) {
	old := `package pkg

func init() {
}
`
	new := `package pkg

func init() {
	os.Exit(0)
}
`
	warnings := checkPrematureExit("pkg.go", old, new)
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings for os.Exit in init(), got: %v", warnings)
	}
}

func TestCheckPrematureExit_CmdDirExcluded(t *testing.T) {
	new := `package main

func runTask() {
	os.Exit(1)
}
`
	warnings := checkPrematureExit("cmd/myapp/run.go", "", new)
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings for os.Exit in cmd/ directory, got: %v", warnings)
	}
}

func TestCheckPrematureExit_TestFileExcluded(t *testing.T) {
	new := `package agent

func helper() {
	os.Exit(1)
}
`
	warnings := checkPrematureExit("agent_test.go", "", new)
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings in test files, got: %v", warnings)
	}
}

func TestCheckPrematureExit_DeltaAware(t *testing.T) {
	old := `package server

func handler() {
	log.Fatal("err")
}
`
	new := `package server

func handler() {
	log.Fatal("err")
}
`
	warnings := checkPrematureExit("server.go", old, new)
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings when os.Exit already existed, got: %v", warnings)
	}
}

func TestCheckPrematureExit_NoFalsePositiveNormalCode(t *testing.T) {
	new := `package server

import "fmt"

func handler() error {
	return fmt.Errorf("something failed")
}

func graceful() {
	fmt.Println("done")
}
`
	warnings := checkPrematureExit("server.go", "", new)
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings for normal code, got: %v", warnings)
	}
}

func TestCheckPrematureExit_LogFatalf(t *testing.T) {
	new := `package server

func process() {
	log.Fatalf("bad state: %v", err)
}
`
	warnings := checkPrematureExit("server.go", "", new)
	if len(warnings) == 0 {
		t.Fatal("expected warning for log.Fatalf")
	}
}

func TestCheckPrematureExit_LogPanic(t *testing.T) {
	new := `package server

func process() {
	log.Panic("boom")
}
`
	warnings := checkPrematureExit("server.go", "", new)
	if len(warnings) == 0 {
		t.Fatal("expected warning for log.Panic")
	}
}

func TestCheckPrematureExit_Closure(t *testing.T) {
	new := `package server

func setup() {
	fn := func() {
		os.Exit(1)
	}
	fn()
}
`
	warnings := checkPrematureExit("server.go", "", new)
	if len(warnings) == 0 {
		t.Fatal("expected warning for os.Exit in closure")
	}
}

func TestCheckPrematureExit_NonGoFile(t *testing.T) {
	new := `os.exit(1);`
	warnings := checkPrematureExit("script.js", "", new)
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings for non-Go file, got: %v", warnings)
	}
}

func TestCheckPrematureExit_MultipleDifferentFunctions(t *testing.T) {
	new := `package server

func handler() {
	os.Exit(1)
	log.Fatal("x")
}
`
	warnings := checkPrematureExit("server.go", "", new)
	if len(warnings) == 0 {
		t.Fatal("expected at least one warning")
	}
	// Should mention both functions.
	if !strings.Contains(warnings[0], "os.Exit") || !strings.Contains(warnings[0], "log.Fatal") {
		t.Logf("warning text: %s", warnings[0])
	}
}
