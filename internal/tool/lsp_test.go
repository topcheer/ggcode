package tool

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/lsp"
)

func TestNearbyIdentifierPositionsUsesClosestSymbolCandidates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sample.cs")
	source := "var message = new Message(author, body);\n"
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatalf("WriteFile(sample.cs) error = %v", err)
	}
	positions := nearbyIdentifierPositions(path, lsp.Position{Line: 1, Character: 18})
	if len(positions) < 3 {
		t.Fatalf("expected multiple fallback positions, got %#v", positions)
	}
	if positions[0] != (lsp.Position{Line: 1, Character: 19}) {
		t.Fatalf("expected right-side Message candidate first, got %#v", positions)
	}
	if positions[1] != (lsp.Position{Line: 1, Character: 15}) {
		t.Fatalf("expected second candidate to be new, got %#v", positions)
	}
}

func TestLSPSymbolsToolWithInstalledGopls(t *testing.T) {
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls not installed")
	}
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "go.mod"), []byte("module example.com/test\n\ngo 1.26.1\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(go.mod) error = %v", err)
	}
	path := filepath.Join(workspace, "sample.go")
	if err := os.WriteFile(path, []byte("package sample\n\nfunc Add(a int, b int) int { return a + b }\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(sample.go) error = %v", err)
	}
	tool := NewLSPTools(workspace, nil, nil)[3]
	input, err := json.Marshal(map[string]string{"path": path})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %+v", result)
	}
	if !strings.Contains(result.Content, "Add") {
		t.Fatalf("expected Add symbol in tool output, got %q", result.Content)
	}
}

func TestLSPWorkspaceSymbolsToolWithInstalledGopls(t *testing.T) {
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls not installed")
	}
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "go.mod"), []byte("module example.com/test\n\ngo 1.26.1\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(go.mod) error = %v", err)
	}
	path := filepath.Join(workspace, "sample.go")
	if err := os.WriteFile(path, []byte("package sample\n\nfunc Add(a int, b int) int { return a + b }\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(sample.go) error = %v", err)
	}
	tool := NewLSPTools(workspace, nil, nil)[4]
	input, err := json.Marshal(map[string]string{"query": "Add"})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %+v", result)
	}
	if !strings.Contains(result.Content, "Add") {
		t.Fatalf("expected Add symbol in workspace symbol output, got %q", result.Content)
	}
}

func TestLSPRenameToolWithInstalledGopls(t *testing.T) {
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls not installed")
	}
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "go.mod"), []byte("module example.com/test\n\ngo 1.26.1\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(go.mod) error = %v", err)
	}
	path := filepath.Join(workspace, "sample.go")
	source := "package sample\n\nfunc Add(a int, b int) int { return a + b }\n\nfunc Use() int { return Add(1, 2) }\n"
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatalf("WriteFile(sample.go) error = %v", err)
	}
	allow := func(candidate string) bool { return strings.HasPrefix(candidate, workspace) }
	tool := NewLSPTools(workspace, allow, allow)[7]
	input, err := json.Marshal(map[string]any{"path": path, "line": 3, "character": 6, "new_name": "Sum"})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %+v", result)
	}
	updated, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(sample.go) error = %v", err)
	}
	if !strings.Contains(string(updated), "func Sum") || !strings.Contains(string(updated), "return Sum(1, 2)") {
		t.Fatalf("expected rename to update file, got %q", string(updated))
	}
}

func TestLSPPositionToolsFallbackOnExternalCSharpFixture(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("home directory unavailable")
	}
	workspace := filepath.Join(home, "ggai", "csharp-message-board")
	messagePath := filepath.Join(workspace, "Message.cs")
	messageBoardPath := filepath.Join(workspace, "MessageBoard.cs")
	if _, err := os.Stat(messagePath); err != nil {
		t.Skip("external csharp fixture not present")
	}
	tools := NewLSPTools(workspace, nil, nil)

	hoverInput, err := json.Marshal(map[string]any{"path": messagePath, "line": 3, "character": 21})
	if err != nil {
		t.Fatalf("json.Marshal(hoverInput) error = %v", err)
	}
	hoverResult, err := tools[0].Execute(context.Background(), hoverInput)
	if err != nil {
		t.Fatalf("hover Execute() error = %v", err)
	}
	if hoverResult.IsError || !strings.Contains(hoverResult.Content, "Message") {
		t.Fatalf("expected hover fallback to resolve Message symbol, got %+v", hoverResult)
	}

	definitionInput, err := json.Marshal(map[string]any{"path": messageBoardPath, "line": 9, "character": 21})
	if err != nil {
		t.Fatalf("json.Marshal(definitionInput) error = %v", err)
	}
	definitionResult, err := tools[1].Execute(context.Background(), definitionInput)
	if err != nil {
		t.Fatalf("definition Execute() error = %v", err)
	}
	if definitionResult.IsError || !strings.Contains(definitionResult.Content, "Message.cs:3:22") {
		t.Fatalf("expected definition fallback to resolve Message.cs, got %+v", definitionResult)
	}

	referencesInput, err := json.Marshal(map[string]any{"path": messagePath, "line": 3, "character": 21})
	if err != nil {
		t.Fatalf("json.Marshal(referencesInput) error = %v", err)
	}
	referencesResult, err := tools[2].Execute(context.Background(), referencesInput)
	if err != nil {
		t.Fatalf("references Execute() error = %v", err)
	}
	if referencesResult.IsError || !strings.Contains(referencesResult.Content, "MessageBoard.cs:9:27") {
		t.Fatalf("expected references fallback to include MessageBoard.cs usage, got %+v", referencesResult)
	}
}

func TestLSPToolsWithExternalPythonFixture(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("home directory unavailable")
	}
	workspace := filepath.Join(home, "ggai", "python-message-board")
	appPath := filepath.Join(workspace, "app.py")
	if _, err := os.Stat(appPath); err != nil {
		t.Skip("external python fixture not present")
	}
	tools := NewLSPTools(workspace, nil, nil)

	symbolsInput, err := json.Marshal(map[string]any{"path": appPath})
	if err != nil {
		t.Fatalf("json.Marshal(symbolsInput) error = %v", err)
	}
	symbolsResult, err := tools[3].Execute(context.Background(), symbolsInput)
	if err != nil {
		t.Fatalf("symbols Execute() error = %v", err)
	}
	if symbolsResult.IsError || strings.Contains(symbolsResult.Content, "deadline exceeded") || strings.Contains(symbolsResult.Content, "No symbols returned.") {
		t.Fatalf("expected python symbols to complete with semantic output, got %+v", symbolsResult)
	}

	hoverInput, err := json.Marshal(map[string]any{"path": appPath, "line": 9, "character": 27})
	if err != nil {
		t.Fatalf("json.Marshal(hoverInput) error = %v", err)
	}
	hoverResult, err := tools[0].Execute(context.Background(), hoverInput)
	if err != nil {
		t.Fatalf("hover Execute() error = %v", err)
	}
	if hoverResult.IsError || strings.Contains(hoverResult.Content, "deadline exceeded") || strings.Contains(hoverResult.Content, "No hover information returned.") {
		t.Fatalf("expected python hover to complete with semantic output, got %+v", hoverResult)
	}
}
