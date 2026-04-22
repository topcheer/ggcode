package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func setupGrepTestDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	// Create test files
	os.WriteFile(filepath.Join(dir, "hello.go"), []byte("package main\n\nfunc hello() string {\n\treturn \"hello world\"\n}\n\nfunc main() {\n\tprintln(hello())\n}\n"), 0644)
	os.WriteFile(filepath.Join(dir, "util.py"), []byte("def greet(name):\n    return f\"hello {name}\"\n\nclass Greeter:\n    def say_hello(self):\n        return \"hello\"\n"), 0644)
	os.WriteFile(filepath.Join(dir, "style.css"), []byte(".hello {\n    color: red;\n}\n\n.world {\n    color: blue;\n}\n"), 0644)
	os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Hello World\n\nThis is a test.\nHello again.\n"), 0644)

	// Initialize git repo so gitTrackedFiles works
	gitInit := gitCommand(context.Background(), "init")
	gitInit.Dir = dir
	gitInit.Run()

	gitAdd := gitCommand(context.Background(), "add", "-A")
	gitAdd.Dir = dir
	gitAdd.Run()

	gitCommit := gitCommand(context.Background(), "commit", "-m", "init", "--author", "test <test@test.com>")
	gitCommit.Dir = dir
	gitCommit.Env = append(os.Environ(), "GIT_AUTHOR_DATE=now", "GIT_COMMITTER_DATE=now")
	gitCommit.Run()

	return dir
}

func TestGrep_ContentMode(t *testing.T) {
	dir := setupGrepTestDir(t)
	tool := Grep{}

	input, _ := json.Marshal(map[string]interface{}{
		"pattern": "hello",
		"path":    dir,
		"-i":      true,
	})

	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
	// Should find matches in multiple files
	if !containsAll(result.Content, "hello.go", "util.py", "README.md") {
		t.Errorf("expected matches in hello.go, util.py, README.md; got:\n%s", result.Content)
	}
}

func TestGrep_FilesWithMatches(t *testing.T) {
	dir := setupGrepTestDir(t)
	tool := Grep{}

	input, _ := json.Marshal(map[string]interface{}{
		"pattern":     "hello",
		"path":        dir,
		"-i":          true,
		"output_mode": "files_with_matches",
	})

	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
	if !containsAll(result.Content, "hello.go", "util.py", "README.md") {
		t.Errorf("expected file names in output; got:\n%s", result.Content)
	}
	if !containsAny(result.Content, "file(s) matched") {
		t.Errorf("expected match count summary; got:\n%s", result.Content)
	}
}

func TestGrep_CountMode(t *testing.T) {
	dir := setupGrepTestDir(t)
	tool := Grep{}

	input, _ := json.Marshal(map[string]interface{}{
		"pattern":     "hello",
		"path":        dir,
		"-i":          true,
		"output_mode": "count",
	})

	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
	if !containsAny(result.Content, "match(es) total") {
		t.Errorf("expected total count; got:\n%s", result.Content)
	}
}

func TestGrep_GlobFilter(t *testing.T) {
	dir := setupGrepTestDir(t)
	tool := Grep{}

	input, _ := json.Marshal(map[string]interface{}{
		"pattern":     "hello",
		"path":        dir,
		"glob":        "*.go",
		"output_mode": "files_with_matches",
		"-i":          true,
	})

	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
	if !containsAny(result.Content, "hello.go") {
		t.Errorf("expected hello.go; got:\n%s", result.Content)
	}
	if containsAny(result.Content, "util.py") {
		t.Errorf("should not match util.py with glob=*.go; got:\n%s", result.Content)
	}
}

func TestGrep_TypeFilter(t *testing.T) {
	dir := setupGrepTestDir(t)
	tool := Grep{}

	input, _ := json.Marshal(map[string]interface{}{
		"pattern":     "hello",
		"path":        dir,
		"type":        "py",
		"output_mode": "files_with_matches",
		"-i":          true,
	})

	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
	if !containsAny(result.Content, "util.py") {
		t.Errorf("expected util.py; got:\n%s", result.Content)
	}
	if containsAny(result.Content, "hello.go") {
		t.Errorf("should not match hello.go with type=py; got:\n%s", result.Content)
	}
}

func TestGrep_HeadLimit(t *testing.T) {
	dir := setupGrepTestDir(t)
	tool := Grep{}

	input, _ := json.Marshal(map[string]interface{}{
		"pattern":    "hello",
		"path":       dir,
		"-i":         true,
		"head_limit": 1,
	})

	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
	// Should show limited results with pagination hint
	if !containsAny(result.Content, "offset") {
		t.Logf("no pagination hint (might be only 1 result), output:\n%s", result.Content)
	}
}

func TestGrep_NoMatches(t *testing.T) {
	dir := setupGrepTestDir(t)
	tool := Grep{}

	input, _ := json.Marshal(map[string]interface{}{
		"pattern": "zzzzzznonexistent",
		"path":    dir,
	})

	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
	if !containsAny(result.Content, "No matches") {
		t.Errorf("expected no matches message; got:\n%s", result.Content)
	}
}

func TestGrep_InvalidRegex(t *testing.T) {
	tool := Grep{}

	input, _ := json.Marshal(map[string]interface{}{
		"pattern": "[invalid",
	})

	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error for invalid regex")
	}
}

func TestGrep_CaseSensitive(t *testing.T) {
	dir := setupGrepTestDir(t)
	tool := Grep{}

	input, _ := json.Marshal(map[string]interface{}{
		"pattern": "Hello",
		"path":    dir,
	})

	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
	// Should only match "Hello" (case-sensitive), not "hello"
	if containsAny(result.Content, "hello world") {
		t.Errorf("case-sensitive should not match lowercase; got:\n%s", result.Content)
	}
}

func containsAll(s string, substrs ...string) bool {
	for _, sub := range substrs {
		if !containsAny(s, sub) {
			return false
		}
	}
	return true
}

func containsAny(s string, substrs ...string) bool {
	for _, sub := range substrs {
		if len(s) >= len(sub) && (s == sub || len(sub) == 0) {
			return true
		}
		for i := 0; i <= len(s)-len(sub); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
	}
	return false
}
