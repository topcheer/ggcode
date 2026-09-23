package tool

// lsp tool coverage (sa-141): validation branches and capLSPOutput via
// stub exec functions. No language server is ever launched.

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/topcheer/ggcode/internal/lsp"
)

func writeStubGoFile(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "stub.go")
	if err := os.WriteFile(p, []byte("package stub\n\nfunc Stub() {}\n"), 0o644); err != nil {
		t.Fatalf("stub file: %v", err)
	}
	return p
}

func TestLSPPositiveAndErrorPathsSa141(t *testing.T) {
	stubPath := writeStubGoFile(t)
	ctx := context.Background()
	allowAll := AllowedPathChecker(func(string) bool { return true })
	denyAll := AllowedPathChecker(func(string) bool { return false })
	stubErr := errors.New("stub lsp failure")

	pathTool := &lspPathTool{name: "lsp_definition", description: "d", WorkingDir: filepath.Dir(stubPath), sandboxCheck: allowAll}

	// Invalid JSON.
	r, err := pathTool.Execute(ctx, json.RawMessage(`{`))
	if err != nil || !r.IsError || !strings.Contains(r.Content, "invalid input") {
		t.Fatalf("invalid JSON -> (%+v,%v)", r, err)
	}

	// Sandbox denial.
	denied := &lspPathTool{name: "lsp_definition", description: "d", WorkingDir: filepath.Dir(stubPath), sandboxCheck: denyAll}
	r, err = denied.Execute(ctx, json.RawMessage(`{"path":"`+stubPath+`"}`))
	if err != nil || !r.IsError || r.Content == "" {
		t.Fatalf("sandbox denial -> (%+v,%v)", r, err)
	}

	// Exec failure surfaces as error result.
	fail := &lspPathTool{name: "lsp_definition", description: "d", WorkingDir: filepath.Dir(stubPath), sandboxCheck: allowAll,
		exec: func(context.Context, string, string) (string, error) { return "", stubErr }}
	r, err = fail.Execute(ctx, json.RawMessage(`{"path":"`+stubPath+`"}`))
	if err != nil || !r.IsError || !strings.Contains(r.Content, "stub lsp failure") {
		t.Fatalf("exec failure -> (%+v,%v)", r, err)
	}

	// Success passes output through capLSPOutput.
	ok := &lspPathTool{name: "lsp_definition", description: "d", WorkingDir: filepath.Dir(stubPath), sandboxCheck: allowAll,
		exec: func(context.Context, string, string) (string, error) { return "definition here", nil }}
	r, err = ok.Execute(ctx, json.RawMessage(`{"path":"`+stubPath+`"}`))
	if err != nil || r.IsError || r.Content != "definition here" {
		t.Fatalf("success -> (%+v,%v)", r, err)
	}
}

func TestLSPPositionRangeCallHierarchySa141(t *testing.T) {
	stubPath := writeStubGoFile(t)
	ctx := context.Background()
	allowAll := AllowedPathChecker(func(string) bool { return true })

	pos := &lspPositionTool{name: "lsp_hover", description: "d", WorkingDir: filepath.Dir(stubPath), sandboxCheck: allowAll,
		exec: func(_ context.Context, _ string, _ string, _ lsp.Position) (string, error) {
			return "hover text", nil
		}}
	r, err := pos.Execute(ctx, json.RawMessage(`{"path":"`+stubPath+`","line":3,"character":7}`))
	if err != nil || r.IsError || r.Content != "hover text" {
		t.Fatalf("position success -> (%+v,%v)", r, err)
	}

	rng := &lspRangeTool{name: "lsp_code_actions", description: "d", WorkingDir: filepath.Dir(stubPath), sandboxCheck: allowAll,
		exec: func(_ context.Context, _ string, _ string, _ lsp.Range) (string, error) {
			return "actions", nil
		}}
	r, err = rng.Execute(ctx, json.RawMessage(`{"path":"`+stubPath+`","start_line":1,"end_line":2}`))
	if err != nil || r.IsError || r.Content != "actions" {
		t.Fatalf("range success -> (%+v,%v)", r, err)
	}

	call := &lspCallHierarchyTool{name: "lsp_incoming_calls", description: "d", WorkingDir: filepath.Dir(stubPath), sandboxCheck: allowAll,
		exec: func(context.Context, string, string) (string, error) { return "callers", nil }}
	r, err = call.Execute(ctx, json.RawMessage(`{"path":"`+stubPath+`","item":"{\"name\":\"Stub\"}"}`))
	if err != nil || r.IsError || r.Content != "callers" {
		t.Fatalf("call hierarchy success -> (%+v,%v)", r, err)
	}

	// Workspace query: passes path+query straight to exec (no empty-query
	// validation in the current implementation).
	ws := &lspWorkspaceQueryTool{name: "lsp_workspace_symbols", description: "d", WorkingDir: filepath.Dir(stubPath),
		exec: func(context.Context, string, string) (string, error) {
			return "symbols", nil
		}}
	r, err = ws.Execute(ctx, json.RawMessage(`{"path":"`+stubPath+`","query":"Stub"}`))
	if err != nil || r.IsError || r.Content != "symbols" {
		t.Fatalf("workspace query -> (%+v,%v)", r, err)
	}
}

func TestCapLSPOutputSa141(t *testing.T) {
	small := "short"
	if got := capLSPOutput(small); got != small {
		t.Fatalf("capLSPOutput(small) = %q", got)
	}
	// ASCII overflow: cut exactly at the boundary + suffix.
	big := strings.Repeat("a", maxLSPOutputBytes+100)
	got := capLSPOutput(big)
	if !strings.HasSuffix(got, "... [LSP output truncated]") {
		t.Fatalf("capLSPOutput(big) suffix missing")
	}
	// Multibyte content: cut lands on a rune boundary (valid UTF-8 prefix).
	cjk := strings.Repeat("中吇", maxLSPOutputBytes) // 6 bytes per rune
	got = capLSPOutput(cjk)
	prefix := strings.TrimSuffix(got, "\n... [LSP output truncated]")
	if !utf8ValidString(prefix) {
		t.Fatal("capLSPOutput produced invalid UTF-8 prefix (mojibake regression)")
	}
}

func utf8ValidString(s string) bool {
	return utf8.ValidString(s)
}

func TestLSPOutputSizeMatchesCapSa141(t *testing.T) {
	big := strings.Repeat("x", maxLSPOutputBytes*2)
	got := capLSPOutput(big)
	if len(got) > maxLSPOutputBytes+100 {
		t.Fatalf("capped output too large: %d bytes", len(got))
	}
}
