package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMultiEdit_FallbackChain(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "test.go")
	// Tab-indented file, CRLF line endings on the second site.
	content := "package main\n\nfunc a() {\n\tprintln(\"a\")\n}\nfunc b() {\r\n\tprintln(\"b\")\r\n}\n"
	if err := os.WriteFile(fp, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	input, _ := json.Marshal(map[string]any{
		"file_path": fp,
		"edits": []map[string]string{
			// edit 1: spaces instead of tab — exercises indent normalization.
			{
				"old_text": "func a() {\n    println(\"a\")\n}",
				"new_text": "func a() {\n    println(\"AAA\")\n}",
			},
			// edit 2: LF instead of CRLF — exercises CRLF fallback.
			{
				"old_text": "func b() {\n\tprintln(\"b\")\n}",
				"new_text": "func b() {\n\tprintln(\"BBB\")\n}",
			},
		},
	})
	res, err := MultiEditFile{}.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("expected fallbacks to succeed; got error: %s", res.Content)
	}
	got, _ := os.ReadFile(fp)
	gs := string(got)
	if !strings.Contains(gs, `println("AAA")`) || !strings.Contains(gs, `println("BBB")`) {
		t.Errorf("unexpected content: %q", gs)
	}
	// CRLF on the second site must be preserved.
	if !strings.Contains(gs, "func b() {\r\n\tprintln(\"BBB\")\r\n}") {
		t.Errorf("CRLF preservation failed: %q", gs)
	}
}

func TestMultiEdit_NotFoundIncludesHint(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "test.txt")
	os.WriteFile(fp, []byte("\tindented\n"), 0644)

	input, _ := json.Marshal(map[string]any{
		"file_path": fp,
		"edits": []map[string]string{
			{"old_text": "completely unrelated text", "new_text": "x"},
		},
	})
	res, err := MultiEditFile{}.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("expected error for missing match")
	}
	// Hint may vary, but the message should be more than just "not found".
	if !strings.Contains(res.Content, "edits[0]") {
		t.Errorf("expected edit index in error; got: %s", res.Content)
	}
}

func TestMultiEdit_ReadFileAnchorsDuplicateText(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "test.go")
	content := "package main\n\nfunc a() {\n\tprintln(\"same\")\n}\n\nfunc b() {\n\tprintln(\"same\")\n}\n"
	if err := os.WriteFile(fp, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	input, _ := json.Marshal(map[string]any{
		"file_path": fp,
		"edits": []map[string]string{
			{
				"old_text": "     4\t\tprintln(\"same\")",
				"new_text": "     4\t\tprintln(\"FIRST\")",
			},
			{
				"old_text": "     8\t\tprintln(\"same\")",
				"new_text": "     8\t\tprintln(\"SECOND\")",
			},
		},
	})
	res, err := MultiEditFile{}.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("expected numbered anchors to disambiguate duplicates; got: %s", res.Content)
	}

	got, _ := os.ReadFile(fp)
	want := "package main\n\nfunc a() {\n\tprintln(\"FIRST\")\n}\n\nfunc b() {\n\tprintln(\"SECOND\")\n}\n"
	if string(got) != want {
		t.Errorf("unexpected content:\n got: %q\nwant: %q", got, want)
	}
}

func TestMultiEdit_ReadFileWrapperLinesAreIgnored(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "test.go")
	content := "package main\n\nfunc main() {\n\tfmt.Println(\"hello\")\n\tfmt.Println(\"again\")\n}\n"
	if err := os.WriteFile(fp, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	input, _ := json.Marshal(map[string]any{
		"file_path": fp,
		"edits": []map[string]string{
			{
				"old_text": "[indent: tab]\n     4\t\tfmt.Println(\"hello\")",
				"new_text": "[indent: tab]\n     4\t\tfmt.Println(\"world\")",
			},
			{
				"old_text": "     5\t\tfmt.Println(\"again\")\n[File truncated: showing lines 1-5 of 6. Use read_file with offset/limit for more.]",
				"new_text": "     5\t\tfmt.Println(\"done\")",
			},
		},
	})
	res, err := MultiEditFile{}.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("expected wrapper lines to be ignored; got: %s", res.Content)
	}

	got, _ := os.ReadFile(fp)
	want := "package main\n\nfunc main() {\n\tfmt.Println(\"world\")\n\tfmt.Println(\"done\")\n}\n"
	if string(got) != want {
		t.Errorf("unexpected content:\n got: %q\nwant: %q", got, want)
	}
}

func TestMultiEdit_LeadingIndentShift_OverIndentedOldText(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "test.go")
	content := "func catalog(key string) string {\n\tswitch key {\n\tcase \"hint.follow_panel\":\n\t\treturn \"Ctrl+N follow\"\n\tcase \"hint.unfollow_panel\":\n\t\treturn \"Ctrl+N unfollow\"\n\t}\n\treturn \"\"\n}\n"
	if err := os.WriteFile(fp, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	input, _ := json.Marshal(map[string]any{
		"file_path": fp,
		"edits": []map[string]string{
			{
				"old_text": "\t\tcase \"hint.follow_panel\":\n\t\t\treturn \"Ctrl+N follow\"",
				"new_text": "\t\tcase \"hint.follow_panel\":\n\t\t\treturn \"Ctrl+F follow\"",
			},
			{
				"old_text": "\t\tcase \"hint.unfollow_panel\":\n\t\t\treturn \"Ctrl+N unfollow\"",
				"new_text": "\t\tcase \"hint.unfollow_panel\":\n\t\t\treturn \"Ctrl+F unfollow\"",
			},
		},
	})
	res, err := MultiEditFile{}.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("expected over-indented old_text edits to succeed; got: %s", res.Content)
	}

	got, _ := os.ReadFile(fp)
	want := "func catalog(key string) string {\n\tswitch key {\n\tcase \"hint.follow_panel\":\n\t\treturn \"Ctrl+F follow\"\n\tcase \"hint.unfollow_panel\":\n\t\treturn \"Ctrl+F unfollow\"\n\t}\n\treturn \"\"\n}\n"
	if string(got) != want {
		t.Errorf("unexpected content:\n got: %q\nwant: %q", got, want)
	}
}

func TestMultiEdit_ReadFileAnchorIgnoresDanglingLineNumber(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "test.go")
	content := "func describe(result string) string {\n\tcaseOne()\n\treturn result\n}\n"
	if err := os.WriteFile(fp, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	input, _ := json.Marshal(map[string]any{
		"file_path": fp,
		"edits": []map[string]string{
			{
				"old_text": "     2\t\tcaseOne()\n     3\t\treturn result\n     4",
				"new_text": "     2\t\tcaseTwo()\n     3\t\treturn result\n     4",
			},
		},
	})
	res, err := MultiEditFile{}.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("expected dangling line-number fragment to be ignored; got: %s", res.Content)
	}

	got, _ := os.ReadFile(fp)
	want := "func describe(result string) string {\n\tcaseTwo()\n\treturn result\n}\n"
	if string(got) != want {
		t.Errorf("unexpected content:\n got: %q\nwant: %q", got, want)
	}
}

func TestMultiEdit_CorpusReplay_ValidCompatibilityCases(t *testing.T) {
	tests := []struct {
		name    string
		content string
		edits   []map[string]string
		want    string
	}{
		{
			name:    "top-level and nested blocks over-indented like corpus failures",
			content: "package tool\n\nfunc localizedGenericActivity(lang string, label string) string {\n\treturn label\n}\n\nfunc params() string {\n\treturn `{\n\t\t\"properties\": {\n\t\t\t\"command\": {\n\t\t\t\t\"type\": \"string\",\n\t\t\t\t\"description\": \"Shell command to execute\"\n\t\t\t}\n\t\t}\n\t}`\n}\n",
			edits: []map[string]string{
				{
					"old_text": "\tfunc localizedGenericActivity(lang string, label string) string {\n\t\treturn label\n\t}",
					"new_text": "\tfunc localizedGenericActivity(lang string, label string) string {\n\t\treturn strings.TrimSpace(label)\n\t}",
				},
				{
					"old_text": "\t\t\t\t\"command\": {\n\t\t\t\t\t\"type\": \"string\",\n\t\t\t\t\t\"description\": \"Shell command to execute\"\n\t\t\t\t}",
					"new_text": "\t\t\t\t\"command\": {\n\t\t\t\t\t\"type\": \"string\",\n\t\t\t\t\t\"description\": \"Shell command to execute in the background\"\n\t\t\t\t}",
				},
			},
			want: "package tool\n\nfunc localizedGenericActivity(lang string, label string) string {\n\treturn strings.TrimSpace(label)\n}\n\nfunc params() string {\n\treturn `{\n\t\t\"properties\": {\n\t\t\t\"command\": {\n\t\t\t\t\"type\": \"string\",\n\t\t\t\t\"description\": \"Shell command to execute in the background\"\n\t\t\t}\n\t\t}\n\t}`\n}\n",
		},
		{
			name:    "numbered import block ignores dangling final line number like corpus failures",
			content: "package main\n\nimport (\n\t\"strings\"\n\t\"syscall\"\n\t\"time\"\n\n\t\"github.com/hashicorp/mdns\"\n\t\"github.com/topcheer/ggcode/internal/debug\"\n)\n",
			edits: []map[string]string{
				{
					"old_text": "   4\t\t\"strings\"\n   5\t\t\"syscall\"\n   6\t\t\"time\"\n   7\n   8\t\t\"github.com/hashicorp/mdns\"\n   9\t\t\"github.com/topcheer/ggcode/internal/debug\"\n   10",
					"new_text": "   4\t\t\"strings\"\n   5\t\t\"syscall\"\n   6\t\t\"time\"\n   7\n   8\t\t\"github.com/hashicorp/mdns\"\n   9\t\t\"github.com/topcheer/ggcode/internal/debug\"\n   10\t\t\"github.com/topcheer/ggcode/internal/safego\"",
				},
			},
			want: "package main\n\nimport (\n\t\"strings\"\n\t\"syscall\"\n\t\"time\"\n\n\t\"github.com/hashicorp/mdns\"\n\t\"github.com/topcheer/ggcode/internal/debug\"\n\t\"github.com/topcheer/ggcode/internal/safego\"\n)\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			fp := filepath.Join(dir, "test.go")
			if err := os.WriteFile(fp, []byte(tt.content), 0644); err != nil {
				t.Fatal(err)
			}

			input, _ := json.Marshal(map[string]any{
				"file_path": fp,
				"edits":     tt.edits,
			})
			res, err := MultiEditFile{}.Execute(context.Background(), input)
			if err != nil {
				t.Fatal(err)
			}
			if res.IsError {
				t.Fatalf("expected corpus replay case to succeed; got: %s", res.Content)
			}

			got, _ := os.ReadFile(fp)
			if string(got) != tt.want {
				t.Errorf("unexpected content:\n got: %q\nwant: %q", got, tt.want)
			}
		})
	}
}

func TestMultiEdit_CorpusReplay_InvalidCases(t *testing.T) {
	tests := []struct {
		name       string
		content    string
		edits      []map[string]string
		wantSubstr string
	}{
		{
			name:    "empty old_text remains invalid",
			content: "alpha\nbeta\n",
			edits: []map[string]string{
				{"old_text": "", "new_text": "x"},
			},
			wantSubstr: "old_text must not be empty",
		},
		{
			name:    "non-unique old_text remains invalid without anchor",
			content: "release-smoke-linux:\n  needs:\nrelease-smoke-linux:\n  needs:\nrelease-smoke-linux:\n  needs:\n",
			edits: []map[string]string{
				{"old_text": "release-smoke-linux:\n  needs:", "new_text": "release-smoke-linux:\n  needs: [verify]"},
			},
			wantSubstr: "must be unique",
		},
		{
			name:    "multi_edit still matches against original file only",
			content: "alpha\nbeta\ngamma\n",
			edits: []map[string]string{
				{"old_text": "beta", "new_text": "BETA"},
				{"old_text": "BETA", "new_text": "BETA2"},
			},
			wantSubstr: "old_text not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			fp := filepath.Join(dir, "test.txt")
			if err := os.WriteFile(fp, []byte(tt.content), 0644); err != nil {
				t.Fatal(err)
			}

			input, _ := json.Marshal(map[string]any{
				"file_path": fp,
				"edits":     tt.edits,
			})
			res, err := MultiEditFile{}.Execute(context.Background(), input)
			if err != nil {
				t.Fatal(err)
			}
			if !res.IsError {
				t.Fatal("expected invalid corpus replay case to fail")
			}
			if !strings.Contains(res.Content, tt.wantSubstr) {
				t.Fatalf("expected error containing %q, got: %s", tt.wantSubstr, res.Content)
			}
		})
	}
}

func TestWriteFile_CreatesParentDirs(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "a", "b", "c", "out.txt")

	input, _ := json.Marshal(map[string]any{
		"path":    fp,
		"content": "hello",
	})
	res, err := WriteFile{}.Execute(context.Background(), input)
	if err != nil || res.IsError {
		t.Fatalf("write should auto-create parent dirs; got err=%v res=%v", err, res)
	}
	got, err := os.ReadFile(fp)
	if err != nil {
		t.Fatalf("file not written: %v", err)
	}
	if string(got) != "hello" {
		t.Errorf("unexpected content: %q", got)
	}
}

func TestResolveOldText_PreservesUnchangedOnExactMatch(t *testing.T) {
	mr := resolveOldText("hello world", "hello")
	if mr.canonical != "hello" || mr.transform != "" {
		t.Errorf("exact match should return unchanged; got %q, transform=%q", mr.canonical, mr.transform)
	}
}

func TestStripLineNumberPrefix_NoFalsePositive(t *testing.T) {
	// Single line that happens to have digits + tab prefix should not be
	// treated as line-numbered (requires >=2 such lines).
	in := "42\tprice: 12.99"
	if got := stripLineNumberPrefix(in); got != in {
		t.Errorf("expected no-op on single-line digit-tab input; got %q", got)
	}
}

func TestFindMatchLineNumbers(t *testing.T) {
	content := "dup\nA\ndup\nB\ndup\n"
	lines := findMatchLineNumbers(content, "dup")
	want := []int{1, 3, 5}
	if len(lines) != len(want) {
		t.Fatalf("got %v, want %v", lines, want)
	}
	for i, n := range want {
		if lines[i] != n {
			t.Errorf("line[%d]=%d, want %d", i, lines[i], n)
		}
	}
}
