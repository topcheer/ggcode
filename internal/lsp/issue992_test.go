package lsp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestScanWorkspaceExtensionsWorkspaceRootNotSkipped(t *testing.T) {
	for _, rootName := range []string{".work", "vendor", "build", "dist", "target"} {
		t.Run(rootName, func(t *testing.T) {
			parent := t.TempDir()
			root := filepath.Join(parent, rootName)
			if err := os.MkdirAll(root, 0o755); err != nil {
				t.Fatal(err)
			}

			for _, rel := range []string{"main.go", "pkg/util.go"} {
				if err := os.MkdirAll(filepath.Dir(filepath.Join(root, rel)), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, rel), []byte("x"), 0o644); err != nil {
					t.Fatal(err)
				}
			}

			for _, rel := range []string{
				".git/config.go",
				"node_modules/dep/index.go",
				"vendor/lib/lib.go",
			} {
				if err := os.MkdirAll(filepath.Dir(filepath.Join(root, rel)), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, rel), []byte("x"), 0o644); err != nil {
					t.Fatal(err)
				}
			}

			found := scanWorkspaceExtensions(root)
			if _, ok := found[".go"]; !ok {
				t.Fatalf("root %q: expected .go extension evidence from workspace scan, got %v", rootName, found)
			}
		})
	}
}

func TestScanWorkspaceExtensionsSkipsHiddenSubdirs(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "app.ts"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".cache"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".cache", "gen.ts"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	found := scanWorkspaceExtensions(root)
	if _, ok := found[".ts"]; !ok {
		t.Fatalf("expected .ts evidence from root file, got %v", found)
	}
}

func TestCallFailsFastWhenSessionFailed(t *testing.T) {
	c := &stdioClient{pending: make(map[string]chan rpcEnvelope)}
	c.failMu.Lock()
	c.failed = true
	c.failMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	start := time.Now()
	err := c.call(ctx, "textDocument/hover", map[string]any{}, nil)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected error from call on failed session")
	}
	if !strings.Contains(err.Error(), "session terminated") {
		t.Fatalf("unexpected error: %v", err)
	}
	if elapsed > time.Second {
		t.Fatalf("call blocked %v before failing; expected fast failure", elapsed)
	}
}

func TestParseWorkspaceEditDeduplicatesDualSources(t *testing.T) {
	const editJSON = `{
		"changes": {
			"file:///work/a.go": [
				{"range": {"start": {"line": 0, "character": 0}, "end": {"line": 0, "character": 5}}, "newText": "hello"},
				{"range": {"start": {"line": 1, "character": 2}, "end": {"line": 1, "character": 4}}, "newText": "world"}
			]
		},
		"documentChanges": [
			{
				"textDocument": {"uri": "file:///work/a.go"},
				"edits": [
					{"range": {"start": {"line": 0, "character": 0}, "end": {"line": 0, "character": 5}}, "newText": "hello"}
				]
			},
			{
				"textDocument": {"uri": "file:///work/b.go"},
				"edits": [
					{"range": {"start": {"line": 3, "character": 1}, "end": {"line": 3, "character": 9}}, "newText": "other"}
				]
			}
		]
	}`
	edits, _ := parseWorkspaceEdit(json.RawMessage(editJSON))
	if len(edits) != 2 {
		t.Fatalf("expected 2 files, got %d: %+v", len(edits), edits)
	}
	for _, fe := range edits {
		switch fe.Path {
		case "/work/a.go":
			if len(fe.Edits) != 2 {
				t.Fatalf("a.go: expected 2 deduped edits, got %d: %+v", len(fe.Edits), fe.Edits)
			}
		case "/work/b.go":
			if len(fe.Edits) != 1 {
				t.Fatalf("b.go: expected 1 edit, got %d", len(fe.Edits))
			}
		default:
			t.Fatalf("unexpected path %q", fe.Path)
		}
	}
}
