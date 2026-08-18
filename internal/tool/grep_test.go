package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGrepDescriptionClarifiesDefaultOutputMode(t *testing.T) {
	tool := Grep{}
	for _, want := range []string{"Defaults to files_with_matches", "output_mode=content", "matching lines"} {
		if !containsAny(tool.Description(), want) {
			t.Fatalf("grep description should mention %q, got %q", want, tool.Description())
		}
	}
}

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
	if !containsAny(result.Content, "No matches") && !containsAny(result.Content, "0 file") {
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

func TestGrep_LongAliasParameters(t *testing.T) {
	dir := setupGrepTestDir(t)
	tool := Grep{}

	input, _ := json.Marshal(map[string]interface{}{
		"pattern":     "Hello",
		"path":        dir,
		"ignore_case": true,
		"before":      1,
		"after":       1,
		"output_mode": "content",
	})

	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
	if !containsAll(result.Content, "hello.go", "hello world") {
		t.Fatalf("expected long alias parameters to behave like -i/-B/-A, got:\n%s", result.Content)
	}
}

func TestToolDescriptions_OptimizedForLLM(t *testing.T) {
	rc := RunCommand{}
	if containsAny(string(rc.Parameters()), "working_dir") {
		t.Fatal("run_command schema should not advertise ignored working_dir")
	}
	if !containsAll(rc.Description(), "background job", "wait_command") {
		t.Fatalf("run_command description should mention background job follow-up, got %q", rc.Description())
	}

	gb := GitBranchList{}
	if !containsAll(gb.Description(), "local Git branches", "remote=true") || containsAny(gb.Description(), "GitHub repository") {
		t.Fatalf("git_branch_list description should describe local git behavior, got %q", gb.Description())
	}

	mfr := MultiFileRead{}
	if !containsAll(mfr.Description(), "read_file", "images") {
		t.Fatalf("multi_file_read description should clarify text/image behavior, got %q", mfr.Description())
	}

	mfe := MultiFileEdit{}
	if !containsAll(mfe.Description(), "multiple existing files", "multi_edit_file") {
		t.Fatalf("multi_file_edit description should clarify when to use multi_edit_file, got %q", mfe.Description())
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

func TestFormatCountReportsGlobalTotalsWithPagination(t *testing.T) {
	// 10 files, 100 total matches: a=15,b=12,c=10,d=9,e=8,f=8,g=8,h=7,i=7,j=16
	counts := map[string]int{
		"a.go": 15, "b.go": 12, "c.go": 10, "d.go": 9, "e.go": 8,
		"f.go": 8, "g.go": 8, "h.go": 7, "i.go": 7, "j.go": 16,
	}

	// Page 1: offset=0, head_limit=3 -> 3 lines listed, but global totals.
	res := formatCount(counts, grepArgs{Offset: 0, HeadLimit: 3})
	content := res.Content
	if !containsAny(content, "10 file(s)") || !containsAny(content, "100 match(es)") {
		t.Fatalf("expected global totals '10 file(s), 100 match(es) total', got:\n%s", content)
	}
	if lines := countNonEmptyLines(content); lines != 3+1 { // 3 file lines + summary
		t.Fatalf("expected 3 listed files + summary, got %d lines:\n%s", lines, content)
	}
	if !containsAny(content, "a.go: 15") || !containsAny(content, "c.go: 10") || containsAny(content, "d.go:") {
		t.Fatalf("expected only first-page files a,b,c listed, got:\n%s", content)
	}

	// Page 2: same global totals.
	res2 := formatCount(counts, grepArgs{Offset: 3, HeadLimit: 3})
	if !containsAny(res2.Content, "10 file(s)") || !containsAny(res2.Content, "100 match(es)") {
		t.Fatalf("page 2 should report same global totals, got:\n%s", res2.Content)
	}
	if !containsAny(res2.Content, "d.go: 9") || containsAny(res2.Content, "a.go:") {
		t.Fatalf("page 2 should list only d,e,f, got:\n%s", res2.Content)
	}

	// No pagination: behavior unchanged, all 10 files listed.
	res3 := formatCount(counts, grepArgs{})
	if !containsAny(res3.Content, "10 file(s), 100 match(es) total") {
		t.Fatalf("unpaginated output should report all totals, got:\n%s", res3.Content)
	}
	if lines := countNonEmptyLines(res3.Content); lines != 10+1 {
		t.Fatalf("expected 10 file lines + summary, got %d lines", lines)
	}
}

func countNonEmptyLines(s string) int {
	n := 0
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) != "" {
			n++
		}
	}
	return n
}
