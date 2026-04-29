package lsp

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// lspIntegrationTestDir creates a temporary Go module with a sample file
// for LSP integration tests. Returns the workspace dir and file path.
func lspIntegrationTestDir(t *testing.T) (workspace, goFile string) {
	t.Helper()

	workspace = t.TempDir()

	// Create go.mod
	if err := os.WriteFile(filepath.Join(workspace, "go.mod"), []byte("module testlsp\n\ngo 1.22\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Create a simple Go file with various symbols to query
	code := `package testlsp

import "fmt"

// Greeter says hello
type Greeter struct {
	Name string
}

// Greet returns a greeting
func (g *Greeter) Greet() string {
	return fmt.Sprintf("Hello, %s!", g.Name)
}

// SayHi is a free function
func SayHi(name string) string {
	g := &Greeter{Name: name}
	return g.Greet()
}
`
	goFile = filepath.Join(workspace, "sample.go")
	if err := os.WriteFile(goFile, []byte(code), 0644); err != nil {
		t.Fatal(err)
	}

	// Run `go mod tidy` so gopls is happy
	cmd := exec.Command("go", "mod", "tidy")
	cmd.Dir = workspace
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go mod tidy failed: %v\n%s", err, out)
	}

	return workspace, goFile
}

// skipIfNoGopls skips the test if gopls is not available.
func skipIfNoGopls(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls not found in PATH, skipping LSP integration test")
	}
}

func TestLSP_Hover_Integration(t *testing.T) {
	skipIfNoGopls(t)
	workspace, goFile := lspIntegrationTestDir(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Hover over "Greeter" on line 6, char 6 (type name)
	result, err := Hover(ctx, workspace, goFile, Position{Line: 6, Character: 6})
	if err != nil {
		t.Fatalf("Hover failed: %v", err)
	}
	if result == "" {
		t.Error("expected non-empty hover result for Greeter")
	}
	t.Logf("Hover result: %s", result)

	// Clean up global session so other tests don't reuse it
	globalSessions.mu.Lock()
	for k, s := range globalSessions.sessions {
		if s.workspace == workspace {
			s.close()
			delete(globalSessions.sessions, k)
		}
	}
	globalSessions.mu.Unlock()
}

func TestLSP_Definition_Integration(t *testing.T) {
	skipIfNoGopls(t)
	workspace, goFile := lspIntegrationTestDir(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Definition of "Greeter" on line 18 (in SayHi function)
	locs, err := Definition(ctx, workspace, goFile, Position{Line: 18, Character: 11})
	if err != nil {
		t.Fatalf("Definition failed: %v", err)
	}
	if len(locs) == 0 {
		t.Error("expected at least one definition location")
	} else {
		t.Logf("Definition: %+v", locs[0])
	}

	globalSessions.mu.Lock()
	for k, s := range globalSessions.sessions {
		if s.workspace == workspace {
			s.close()
			delete(globalSessions.sessions, k)
		}
	}
	globalSessions.mu.Unlock()
}

func TestLSP_References_Integration(t *testing.T) {
	skipIfNoGopls(t)
	workspace, goFile := lspIntegrationTestDir(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// References to "Greet" method on line 12
	locs, err := References(ctx, workspace, goFile, Position{Line: 12, Character: 16})
	if err != nil {
		t.Fatalf("References failed: %v", err)
	}
	if len(locs) == 0 {
		t.Error("expected at least one reference to Greet")
	} else {
		t.Logf("Found %d references", len(locs))
	}

	globalSessions.mu.Lock()
	for k, s := range globalSessions.sessions {
		if s.workspace == workspace {
			s.close()
			delete(globalSessions.sessions, k)
		}
	}
	globalSessions.mu.Unlock()
}

func TestLSP_Implementation_Integration(t *testing.T) {
	skipIfNoGopls(t)
	workspace, goFile := lspIntegrationTestDir(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Implementation query on Greeter struct
	locs, err := Implementation(ctx, workspace, goFile, Position{Line: 6, Character: 6})
	if err != nil {
		t.Fatalf("Implementation failed: %v", err)
	}
	t.Logf("Implementation: %d results", len(locs))

	globalSessions.mu.Lock()
	for k, s := range globalSessions.sessions {
		if s.workspace == workspace {
			s.close()
			delete(globalSessions.sessions, k)
		}
	}
	globalSessions.mu.Unlock()
}

func TestLSP_Diagnostics_Integration(t *testing.T) {
	skipIfNoGopls(t)
	workspace, goFile := lspIntegrationTestDir(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	diags, err := Diagnostics(ctx, workspace, goFile)
	if err != nil {
		t.Fatalf("Diagnostics failed: %v", err)
	}
	t.Logf("Diagnostics: %d items", len(diags))

	globalSessions.mu.Lock()
	for k, s := range globalSessions.sessions {
		if s.workspace == workspace {
			s.close()
			delete(globalSessions.sessions, k)
		}
	}
	globalSessions.mu.Unlock()
}

func TestLSP_PrepareCallHierarchy_Integration(t *testing.T) {
	skipIfNoGopls(t)
	workspace, goFile := lspIntegrationTestDir(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Call hierarchy for "Greet" method on line 12
	items, err := PrepareCallHierarchy(ctx, workspace, goFile, Position{Line: 12, Character: 16})
	if err != nil {
		t.Fatalf("PrepareCallHierarchy failed: %v", err)
	}
	if len(items) == 0 {
		t.Error("expected at least one call hierarchy item")
	} else {
		t.Logf("CallHierarchy item: Name=%s, Path=%s", items[0].Name, items[0].Path)
	}

	globalSessions.mu.Lock()
	for k, s := range globalSessions.sessions {
		if s.workspace == workspace {
			s.close()
			delete(globalSessions.sessions, k)
		}
	}
	globalSessions.mu.Unlock()
}

func TestLSP_IncomingCalls_Integration(t *testing.T) {
	skipIfNoGopls(t)
	workspace, goFile := lspIntegrationTestDir(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// First prepare hierarchy for Greet method
	items, err := PrepareCallHierarchy(ctx, workspace, goFile, Position{Line: 12, Character: 16})
	if err != nil {
		t.Fatalf("PrepareCallHierarchy failed: %v", err)
	}
	if len(items) == 0 {
		t.Fatal("no call hierarchy items, cannot test IncomingCalls")
	}

	// Get incoming calls (callers of Greet)
	calls, err := IncomingCalls(ctx, workspace, items[0])
	if err != nil {
		t.Fatalf("IncomingCalls failed: %v", err)
	}
	t.Logf("IncomingCalls: %d results", len(calls))
	if len(calls) > 0 {
		t.Logf("  From: %s", calls[0].From.Name)
	}

	globalSessions.mu.Lock()
	for k, s := range globalSessions.sessions {
		if s.workspace == workspace {
			s.close()
			delete(globalSessions.sessions, k)
		}
	}
	globalSessions.mu.Unlock()
}

func TestLSP_OutgoingCalls_Integration(t *testing.T) {
	skipIfNoGopls(t)
	workspace, goFile := lspIntegrationTestDir(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Prepare hierarchy for SayHi function
	items, err := PrepareCallHierarchy(ctx, workspace, goFile, Position{Line: 18, Character: 16})
	if err != nil {
		t.Fatalf("PrepareCallHierarchy failed: %v", err)
	}
	if len(items) == 0 {
		t.Fatal("no call hierarchy items, cannot test OutgoingCalls")
	}

	// Get outgoing calls (callees of SayHi)
	calls, err := OutgoingCalls(ctx, workspace, items[0])
	if err != nil {
		t.Fatalf("OutgoingCalls failed: %v", err)
	}
	t.Logf("OutgoingCalls: %d results", len(calls))
	if len(calls) > 0 {
		t.Logf("  To: %s", calls[0].To.Name)
	}

	globalSessions.mu.Lock()
	for k, s := range globalSessions.sessions {
		if s.workspace == workspace {
			s.close()
			delete(globalSessions.sessions, k)
		}
	}
	globalSessions.mu.Unlock()
}

func TestLSP_WorkspaceSymbols_Integration(t *testing.T) {
	skipIfNoGopls(t)
	workspace, _ := lspIntegrationTestDir(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	symbols, err := WorkspaceSymbols(ctx, workspace, "Greet")
	if err != nil {
		t.Fatalf("WorkspaceSymbols failed: %v", err)
	}
	if len(symbols) == 0 {
		t.Error("expected at least one symbol matching 'Greet'")
	} else {
		t.Logf("Found %d symbols", len(symbols))
		for _, s := range symbols {
			t.Logf("  %s (kind=%d, path=%s)", s.Name, s.Kind, s.Path)
		}
	}

	globalSessions.mu.Lock()
	for k, s := range globalSessions.sessions {
		if s.workspace == workspace {
			s.close()
			delete(globalSessions.sessions, k)
		}
	}
	globalSessions.mu.Unlock()
}

func TestLSP_RenameEdits_Integration(t *testing.T) {
	skipIfNoGopls(t)
	workspace, goFile := lspIntegrationTestDir(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Rename "Greet" to "Hello"
	edits, err := RenameEdits(ctx, workspace, goFile, Position{Line: 12, Character: 16}, "Hello")
	if err != nil {
		t.Fatalf("RenameEdits failed: %v", err)
	}
	if len(edits) == 0 {
		t.Error("expected at least one file edit for rename")
	} else {
		t.Logf("Rename produces %d file edits", len(edits))
	}

	globalSessions.mu.Lock()
	for k, s := range globalSessions.sessions {
		if s.workspace == workspace {
			s.close()
			delete(globalSessions.sessions, k)
		}
	}
	globalSessions.mu.Unlock()
}

func TestLSP_CodeActions_Integration(t *testing.T) {
	skipIfNoGopls(t)
	workspace, goFile := lspIntegrationTestDir(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Request code actions on the import line (range within actual content)
	actions, err := CodeActions(ctx, workspace, goFile, Range{
		Start: Position{Line: 5, Character: 0},
		End:   Position{Line: 5, Character: 10},
	})
	if err != nil {
		t.Fatalf("CodeActions failed: %v", err)
	}
	t.Logf("CodeActions: %d results", len(actions))

	globalSessions.mu.Lock()
	for k, s := range globalSessions.sessions {
		if s.workspace == workspace {
			s.close()
			delete(globalSessions.sessions, k)
		}
	}
	globalSessions.mu.Unlock()
}
