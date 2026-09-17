package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveMemoryPathConfinement(t *testing.T) {
	root := t.TempDir()

	// Valid paths map inside the store and keep the canonical virtual form.
	// The real path is built on the symlink-resolved root, so compare against
	// the resolved root (root itself usually contains no links; macOS temp
	// dirs do).
	resolvedRoot, rerr := filepath.EvalSymlinks(root)
	if rerr != nil {
		t.Fatal(rerr)
	}
	clean, real, err := resolveMemoryPath(root, "/memories/notes/todo.txt")
	if err != nil {
		t.Fatalf("valid path rejected: %v", err)
	}
	if clean != "/memories/notes/todo.txt" {
		t.Fatalf("clean = %q, want /memories/notes/todo.txt", clean)
	}
	if want := filepath.Join(resolvedRoot, "notes", "todo.txt"); real != want {
		t.Fatalf("real = %q, want %q", real, want)
	}

	// The root itself is legal (directory listing target).
	if _, real, err := resolveMemoryPath(root, "/memories"); err != nil || real != resolvedRoot {
		t.Fatalf("root path: real=%q err=%v", real, err)
	}

	for name, p := range map[string]string{
		"missing prefix":      "notes/todo.txt",
		"wrong root":          "/tmp/notes/todo.txt",
		"dot-dot traversal":   "/memories/../../etc/passwd",
		"encoded traversal":   "/memories/%2e%2e/secret",
		"encoded separator":   "/memories/notes%2ftodo.txt",
		"backslash separator": "/memories/notes%5ctodo.txt",
		"empty":               "",
	} {
		if _, _, err := resolveMemoryPath(root, p); err == nil {
			t.Errorf("%s: expected rejection of %q", name, p)
		} else if !strings.HasPrefix(err.Error(), "Error:") {
			t.Errorf("%s: error %q should keep the Error: convention", name, err)
		}
	}
}

func TestResolveMemoryPathRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link.txt")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, _, err := resolveMemoryPath(root, "/memories/link.txt"); err == nil {
		t.Fatal("symlink escaping the store must be rejected")
	}
}

func TestMemoryToolCreateViewEditDeleteRoundTrip(t *testing.T) {
	s := newMemoryToolState()
	dir := t.TempDir()

	call := func(t *testing.T, args map[string]any) toolResultProbe {
		t.Helper()
		raw, err := json.Marshal(args)
		if err != nil {
			t.Fatal(err)
		}
		res := s.executeResult(raw, dir)
		return toolResultProbe{content: res.Content, isErr: res.IsError}
	}

	// create
	if p := call(t, map[string]any{"command": "create", "path": "/memories/todo.txt", "file_text": "alpha\n"}); p.isErr {
		t.Fatalf("create failed: %s", p.content)
	}
	onDisk := filepath.Join(dir, memoryDirName, "todo.txt")
	if b, err := os.ReadFile(onDisk); err != nil || string(b) != "alpha\n" {
		t.Fatalf("on-disk content = %q err=%v, want %q", b, err, "alpha\n")
	}

	// view (full file, numbered)
	p := call(t, map[string]any{"command": "view", "path": "/memories/todo.txt"})
	if p.isErr || !strings.Contains(p.content, "1\talpha") {
		t.Fatalf("view = %q isErr=%v", p.content, p.isErr)
	}

	// str_replace
	if p := call(t, map[string]any{"command": "str_replace", "path": "/memories/todo.txt", "old_str": "alpha", "new_str": "beta"}); p.isErr {
		t.Fatalf("str_replace failed: %s", p.content)
	}
	if b, err := os.ReadFile(onDisk); err != nil || string(b) != "beta" { // line-normalized store drops the trailing newline
		t.Fatalf("after replace content = %q err=%v", b, err)
	}

	// insert
	if p := call(t, map[string]any{"command": "insert", "path": "/memories/todo.txt", "insert_line": 1, "insert_text": "gamma"}); p.isErr {
		t.Fatalf("insert failed: %s", p.content)
	}

	// view_range: only line 2 - insert at line 1 puts "gamma" after "beta",
	// so the file is beta/gamma and the slice keeps absolute line numbers.
	p = call(t, map[string]any{"command": "view", "path": "/memories/todo.txt", "view_range": []int{2, 2}})
	if p.isErr || !strings.Contains(p.content, "2\tgamma") || strings.Contains(p.content, "1\t") {
		t.Fatalf("view_range = %q isErr=%v", p.content, p.isErr)
	}

	// rename
	if p := call(t, map[string]any{"command": "rename", "old_path": "/memories/todo.txt", "new_path": "/memories/notes/done.txt"}); p.isErr {
		t.Fatalf("rename failed: %s", p.content)
	}
	if _, err := os.Stat(onDisk); !os.IsNotExist(err) {
		t.Fatalf("old path still exists after rename: err=%v", err)
	}

	// delete
	if p := call(t, map[string]any{"command": "delete", "path": "/memories/notes/done.txt"}); p.isErr {
		t.Fatalf("delete failed: %s", p.content)
	}

	// Unknown command and malformed arguments surface as error results.
	if p := call(t, map[string]any{"command": "exploit", "path": "/memories/x"}); !p.isErr {
		t.Fatalf("unknown command must be an error result, got %q", p.content)
	}
	if p := call(t, map[string]any{"command": "create", "path": "/outside/escape.txt", "file_text": "x"}); !p.isErr {
		t.Fatalf("path outside /memories must be an error result, got %q", p.content)
	}
}

// toolResultProbe decouples the test from the tool.Result struct shape.
type toolResultProbe struct {
	content string
	isErr   bool
}
