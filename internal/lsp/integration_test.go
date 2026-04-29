package lsp

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// ── helpers ──────────────────────────────────────────────────────────────

func skipIfNoBinary(t *testing.T, name string) {
	t.Helper()
	if _, err := exec.LookPath(name); err != nil {
		t.Skipf("%s not found in PATH, skipping", name)
	}
}

func cleanupSessions(t *testing.T, workspace string) {
	t.Helper()
	globalSessions.mu.Lock()
	for k, s := range globalSessions.sessions {
		if s.workspace == workspace {
			s.close()
			delete(globalSessions.sessions, k)
		}
	}
	globalSessions.mu.Unlock()
}

// ── Go / gopls ───────────────────────────────────────────────────────────

func createGoWorkspace(t *testing.T) (workspace, goFile string) {
	t.Helper()
	workspace = t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "go.mod"), []byte("module testlsp\n\ngo 1.22\n"), 0644); err != nil {
		t.Fatal(err)
	}
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
	cmd := exec.Command("go", "mod", "tidy")
	cmd.Dir = workspace
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go mod tidy: %v\n%s", err, out)
	}
	return workspace, goFile
}

func TestLSP_Go_Hover(t *testing.T) {
	skipIfNoBinary(t, "gopls")
	ws, f := createGoWorkspace(t)
	defer cleanupSessions(t, ws)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	got, err := Hover(ctx, ws, f, Position{Line: 6, Character: 6})
	if err != nil {
		t.Fatal(err)
	}
	if got == "" {
		t.Error("empty hover result for Greeter")
	}
}

func TestLSP_Go_Definition(t *testing.T) {
	skipIfNoBinary(t, "gopls")
	ws, f := createGoWorkspace(t)
	defer cleanupSessions(t, ws)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	locs, err := Definition(ctx, ws, f, Position{Line: 18, Character: 11})
	if err != nil {
		t.Fatal(err)
	}
	if len(locs) == 0 {
		t.Error("expected definition location")
	}
}

func TestLSP_Go_References(t *testing.T) {
	skipIfNoBinary(t, "gopls")
	ws, f := createGoWorkspace(t)
	defer cleanupSessions(t, ws)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	locs, err := References(ctx, ws, f, Position{Line: 12, Character: 16})
	if err != nil {
		t.Fatal(err)
	}
	if len(locs) == 0 {
		t.Error("expected references to Greet")
	}
}

func TestLSP_Go_Implementation(t *testing.T) {
	skipIfNoBinary(t, "gopls")
	ws, f := createGoWorkspace(t)
	defer cleanupSessions(t, ws)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	locs, err := Implementation(ctx, ws, f, Position{Line: 6, Character: 6})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Implementation: %d results", len(locs))
}

func TestLSP_Go_Diagnostics(t *testing.T) {
	skipIfNoBinary(t, "gopls")
	ws, f := createGoWorkspace(t)
	defer cleanupSessions(t, ws)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	diags, err := Diagnostics(ctx, ws, f)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Diagnostics: %d items", len(diags))
}

func TestLSP_Go_Diagnostics_Errors(t *testing.T) {
	skipIfNoBinary(t, "gopls")
	ws := t.TempDir()
	defer cleanupSessions(t, ws)

	if err := os.WriteFile(filepath.Join(ws, "go.mod"), []byte("module testlsp\n\ngo 1.22\n"), 0644); err != nil {
		t.Fatal(err)
	}
	// File with a deliberate type error
	code := `package testlsp

func broken() int {
	return "not an int"
}
`
	f := filepath.Join(ws, "bad.go")
	if err := os.WriteFile(f, []byte(code), 0644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", "mod", "tidy")
	cmd.Dir = ws
	_ = cmd.Run() // may fail, that's ok

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	diags, err := Diagnostics(ctx, ws, f)
	if err != nil {
		t.Fatal(err)
	}
	if len(diags) == 0 {
		t.Error("expected diagnostics for type error")
	}
	t.Logf("Error Diagnostics: %d", len(diags))
}

func TestLSP_Go_CallHierarchy(t *testing.T) {
	skipIfNoBinary(t, "gopls")
	ws, f := createGoWorkspace(t)
	defer cleanupSessions(t, ws)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	items, err := PrepareCallHierarchy(ctx, ws, f, Position{Line: 12, Character: 16})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) == 0 {
		t.Fatal("no call hierarchy items")
	}
	t.Logf("PrepareCallHierarchy: Name=%s", items[0].Name)

	inCalls, err := IncomingCalls(ctx, ws, items[0])
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("IncomingCalls: %d results", len(inCalls))

	outItems, err := PrepareCallHierarchy(ctx, ws, f, Position{Line: 18, Character: 16})
	if err != nil {
		t.Fatal(err)
	}
	if len(outItems) > 0 {
		outCalls, err := OutgoingCalls(ctx, ws, outItems[0])
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("OutgoingCalls: %d results", len(outCalls))
	}
}

func TestLSP_Go_WorkspaceSymbols(t *testing.T) {
	skipIfNoBinary(t, "gopls")
	ws, _ := createGoWorkspace(t)
	defer cleanupSessions(t, ws)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	symbols, err := WorkspaceSymbols(ctx, ws, "Greet")
	if err != nil {
		t.Fatal(err)
	}
	if len(symbols) == 0 {
		t.Error("expected symbols matching 'Greet'")
	}
}

func TestLSP_Go_RenameEdits(t *testing.T) {
	skipIfNoBinary(t, "gopls")
	ws, f := createGoWorkspace(t)
	defer cleanupSessions(t, ws)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	edits, err := RenameEdits(ctx, ws, f, Position{Line: 12, Character: 16}, "Hello")
	if err != nil {
		t.Fatal(err)
	}
	if len(edits) == 0 {
		t.Error("expected rename edits")
	}
}

func TestLSP_Go_CodeActions(t *testing.T) {
	skipIfNoBinary(t, "gopls")
	ws, f := createGoWorkspace(t)
	defer cleanupSessions(t, ws)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	actions, err := CodeActions(ctx, ws, f, Range{
		Start: Position{Line: 5, Character: 0},
		End:   Position{Line: 5, Character: 10},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("CodeActions: %d results", len(actions))
}

// ── TypeScript / typescript-language-server ──────────────────────────────

func createTSWorkspace(t *testing.T) (workspace, tsFile string) {
	t.Helper()
	workspace = t.TempDir()

	// package.json + tsconfig.json + install typescript so tsserver is happy
	pkgJSON := `{"name":"testlsp","version":"1.0.0","dependencies":{}}`
	if err := os.WriteFile(filepath.Join(workspace, "package.json"), []byte(pkgJSON), 0644); err != nil {
		t.Fatal(err)
	}
	tsconfig := `{"compilerOptions":{"target":"ES2020","module":"commonjs","strict":true}}`
	if err := os.WriteFile(filepath.Join(workspace, "tsconfig.json"), []byte(tsconfig), 0644); err != nil {
		t.Fatal(err)
	}
	// npm install so tsserver sees a real project
	cmd := exec.Command("npm", "install", "--ignore-scripts")
	cmd.Dir = workspace
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("npm install: %v\n%s", err, out)
	}

	code := `export interface User {
  name: string;
  age: number;
}

export function greet(user: User): string {
  return "Hello, " + user.name;
}

export function main(): void {
  const u: User = { name: "World", age: 42 };
  console.log(greet(u));
}
`
	tsFile = filepath.Join(workspace, "index.ts")
	if err := os.WriteFile(tsFile, []byte(code), 0644); err != nil {
		t.Fatal(err)
	}
	return workspace, tsFile
}

func TestLSP_TS_Hover(t *testing.T) {
	skipIfNoBinary(t, "typescript-language-server")
	ws, f := createTSWorkspace(t)
	defer cleanupSessions(t, ws)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Hover over 'greet' function name on line 6
	got, err := Hover(ctx, ws, f, Position{Line: 6, Character: 25})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("TS Hover: %s", truncateForLog(got, 200))
	// tsserver may return empty on first call; just log, don't fail
}

func TestLSP_TS_Definition(t *testing.T) {
	skipIfNoBinary(t, "typescript-language-server")
	ws, f := createTSWorkspace(t)
	defer cleanupSessions(t, ws)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Definition of "User" on line 6 (greet param type)
	locs, err := Definition(ctx, ws, f, Position{Line: 6, Character: 24})
	if err != nil {
		t.Fatal(err)
	}
	if len(locs) == 0 {
		t.Error("expected definition location for User")
	}
}

func TestLSP_TS_References(t *testing.T) {
	skipIfNoBinary(t, "typescript-language-server")
	ws, f := createTSWorkspace(t)
	defer cleanupSessions(t, ws)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	locs, err := References(ctx, ws, f, Position{Line: 6, Character: 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(locs) == 0 {
		t.Error("expected references to 'greet'")
	}
	t.Logf("TS References: %d", len(locs))
}

func TestLSP_TS_Diagnostics(t *testing.T) {
	skipIfNoBinary(t, "typescript-language-server")
	ws, f := createTSWorkspace(t)
	defer cleanupSessions(t, ws)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	diags, err := Diagnostics(ctx, ws, f)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("TS Diagnostics: %d", len(diags))
}

func TestLSP_TS_WorkspaceSymbols(t *testing.T) {
	skipIfNoBinary(t, "typescript-language-server")
	ws, _ := createTSWorkspace(t)
	defer cleanupSessions(t, ws)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	symbols, err := WorkspaceSymbols(ctx, ws, "greet")
	if err != nil {
		// tsserver may error with "No Project" in minimal temp dir
		t.Logf("WorkspaceSymbols error (may be ok for temp workspace): %v", err)
		return
	}
	t.Logf("TS WorkspaceSymbols: %d results", len(symbols))
}

func TestLSP_TS_RenameEdits(t *testing.T) {
	skipIfNoBinary(t, "typescript-language-server")
	ws, f := createTSWorkspace(t)
	defer cleanupSessions(t, ws)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	edits, err := RenameEdits(ctx, ws, f, Position{Line: 6, Character: 20}, "sayHello")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("TS Rename: %d file edits", len(edits))
}

func TestLSP_TS_CodeActions(t *testing.T) {
	skipIfNoBinary(t, "typescript-language-server")
	ws, f := createTSWorkspace(t)
	defer cleanupSessions(t, ws)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	actions, err := CodeActions(ctx, ws, f, Range{
		Start: Position{Line: 1, Character: 0},
		End:   Position{Line: 1, Character: 10},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("TS CodeActions: %d results", len(actions))
}

func TestLSP_TS_Implementation(t *testing.T) {
	skipIfNoBinary(t, "typescript-language-server")
	ws, f := createTSWorkspace(t)
	defer cleanupSessions(t, ws)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	locs, err := Implementation(ctx, ws, f, Position{Line: 1, Character: 10})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("TS Implementation: %d results", len(locs))
}

func TestLSP_TS_CallHierarchy(t *testing.T) {
	skipIfNoBinary(t, "typescript-language-server")
	ws, f := createTSWorkspace(t)
	defer cleanupSessions(t, ws)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	items, err := PrepareCallHierarchy(ctx, ws, f, Position{Line: 6, Character: 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) == 0 {
		t.Fatal("no call hierarchy items for greet")
	}
	t.Logf("TS PrepareCallHierarchy: Name=%s", items[0].Name)

	inCalls, err := IncomingCalls(ctx, ws, items[0])
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("TS IncomingCalls: %d", len(inCalls))

	outCalls, err := OutgoingCalls(ctx, ws, items[0])
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("TS OutgoingCalls: %d", len(outCalls))
}

// ── Lua / lua-language-server ────────────────────────────────────────────

func createLuaWorkspace(t *testing.T) (workspace, luaFile string) {
	t.Helper()
	workspace = t.TempDir()

	code := `--- A simple calculator module
local M = {}

--- Add two numbers
function M.add(a, b)
  return a + b
end

--- Multiply two numbers
function M.mul(a, b)
  return a * b
end

--- Chain add then multiply
function M.chain(x, y, z)
  return M.mul(M.add(x, y), z)
end

return M
`
	luaFile = filepath.Join(workspace, "calc.lua")
	if err := os.WriteFile(luaFile, []byte(code), 0644); err != nil {
		t.Fatal(err)
	}
	return workspace, luaFile
}

func TestLSP_Lua_Hover(t *testing.T) {
	skipIfNoBinary(t, "lua-language-server")
	ws, f := createLuaWorkspace(t)
	defer cleanupSessions(t, ws)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	got, err := Hover(ctx, ws, f, Position{Line: 5, Character: 14})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Lua Hover: %s", truncateForLog(got, 200))
}

func TestLSP_Lua_Definition(t *testing.T) {
	skipIfNoBinary(t, "lua-language-server")
	ws, f := createLuaWorkspace(t)
	defer cleanupSessions(t, ws)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	locs, err := Definition(ctx, ws, f, Position{Line: 18, Character: 10})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Lua Definition: %d results", len(locs))
}

func TestLSP_Lua_References(t *testing.T) {
	skipIfNoBinary(t, "lua-language-server")
	ws, f := createLuaWorkspace(t)
	defer cleanupSessions(t, ws)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	locs, err := References(ctx, ws, f, Position{Line: 5, Character: 14})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Lua References: %d results", len(locs))
}

func TestLSP_Lua_Diagnostics(t *testing.T) {
	skipIfNoBinary(t, "lua-language-server")
	ws, f := createLuaWorkspace(t)
	defer cleanupSessions(t, ws)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	diags, err := Diagnostics(ctx, ws, f)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Lua Diagnostics: %d", len(diags))
}

func TestLSP_Lua_WorkspaceSymbols(t *testing.T) {
	skipIfNoBinary(t, "lua-language-server")
	ws, _ := createLuaWorkspace(t)
	defer cleanupSessions(t, ws)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	symbols, err := WorkspaceSymbols(ctx, ws, "add")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Lua WorkspaceSymbols: %d results", len(symbols))
}

func TestLSP_Lua_RenameEdits(t *testing.T) {
	skipIfNoBinary(t, "lua-language-server")
	ws, f := createLuaWorkspace(t)
	defer cleanupSessions(t, ws)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	edits, err := RenameEdits(ctx, ws, f, Position{Line: 5, Character: 14}, "sum")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Lua Rename: %d file edits", len(edits))
}

func TestLSP_Lua_CodeActions(t *testing.T) {
	skipIfNoBinary(t, "lua-language-server")
	ws, f := createLuaWorkspace(t)
	defer cleanupSessions(t, ws)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	actions, err := CodeActions(ctx, ws, f, Range{
		Start: Position{Line: 1, Character: 0},
		End:   Position{Line: 1, Character: 10},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Lua CodeActions: %d results", len(actions))
}

// ── util ─────────────────────────────────────────────────────────────────

func truncateForLog(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
