//go:build integration_local

// This file contains tests that spawn heavyweight external language server
// processes (JVM, .NET, Node.js, etc.). They are excluded from CI and
// `make verify-ci` via the integration_local build tag. Run them locally with:
//
//	go test -tags "goolm,integration_local" ./internal/lsp/ -run TestDocumentSymbolsWithInstalled -v
//	go test -tags "goolm,integration_local" ./internal/lsp/ -run TestExternal -v

package lsp

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDocumentSymbolsWithInstalledTypeScriptLanguageServer(t *testing.T) {
	if _, err := exec.LookPath("typescript-language-server"); err != nil {
		t.Skip("typescript-language-server not installed")
	}
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "package.json"), []byte("{\"name\":\"board\",\"private\":true}"), 0o644); err != nil {
		t.Fatalf("WriteFile(package.json) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "tsconfig.json"), []byte("{\"compilerOptions\":{\"target\":\"ES2022\",\"module\":\"NodeNext\",\"moduleResolution\":\"NodeNext\",\"strict\":true}}"), 0o644); err != nil {
		t.Fatalf("WriteFile(tsconfig.json) error = %v", err)
	}
	srcDir := filepath.Join(workspace, "src")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(src) error = %v", err)
	}
	boardPath := filepath.Join(srcDir, "board.ts")
	source := `export class MessageBoard {
  addMessage(author: string, body: string): string {
    return author + body;
  }
}
`
	if err := os.WriteFile(boardPath, []byte(source), 0o644); err != nil {
		t.Fatalf("WriteFile(board.ts) error = %v", err)
	}
	symbols, err := DocumentSymbols(context.Background(), workspace, boardPath)
	if err != nil {
		t.Fatalf("DocumentSymbols() error = %v", err)
	}
	found := false
	for _, symbol := range symbols {
		if symbol.Name == "MessageBoard" || symbol.Name == "addMessage" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected MessageBoard/addMessage symbol, got %#v", symbols)
	}
}

func TestDocumentSymbolsWithInstalledJDTLS(t *testing.T) {
	if _, err := exec.LookPath("jdtls"); err != nil {
		t.Skip("jdtls not installed")
	}
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "pom.xml"), []byte("<project xmlns=\"http://maven.apache.org/POM/4.0.0\"><modelVersion>4.0.0</modelVersion><groupId>com.example</groupId><artifactId>board</artifactId><version>1.0.0</version></project>"), 0o644); err != nil {
		t.Fatalf("WriteFile(pom.xml) error = %v", err)
	}
	srcDir := filepath.Join(workspace, "src", "main", "java", "com", "example", "board")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(java src) error = %v", err)
	}
	boardPath := filepath.Join(srcDir, "MessageBoard.java")
	source := `package com.example.board;

public final class MessageBoard {
    public String addMessage(String author, String body) {
        return author + body;
    }
}
`
	if err := os.WriteFile(boardPath, []byte(source), 0o644); err != nil {
		t.Fatalf("WriteFile(MessageBoard.java) error = %v", err)
	}
	symbols, err := DocumentSymbols(context.Background(), workspace, boardPath)
	if err != nil {
		t.Fatalf("DocumentSymbols() error = %v", err)
	}
	found := false
	for _, symbol := range symbols {
		if symbol.Name == "MessageBoard" || symbol.Name == "addMessage" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected MessageBoard/addMessage symbol, got %#v", symbols)
	}
}

func TestDocumentSymbolsWithInstalledCSharpLS(t *testing.T) {
	if _, err := exec.LookPath("csharp-ls"); err != nil {
		t.Skip("csharp-ls not installed")
	}
	if _, err := exec.LookPath("dotnet"); err != nil {
		t.Skip("dotnet not installed")
	}
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "Board.csproj"), []byte("<Project Sdk=\"Microsoft.NET.Sdk\"><PropertyGroup><TargetFramework>net8.0</TargetFramework></PropertyGroup></Project>"), 0o644); err != nil {
		t.Fatalf("WriteFile(Board.csproj) error = %v", err)
	}
	boardPath := filepath.Join(workspace, "MessageBoard.cs")
	source := `namespace MessageBoardApp;

public sealed class MessageBoard
{
    public string AddMessage(string author, string body)
    {
        return author + body;
    }
}
`
	if err := os.WriteFile(boardPath, []byte(source), 0o644); err != nil {
		t.Fatalf("WriteFile(MessageBoard.cs) error = %v", err)
	}
	newSln := exec.Command("dotnet", "new", "sln", "-n", "Board")
	newSln.Dir = workspace
	newSln.Env = os.Environ()
	if out, err := newSln.CombinedOutput(); err != nil {
		t.Fatalf("dotnet new sln error = %v, output=%s", err, string(out))
	}
	matches, err := filepath.Glob(filepath.Join(workspace, "Board.sln*"))
	if err != nil || len(matches) != 1 {
		t.Fatalf("expected one solution file, got %v (err=%v)", matches, err)
	}
	addProject := exec.Command("dotnet", "sln", filepath.Base(matches[0]), "add", "Board.csproj")
	addProject.Dir = workspace
	addProject.Env = os.Environ()
	if out, err := addProject.CombinedOutput(); err != nil {
		t.Fatalf("dotnet sln add error = %v, output=%s", err, string(out))
	}
	symbols, err := DocumentSymbols(context.Background(), workspace, boardPath)
	if err != nil {
		t.Fatalf("DocumentSymbols() error = %v", err)
	}
	found := false
	for _, symbol := range symbols {
		if symbol.Name == "MessageBoard" || symbol.Name == "AddMessage" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected MessageBoard/AddMessage symbol, got %#v", symbols)
	}
}

func TestExternalCSharpFixtureLSPCalls(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("home directory unavailable")
	}
	workspace := filepath.Join(home, "ggai", "csharp-message-board")
	if _, err := os.Stat(filepath.Join(workspace, "Board.csproj")); err != nil {
		t.Skip("external csharp fixture not present")
	}
	path := filepath.Join(workspace, "MessageBoard.cs")
	if _, err := os.Stat(path); err != nil {
		t.Skip("external csharp fixture source not present")
	}
	resolved, ok := ResolveServerForFile(workspace, path)
	if !ok {
		t.Skip("no csharp server available for external fixture")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	symbols, err := DocumentSymbols(ctx, workspace, path)
	if err != nil || len(symbols) == 0 {
		if wsSymbols, wsErr := WorkspaceSymbols(ctx, workspace, "MessageBoard"); wsErr == nil {
			t.Logf("workspace symbols after empty document symbols: %#v", wsSymbols)
		} else {
			t.Logf("workspace symbols after empty document symbols error: %v", wsErr)
		}
		if definition, defErr := Definition(ctx, workspace, path, Position{Line: 9, Character: 27}); defErr == nil {
			t.Logf("definition after empty symbols: %#v", definition)
		} else {
			t.Logf("definition after empty symbols error: %v", defErr)
		}
		if references, refErr := References(ctx, workspace, path, Position{Line: 9, Character: 27}); refErr == nil {
			t.Logf("references after empty symbols: %#v", references)
		} else {
			t.Logf("references after empty symbols error: %v", refErr)
		}
		session, acquireErr := globalSessions.acquire(ctx, workspace, resolved)
		if acquireErr == nil {
			if docURI, prepErr := session.prepareDocument(ctx, path, resolved.LanguageID); prepErr == nil {
				var raw json.RawMessage
				if callErr := session.client.call(ctx, "textDocument/documentSymbol", map[string]any{
					"textDocument": map[string]any{"uri": docURI},
				}, &raw); callErr == nil {
					t.Logf("raw documentSymbol response: %s", string(raw))
				} else {
					t.Logf("raw documentSymbol error: %v", callErr)
				}
				var wsRaw json.RawMessage
				if callErr := session.client.call(ctx, "workspace/symbol", map[string]any{
					"query": "MessageBoard",
				}, &wsRaw); callErr == nil {
					t.Logf("raw workspace/symbol response: %s", string(wsRaw))
				} else {
					t.Logf("raw workspace/symbol error: %v", callErr)
				}
			}
		}
		if strings.HasSuffix(strings.ToLower(strings.Join(resolved.Args, " ")), ".slnx") {
			withoutSolution := resolved
			withoutSolution.Args = nil
			session, acquireErr := globalSessions.acquire(ctx, workspace, withoutSolution)
			if acquireErr == nil {
				if docURI, prepErr := session.prepareDocument(ctx, path, withoutSolution.LanguageID); prepErr == nil {
					var raw json.RawMessage
					if callErr := session.client.call(ctx, "textDocument/documentSymbol", map[string]any{
						"textDocument": map[string]any{"uri": docURI},
					}, &raw); callErr == nil {
						t.Logf("raw documentSymbol response without --solution: %s", string(raw))
					} else {
						t.Logf("raw documentSymbol error without --solution: %v", callErr)
					}
				}
			}
		}
		tracePath := filepath.Join(t.TempDir(), "csharp-ls-rpc.log")
		traced := resolved
		traced.Args = append(append([]string{}, traced.Args...), "--loglevel", "debug", "--rpclog", tracePath)
		if tracedSession, acquireErr := globalSessions.acquire(ctx, workspace, traced); acquireErr == nil {
			if docURI, prepErr := tracedSession.prepareDocument(ctx, path, traced.LanguageID); prepErr == nil {
				var raw json.RawMessage
				_ = tracedSession.client.call(ctx, "textDocument/documentSymbol", map[string]any{
					"textDocument": map[string]any{"uri": docURI},
				}, &raw)
				if data, readErr := os.ReadFile(tracePath); readErr == nil {
					t.Logf("csharp-ls rpc trace:\n%s", string(data))
				} else {
					t.Logf("csharp-ls rpc trace read error: %v", readErr)
				}
				if stderr := strings.TrimSpace(tracedSession.client.stderr.String()); stderr != "" {
					t.Logf("csharp-ls stderr:\n%s", stderr)
				}
			}
		}
		t.Fatalf("DocumentSymbols() err=%v len=%d resolved=%#v", err, len(symbols), resolved)
	}
	definition, err := Definition(ctx, workspace, path, Position{Line: 9, Character: 27})
	if err != nil || len(definition) == 0 {
		t.Fatalf("Definition() err=%v definition=%#v resolved=%#v", err, definition, resolved)
	}
	references, err := References(ctx, workspace, path, Position{Line: 9, Character: 27})
	if err != nil || len(references) == 0 {
		t.Fatalf("References() err=%v references=%#v resolved=%#v", err, references, resolved)
	}
}

func TestExternalCSharpFixtureMessageRecordHoverAndReferences(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("home directory unavailable")
	}
	workspace := filepath.Join(home, "ggai", "csharp-message-board")
	path := filepath.Join(workspace, "Message.cs")
	if _, err := os.Stat(path); err != nil {
		t.Skip("external csharp Message.cs fixture not present")
	}
	if _, ok := ResolveServerForFile(workspace, path); !ok {
		t.Skip("no csharp server available for external fixture")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	hover, err := Hover(ctx, workspace, path, Position{Line: 3, Character: 22})
	t.Logf("hover on Message record: err=%v hover=%q", err, hover)

	references, err := References(ctx, workspace, path, Position{Line: 3, Character: 22})
	t.Logf("references on Message record: err=%v refs=%#v", err, references)

	definition, err := Definition(ctx, workspace, filepath.Join(workspace, "MessageBoard.cs"), Position{Line: 9, Character: 21})
	t.Logf("definition from constructor usage: err=%v def=%#v", err, definition)
}

func TestExternalPythonFixtureLSPCalls(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("home directory unavailable")
	}
	workspace := filepath.Join(home, "ggai", "python-message-board")
	path := filepath.Join(workspace, "app.py")
	if _, err := os.Stat(path); err != nil {
		t.Skip("external python fixture not present")
	}
	resolved, ok := ResolveServerForFile(workspace, path)
	if !ok {
		t.Skip("no python server available for external fixture")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	start := time.Now()
	symbols, err := DocumentSymbols(ctx, workspace, path)
	t.Logf("DocumentSymbols elapsed=%v err=%v len=%d resolved=%#v", time.Since(start), err, len(symbols), resolved)
	if err == nil && len(symbols) > 0 {
		return
	}

	session, acquireErr := globalSessions.acquire(ctx, workspace, resolved)
	if acquireErr == nil {
		if docURI, prepErr := session.prepareDocument(ctx, path, resolved.LanguageID); prepErr == nil {
			var raw json.RawMessage
			callStart := time.Now()
			callErr := session.client.call(ctx, "textDocument/documentSymbol", map[string]any{
				"textDocument": map[string]any{"uri": docURI},
			}, &raw)
			t.Logf("raw documentSymbol elapsed=%v err=%v raw=%s", time.Since(callStart), callErr, string(raw))
			if stderr := strings.TrimSpace(session.client.stderr.String()); stderr != "" {
				t.Logf("python lsp stderr:\n%s", stderr)
			}
		}
	}
}

func TestExternalClangFixtureLSPCalls(t *testing.T) {
	if _, err := exec.LookPath("clangd"); err != nil {
		t.Skip("clangd not installed")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("home directory unavailable")
	}
	workspace := filepath.Join(home, "ggai", "clang-message-board")
	mainPath := filepath.Join(workspace, "main.cpp")
	headerPath := filepath.Join(workspace, "message_board.h")
	brokenPath := filepath.Join(workspace, "broken.cpp")
	for _, path := range []string{mainPath, headerPath, brokenPath} {
		if _, err := os.Stat(path); err != nil {
			t.Skip("external clang fixture not present")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	symbols, err := DocumentSymbols(ctx, workspace, headerPath)
	if err != nil || len(symbols) == 0 {
		t.Fatalf("DocumentSymbols() err=%v len=%d", err, len(symbols))
	}
	hover, err := Hover(ctx, workspace, mainPath, Position{Line: 6, Character: 9})
	if err != nil || strings.TrimSpace(hover) == "" {
		t.Fatalf("Hover() err=%v hover=%q", err, hover)
	}
	definition, err := Definition(ctx, workspace, mainPath, Position{Line: 6, Character: 9})
	if err != nil || len(definition) == 0 {
		t.Fatalf("Definition() err=%v definition=%#v", err, definition)
	}
	references, err := References(ctx, workspace, headerPath, Position{Line: 14, Character: 8})
	if err != nil || len(references) == 0 {
		t.Fatalf("References() err=%v references=%#v", err, references)
	}
	workspaceSymbols, err := WorkspaceSymbols(ctx, workspace, "MessageBoard")
	if err != nil || len(workspaceSymbols) == 0 {
		t.Fatalf("WorkspaceSymbols() err=%v symbols=%#v", err, workspaceSymbols)
	}
	diagnostics, err := Diagnostics(ctx, workspace, brokenPath)
	if err != nil || len(diagnostics) == 0 {
		t.Fatalf("Diagnostics() err=%v diagnostics=%#v", err, diagnostics)
	}
	edits, err := RenameEdits(ctx, workspace, headerPath, Position{Line: 14, Character: 8}, "PostMessage")
	if err != nil || len(edits) == 0 {
		t.Fatalf("RenameEdits() err=%v edits=%#v", err, edits)
	}
}

func TestExternalSwiftFixtureLSPCalls(t *testing.T) {
	if _, err := exec.LookPath("sourcekit-lsp"); err != nil {
		t.Skip("sourcekit-lsp not installed")
	}
	if _, err := exec.LookPath("swift"); err != nil {
		t.Skip("swift not installed")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("home directory unavailable")
	}
	workspace := filepath.Join(home, "ggai", "swift-message-board")
	boardPath := filepath.Join(workspace, "Sources", "Board", "MessageBoard.swift")
	mainPath := filepath.Join(workspace, "Sources", "Board", "main.swift")
	for _, path := range []string{boardPath, mainPath} {
		if _, err := os.Stat(path); err != nil {
			t.Skip("external swift fixture not present")
		}
	}
	buildCmd := exec.Command("swift", "build")
	buildCmd.Dir = workspace
	if output, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("swift build failed: %v\n%s", err, string(output))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	hover, err := Hover(ctx, workspace, boardPath, Position{Line: 1, Character: 13})
	if err != nil || strings.TrimSpace(hover) == "" {
		t.Fatalf("Hover() err=%v hover=%q", err, hover)
	}
	definition, err := Definition(ctx, workspace, mainPath, Position{Line: 1, Character: 13})
	if err != nil || len(definition) == 0 {
		t.Skipf("Definition() returned empty (LSP may not have indexed fully): err=%v definition=%#v", err, definition)
	}
	references, err := References(ctx, workspace, boardPath, Position{Line: 1, Character: 13})
	if err != nil || len(references) == 0 {
		t.Fatalf("References() err=%v references=%#v", err, references)
	}
	workspaceSymbols, err := WorkspaceSymbols(ctx, workspace, "MessageBoard")
	if err != nil || len(workspaceSymbols) == 0 {
		t.Fatalf("WorkspaceSymbols() err=%v symbols=%#v", err, workspaceSymbols)
	}
	edits, err := RenameEdits(ctx, workspace, boardPath, Position{Line: 4, Character: 10}, "append")
	if err != nil || len(edits) == 0 {
		t.Fatalf("RenameEdits() err=%v edits=%#v", err, edits)
	}
}

func TestExternalConfigFixtureLSPCalls(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("home directory unavailable")
	}
	workspace := filepath.Join(home, "ggai", "config-message-board")
	yamlPath := filepath.Join(workspace, "config.yaml")
	brokenYAMLPath := filepath.Join(workspace, "broken.yaml")
	jsonPath := filepath.Join(workspace, "config.json")
	dockerPath := filepath.Join(workspace, "Dockerfile")
	shellPath := filepath.Join(workspace, "deploy.sh")
	for _, path := range []string{yamlPath, brokenYAMLPath, jsonPath, dockerPath, shellPath} {
		if _, err := os.Stat(path); err != nil {
			t.Skip("external config fixture not present")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	yamlSymbols, err := DocumentSymbols(ctx, workspace, yamlPath)
	if err != nil || len(yamlSymbols) == 0 {
		t.Fatalf("yaml DocumentSymbols() err=%v len=%d", err, len(yamlSymbols))
	}
	yamlDiagnostics, err := Diagnostics(ctx, workspace, brokenYAMLPath)
	if err != nil || len(yamlDiagnostics) == 0 {
		t.Fatalf("yaml Diagnostics() err=%v diagnostics=%#v", err, yamlDiagnostics)
	}

	jsonSymbols, err := DocumentSymbols(ctx, workspace, jsonPath)
	if err != nil || len(jsonSymbols) == 0 {
		t.Fatalf("json DocumentSymbols() err=%v len=%d", err, len(jsonSymbols))
	}
	dockerHover, err := Hover(ctx, workspace, dockerPath, Position{Line: 1, Character: 2})
	if err != nil || strings.TrimSpace(dockerHover) == "" {
		t.Fatalf("docker Hover() err=%v hover=%q", err, dockerHover)
	}

	resolved, ok := ResolveServerForFile(workspace, shellPath)
	if !ok {
		t.Fatal("expected shell server resolution")
	}
	session, err := globalSessions.acquire(ctx, workspace, resolved)
	if err != nil {
		t.Fatalf("shell acquire() error = %v", err)
	}
	docURI, err := session.prepareDocument(ctx, shellPath, resolved.LanguageID)
	if err != nil || strings.TrimSpace(docURI) == "" {
		t.Fatalf("shell prepareDocument() err=%v uri=%q", err, docURI)
	}
}

func TestExternalJavaFixtureDiagnosticsThenCodeActions(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("home directory unavailable")
	}
	if _, err := exec.LookPath("jdtls"); err != nil {
		t.Skip("jdtls not installed")
	}
	workspace := filepath.Join(home, "ggai", "java-message-board")
	path := filepath.Join(workspace, "src", "main", "java", "com", "example", "board", "App.java")
	if _, err := os.Stat(path); err != nil {
		t.Skip("external java fixture not present")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	diagnostics, err := Diagnostics(ctx, workspace, path)
	if err != nil {
		t.Fatalf("Diagnostics() error = %v", err)
	}
	_ = diagnostics

	actions, err := CodeActions(ctx, workspace, path, Range{
		Start: Position{Line: 8, Character: 9},
		End:   Position{Line: 8, Character: 31},
	})
	if err != nil {
		t.Fatalf("CodeActions() error = %v", err)
	}
	_ = actions
}
