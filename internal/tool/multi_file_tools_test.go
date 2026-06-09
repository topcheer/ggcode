package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMultiFileToolDescriptionsClarifyAnchorsAndAtomicity(t *testing.T) {
	readDesc := MultiFileRead{}.Description()
	for _, want := range []string{"copy/paste anchors", "paths absolute and unique", "offset/limit"} {
		if !strings.Contains(readDesc, want) {
			t.Fatalf("multi_file_read description should mention %q, got %q", want, readDesc)
		}
	}

	editTool := MultiFileEdit{}
	editDesc := editTool.Description()
	for _, want := range []string{"Use multi_edit_file instead", "ORIGINAL file content", "Default mode is atomic"} {
		if !strings.Contains(editDesc, want) {
			t.Fatalf("multi_file_edit description should mention %q, got %q", want, editDesc)
		}
	}

	params := string(editTool.Parameters())
	for _, want := range []string{"unique for that file", "including the line-number prefixes", "partial_success writes successful files"} {
		if !strings.Contains(params, want) {
			t.Fatalf("multi_file_edit schema should mention %q, got %s", want, params)
		}
	}
}

func TestMultiFileRead_BasicAndOrdered(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.go")
	b := filepath.Join(dir, "b.yaml")
	missing := filepath.Join(dir, "missing.txt")
	if err := os.WriteFile(a, []byte("package main\n\nfunc a() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b, []byte("root:\n  key: value\n  other: thing\n"), 0644); err != nil {
		t.Fatal(err)
	}

	input, _ := json.Marshal(map[string]any{
		"files": []map[string]any{
			{"path": a, "limit": 3},
			{"path": b, "offset": 2, "limit": 1},
			{"path": missing},
		},
	})
	res, err := MultiFileRead{}.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("expected success, got: %s", res.Content)
	}
	if !strings.Contains(res.Content, "[multi_file_read summary] requested=3 succeeded=2 failed=1 skipped=0") {
		t.Fatalf("unexpected summary: %s", res.Content)
	}
	first := strings.Index(res.Content, "=== FILE: "+a+" ===")
	second := strings.Index(res.Content, "=== FILE: "+b+" ===")
	third := strings.Index(res.Content, "=== ERROR: "+missing+" ===")
	if !(first >= 0 && second > first && third > second) {
		t.Fatalf("expected blocks in input order, got: %s", res.Content)
	}
	if !strings.Contains(res.Content, "     1\tpackage main") {
		t.Fatalf("expected numbered output for first file, got: %s", res.Content)
	}
	if !strings.Contains(res.Content, "[indent: 2 spaces]") || !strings.Contains(res.Content, "     2\t  key: value") {
		t.Fatalf("expected metadata and offset/limit range for second file, got: %s", res.Content)
	}
}

func TestMultiFileRead_RejectsBadPathsAndLimits(t *testing.T) {
	input, _ := json.Marshal(map[string]any{
		"files": []map[string]any{
			{"path": "relative.txt"},
		},
	})
	res, err := MultiFileRead{}.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(res.Content, "path must be absolute") {
		t.Fatalf("expected absolute-path error, got: %+v", res)
	}
}

func TestMultiFileRead_CombinedOutputLimitSkipsRemaining(t *testing.T) {
	dir := t.TempDir()
	makeBig := func(name string) string {
		fp := filepath.Join(dir, name)
		lines := make([]string, 220)
		for i := range lines {
			lines[i] = strings.Repeat("x", 900)
		}
		if err := os.WriteFile(fp, []byte(strings.Join(lines, "\n")), 0644); err != nil {
			t.Fatal(err)
		}
		return fp
	}
	a := makeBig("a.txt")
	b := makeBig("b.txt")
	c := makeBig("c.txt")
	input, _ := json.Marshal(map[string]any{
		"files": []map[string]any{
			{"path": a},
			{"path": b},
			{"path": c},
		},
	})
	res, err := MultiFileRead{}.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("expected runtime limit to be reported in-band, got: %s", res.Content)
	}
	if !strings.Contains(res.Content, "[skipped: combined output limit reached; split into a smaller batch]") {
		t.Fatalf("expected skipped marker, got: %s", res.Content)
	}
	if !strings.Contains(res.Content, "skipped=") {
		t.Fatalf("expected summary with skipped count, got: %s", res.Content)
	}
}

func TestMultiFileEdit_AtomicPlanningFailureDoesNotWrite(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.txt")
	b := filepath.Join(dir, "b.txt")
	if err := os.WriteFile(a, []byte("hello\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b, []byte("world\n"), 0644); err != nil {
		t.Fatal(err)
	}

	input, _ := json.Marshal(map[string]any{
		"files": []map[string]any{
			{"path": a, "edits": []map[string]string{{"old_text": "hello", "new_text": "HELLO"}}},
			{"path": b, "edits": []map[string]string{{"old_text": "missing", "new_text": "WORLD"}}},
		},
	})
	res, err := MultiFileEdit{}.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatalf("expected atomic planning failure to be an error, got: %s", res.Content)
	}
	var out MultiFileEditContent
	if err := json.Unmarshal([]byte(res.Content), &out); err != nil {
		t.Fatalf("expected JSON content, got %q: %v", res.Content, err)
	}
	if out.WrittenFiles != 0 || out.FailedFiles != 1 || out.SkippedFiles != 1 || out.PlannedFiles != 1 {
		t.Fatalf("unexpected result counts: %+v", out)
	}
	gotA, _ := os.ReadFile(a)
	gotB, _ := os.ReadFile(b)
	if string(gotA) != "hello\n" || string(gotB) != "world\n" {
		t.Fatalf("atomic failure should not write files, got a=%q b=%q", gotA, gotB)
	}
}

func TestMultiFileEdit_PartialSuccessWritesAndReports(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.txt")
	b := filepath.Join(dir, "b.txt")
	if err := os.WriteFile(a, []byte("hello\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b, []byte("world\n"), 0644); err != nil {
		t.Fatal(err)
	}

	input, _ := json.Marshal(map[string]any{
		"mode": "partial_success",
		"files": []map[string]any{
			{"path": a, "edits": []map[string]string{{"old_text": "hello", "new_text": "HELLO"}}},
			{"path": b, "edits": []map[string]string{{"old_text": "missing", "new_text": "WORLD"}}},
		},
	})
	res, err := MultiFileEdit{}.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatalf("expected partial success with a failure to set IsError, got: %s", res.Content)
	}
	var out MultiFileEditContent
	if err := json.Unmarshal([]byte(res.Content), &out); err != nil {
		t.Fatalf("expected JSON content, got %q: %v", res.Content, err)
	}
	if out.WrittenFiles != 1 || out.FailedFiles != 1 || len(out.WrittenPaths) != 1 || out.WrittenPaths[0] != a {
		t.Fatalf("unexpected partial-success result: %+v", out)
	}
	gotA, _ := os.ReadFile(a)
	gotB, _ := os.ReadFile(b)
	if string(gotA) != "HELLO\n" || string(gotB) != "world\n" {
		t.Fatalf("unexpected file contents: a=%q b=%q", gotA, gotB)
	}
}

func TestMultiFileEdit_IgnoresMultiFileReadWrappers(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "test.go")
	if err := os.WriteFile(fp, []byte("package main\n\nfunc main() {\n\tprintln(\"hello\")\n}\n"), 0644); err != nil {
		t.Fatal(err)
	}

	oldText := "[multi_file_read summary] requested=1 succeeded=1 failed=0 skipped=0\n\n=== FILE: " + fp + " ===\n     4\t\tprintln(\"hello\")\n[end file]"
	newText := "=== FILE: " + fp + " ===\n     4\t\tprintln(\"world\")\n[end file]"
	input, _ := json.Marshal(map[string]any{
		"files": []map[string]any{
			{"path": fp, "edits": []map[string]string{{"old_text": oldText, "new_text": newText}}},
		},
	})
	res, err := MultiFileEdit{}.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("expected wrapper lines to be ignored, got: %s", res.Content)
	}
	got, _ := os.ReadFile(fp)
	if !strings.Contains(string(got), `println("world")`) {
		t.Fatalf("expected file to be updated, got: %q", got)
	}
}
