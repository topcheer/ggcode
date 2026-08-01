package agent

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// setupExportGuardRepo creates a temporary git repo with a Go file, commits it,
// and returns the working dir path. The caller is responsible for cleanup.
func setupExportGuardRepo(t *testing.T, initialContent string) string {
	t.Helper()
	dir := t.TempDir()

	// git init
	if err := exec.Command("git", "init", dir).Run(); err != nil {
		t.Fatalf("git init: %v", err)
	}
	// Configure user for commit
	exec.Command("git", "-C", dir, "config", "user.email", "test@test.com").Run()
	exec.Command("git", "-C", dir, "config", "user.name", "Test").Run()

	// Write initial Go file.
	goFile := filepath.Join(dir, "service.go")
	if err := os.WriteFile(goFile, []byte(initialContent), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	// Commit.
	exec.Command("git", "-C", dir, "add", ".").Run()
	exec.Command("git", "-C", dir, "commit", "-m", "initial").Run()

	return dir
}

func TestExportGuard_NoChange(t *testing.T) {
	initial := `package service

func Process(input string) error {
	return nil
}

type Config struct {
	Timeout int
}
`
	dir := setupExportGuardRepo(t, initial)

	old := gitHeadExportSymbols(dir, "service.go")
	if old == nil {
		t.Fatal("expected non-nil symbols from git HEAD")
	}

	// No changes — current file is identical to HEAD.
	current := parseExportedSymbols(filepath.Join(dir, "service.go"))
	changes := diffExportSymbols(old, current)
	if len(changes) != 0 {
		t.Errorf("expected 0 breaking changes, got %d: %+v", len(changes), changes)
	}
}

func TestExportGuard_RemovedExport(t *testing.T) {
	initial := `package service

func Process(input string) error {
	return nil
}

func Validate(data string) bool {
	return true
}

type Config struct {
	Timeout int
}
`
	dir := setupExportGuardRepo(t, initial)

	// Rewrite without the Validate function and Config type.
	edited := `package service

func Process(input string) error {
	return nil
}
`
	goFile := filepath.Join(dir, "service.go")
	if err := os.WriteFile(goFile, []byte(edited), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	old := gitHeadExportSymbols(dir, "service.go")
	current := parseExportedSymbols(goFile)
	changes := diffExportSymbols(old, current)

	if len(changes) != 2 {
		t.Fatalf("expected 2 breaking changes (removed Validate + Config), got %d: %+v", len(changes), changes)
	}

	symbols := make(map[string]string)
	for _, c := range changes {
		symbols[c.Symbol] = c.Kind
	}
	if symbols["Validate"] != "removed" {
		t.Errorf("expected Validate removed, got %v", symbols["Validate"])
	}
	if symbols["Config"] != "removed" {
		t.Errorf("expected Config removed, got %v", symbols["Config"])
	}
}

func TestExportGuard_SignatureChanged(t *testing.T) {
	initial := `package service

func Process(input string) error {
	return nil
}
`
	dir := setupExportGuardRepo(t, initial)

	// Change the signature — add a parameter and change return type.
	edited := `package service

func Process(input string, verbose bool) (string, error) {
	return "", nil
}
`
	goFile := filepath.Join(dir, "service.go")
	if err := os.WriteFile(goFile, []byte(edited), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	old := gitHeadExportSymbols(dir, "service.go")
	current := parseExportedSymbols(goFile)
	changes := diffExportSymbols(old, current)

	if len(changes) != 1 {
		t.Fatalf("expected 1 breaking change, got %d: %+v", len(changes), changes)
	}
	if changes[0].Kind != "signature-changed" {
		t.Errorf("expected signature-changed, got %s", changes[0].Kind)
	}
	if changes[0].Symbol != "Process" {
		t.Errorf("expected Process, got %s", changes[0].Symbol)
	}
}

func TestExportGuard_AddedExportNotBreaking(t *testing.T) {
	initial := `package service

func Process(input string) error {
	return nil
}
`
	dir := setupExportGuardRepo(t, initial)

	// Add a new export — not breaking.
	edited := `package service

func Process(input string) error {
	return nil
}

func New() *Service {
	return nil
}
`
	goFile := filepath.Join(dir, "service.go")
	if err := os.WriteFile(goFile, []byte(edited), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	old := gitHeadExportSymbols(dir, "service.go")
	current := parseExportedSymbols(goFile)
	changes := diffExportSymbols(old, current)

	if len(changes) != 0 {
		t.Errorf("expected 0 breaking changes for added export, got %d: %+v", len(changes), changes)
	}
}

func TestExportGuard_MethodRemoved(t *testing.T) {
	initial := `package service

type Handler struct{}

func (h *Handler) Serve(data string) error {
	return nil
}

func (h *Handler) Close() {}
`
	dir := setupExportGuardRepo(t, initial)

	// Remove the Close method.
	edited := `package service

type Handler struct{}

func (h *Handler) Serve(data string) error {
	return nil
}
`
	goFile := filepath.Join(dir, "service.go")
	if err := os.WriteFile(goFile, []byte(edited), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	old := gitHeadExportSymbols(dir, "service.go")
	current := parseExportedSymbols(goFile)
	changes := diffExportSymbols(old, current)

	found := false
	for _, c := range changes {
		if c.Symbol == "Handler.Close" && c.Kind == "removed" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected Handler.Close removed, got changes: %+v", changes)
	}
}

func TestExportGuard_InterfaceRemoved(t *testing.T) {
	initial := `package service

type Processor interface {
	Process(input string) error
}
`
	dir := setupExportGuardRepo(t, initial)

	// Remove the interface entirely.
	edited := `package service

type Processor struct{}
`
	goFile := filepath.Join(dir, "service.go")
	if err := os.WriteFile(goFile, []byte(edited), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	old := gitHeadExportSymbols(dir, "service.go")
	current := parseExportedSymbols(goFile)
	changes := diffExportSymbols(old, current)

	// The Processor symbol changes from "interface" to "struct" kind.
	// Since we key on name+kind, the interface is "removed" and struct is "added".
	found := false
	for _, c := range changes {
		if c.Symbol == "Processor" && c.Kind == "removed" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected Processor interface removed, got: %+v", changes)
	}
}

func TestExportGuard_FormatWarning(t *testing.T) {
	changes := []breakingChange{
		{Symbol: "Foo", Kind: "removed", Detail: "func"},
		{Symbol: "Bar", Kind: "signature-changed", Detail: "(string)(error) → (string)(int,error)"},
	}
	warning := formatBreakingChangeWarning("service.go", changes)

	if warning == "" {
		t.Fatal("expected non-empty warning")
	}
	if !containsStr(warning, "REMOVED: Foo") {
		t.Errorf("warning should mention removed Foo: %s", warning)
	}
	if !containsStr(warning, "CHANGED: Bar") {
		t.Errorf("warning should mention changed Bar: %s", warning)
	}
	if !containsStr(warning, "Search for references") {
		t.Errorf("warning should advise searching for references: %s", warning)
	}
}

func TestExportGuard_ResetClearsChecked(t *testing.T) {
	s := newExportGuardState()
	s.checked["foo.go"] = true
	s.reset()
	if len(s.checked) != 0 {
		t.Errorf("reset should clear checked map, got %d entries", len(s.checked))
	}
}

func TestExportGuard_ConstAndVarRemoval(t *testing.T) {
	initial := `package service

const MaxRetries = 5

var DefaultConfig = "default"
`
	dir := setupExportGuardRepo(t, initial)

	edited := `package service

const NewMaxRetries = 10
`
	goFile := filepath.Join(dir, "service.go")
	if err := os.WriteFile(goFile, []byte(edited), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	old := gitHeadExportSymbols(dir, "service.go")
	current := parseExportedSymbols(goFile)
	changes := diffExportSymbols(old, current)

	// MaxRetries and DefaultConfig removed; NewMaxRetries added (not breaking).
	symbols := make(map[string]string)
	for _, c := range changes {
		symbols[c.Symbol] = c.Kind
	}
	if symbols["MaxRetries"] != "removed" {
		t.Errorf("expected MaxRetries removed, got: %+v", symbols)
	}
	if symbols["DefaultConfig"] != "removed" {
		t.Errorf("expected DefaultConfig removed, got: %+v", symbols)
	}
}
