package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEditFile_LineNumberPrefixStripped(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "test.go")
	content := "package main\n\nfunc hello() {\n\tprintln(\"hi\")\n}\n"
	if err := os.WriteFile(fp, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	// LLM pasted back read_file's `cat -n` output verbatim.
	input, _ := json.Marshal(map[string]any{
		"file_path": fp,
		"old_text":  "     3\tfunc hello() {\n     4\t\tprintln(\"hi\")\n     5\t}",
		"new_text":  "func hello() {\n\tprintln(\"hello\")\n}",
	})
	res, err := EditFile{}.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("expected line-number prefix to be auto-stripped; got error: %s", res.Content)
	}
	got, _ := os.ReadFile(fp)
	if !strings.Contains(string(got), `println("hello")`) {
		t.Errorf("file not updated: %q", got)
	}
}

func TestEditFile_CRLFFallback(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "test.txt")
	if err := os.WriteFile(fp, []byte("alpha\r\nbeta\r\ngamma\r\n"), 0644); err != nil {
		t.Fatal(err)
	}

	input, _ := json.Marshal(map[string]any{
		"file_path": fp,
		"old_text":  "alpha\nbeta", // LF instead of file's CRLF
		"new_text":  "ALPHA\nBETA",
	})
	res, err := EditFile{}.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("expected CRLF fallback; got error: %s", res.Content)
	}
	got, _ := os.ReadFile(fp)
	if string(got) != "ALPHA\r\nBETA\r\ngamma\r\n" {
		t.Errorf("CRLF preservation failed: %q", got)
	}
}

func TestEditFile_TrailingWhitespaceTolerant(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "test.txt")
	// File has a trailing space on the second line.
	if err := os.WriteFile(fp, []byte("first\nsecond  \nthird\n"), 0644); err != nil {
		t.Fatal(err)
	}

	input, _ := json.Marshal(map[string]any{
		"file_path": fp,
		"old_text":  "first\nsecond\nthird", // no trailing spaces
		"new_text":  "ONE\nTWO\nTHREE",
	})
	res, err := EditFile{}.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("expected trailing-whitespace fallback; got error: %s", res.Content)
	}
	got, _ := os.ReadFile(fp)
	if string(got) != "ONE\nTWO\nTHREE\n" {
		t.Errorf("unexpected content: %q", got)
	}
}

func TestEditFile_NonUniqueIncludesLineNumbers(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "test.txt")
	if err := os.WriteFile(fp, []byte("dup\nA\ndup\nB\ndup\n"), 0644); err != nil {
		t.Fatal(err)
	}

	input, _ := json.Marshal(map[string]any{
		"file_path": fp,
		"old_text":  "dup",
		"new_text":  "X",
	})
	res, err := EditFile{}.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("expected non-unique error")
	}
	if !strings.Contains(res.Content, "line(s)") {
		t.Errorf("error should list match line numbers, got: %s", res.Content)
	}
	for _, ln := range []string{"1", "3", "5"} {
		if !strings.Contains(res.Content, ln) {
			t.Errorf("expected line %s in error; got: %s", ln, res.Content)
		}
	}
	if !strings.Contains(res.Content, "replace_all") {
		t.Errorf("expected replace_all hint; got: %s", res.Content)
	}
}

func TestEditFile_DescriptionNotRequired(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "test.txt")
	os.WriteFile(fp, []byte("hello\n"), 0644)

	// No "description" field — must succeed under the new schema.
	input, _ := json.Marshal(map[string]any{
		"file_path": fp,
		"old_text":  "hello",
		"new_text":  "world",
	})
	res, err := EditFile{}.Execute(context.Background(), input)
	if err != nil || res.IsError {
		t.Fatalf("description should be optional; got err=%v res=%v", err, res)
	}
}

func TestEditFile_LeadingIndentShift(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "test.go")
	// File has the comment indented with two tabs.
	content := "package main\n\nfunc x() {\n\tif true {\n\t\t// Device flow: foo\n\t\tdoThing()\n\t}\n}\n"
	if err := os.WriteFile(fp, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	// LLM provides old_text with NO leading whitespace.
	input, _ := json.Marshal(map[string]any{
		"file_path": fp,
		"old_text":  "// Device flow: foo",
		"new_text":  "// Device flow: bar",
	})
	res, err := EditFile{}.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("expected leading-indent-shift to succeed; got: %s", res.Content)
	}
	got, _ := os.ReadFile(fp)
	want := "package main\n\nfunc x() {\n\tif true {\n\t\t// Device flow: bar\n\t\tdoThing()\n\t}\n}\n"
	if string(got) != want {
		t.Errorf("unexpected content:\n got: %q\nwant: %q", got, want)
	}
}

func TestEditFile_LeadingIndentShift_MultiLine(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "test.go")
	// Block has consistent 2-tab base indent + relative indent within.
	content := "func x() {\n\t\tif a {\n\t\t\treturn 1\n\t\t}\n}\n"
	if err := os.WriteFile(fp, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	// LLM strips the 2-tab base but keeps relative indent.
	input, _ := json.Marshal(map[string]any{
		"file_path": fp,
		"old_text":  "if a {\n\treturn 1\n}",
		"new_text":  "if a {\n\treturn 2\n}",
	})
	res, err := EditFile{}.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("expected leading-indent-shift to succeed; got: %s", res.Content)
	}
	got, _ := os.ReadFile(fp)
	want := "func x() {\n\t\tif a {\n\t\t\treturn 2\n\t\t}\n}\n"
	if string(got) != want {
		t.Errorf("unexpected content:\n got: %q\nwant: %q", got, want)
	}
}

func TestEditFile_LeadingIndentShift_OverIndentedOldText(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "test.go")
	content := "func catalog(key string) string {\n\tswitch key {\n\tcase \"hint.follow_panel\":\n\t\treturn \"Ctrl+N follow\"\n\t}\n\treturn \"\"\n}\n"
	if err := os.WriteFile(fp, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	input, _ := json.Marshal(map[string]any{
		"file_path": fp,
		"old_text":  "\t\tcase \"hint.follow_panel\":\n\t\t\treturn \"Ctrl+N follow\"",
		"new_text":  "\t\tcase \"hint.follow_panel\":\n\t\t\treturn \"Ctrl+N unfollow\"",
	})
	res, err := EditFile{}.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("expected over-indented old_text to succeed; got: %s", res.Content)
	}
	got, _ := os.ReadFile(fp)
	want := "func catalog(key string) string {\n\tswitch key {\n\tcase \"hint.follow_panel\":\n\t\treturn \"Ctrl+N unfollow\"\n\t}\n\treturn \"\"\n}\n"
	if string(got) != want {
		t.Errorf("unexpected content:\n got: %q\nwant: %q", got, want)
	}
}

func TestEditFile_SingleLineReadFileAnchor(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "test.txt")
	if err := os.WriteFile(fp, []byte("alpha\nbeta\ngamma\n"), 0644); err != nil {
		t.Fatal(err)
	}

	input, _ := json.Marshal(map[string]any{
		"file_path": fp,
		"old_text":  "     2\tbeta",
		"new_text":  "     2\tBETA",
	})
	res, err := EditFile{}.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("expected single-line numbered anchor to succeed; got: %s", res.Content)
	}
	got, _ := os.ReadFile(fp)
	if string(got) != "alpha\nBETA\ngamma\n" {
		t.Errorf("unexpected content: %q", got)
	}
}

func TestEditFile_ReadFileAnchorDisambiguatesDuplicateText(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "test.go")
	content := "package main\n\nfunc a() {\n\tprintln(\"same\")\n}\n\nfunc b() {\n\tprintln(\"same\")\n}\n"
	if err := os.WriteFile(fp, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	input, _ := json.Marshal(map[string]any{
		"file_path": fp,
		"old_text":  "     8\t\tprintln(\"same\")",
		"new_text":  "     8\t\tprintln(\"SECOND\")",
	})
	res, err := EditFile{}.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("expected numbered anchor to disambiguate duplicate text; got: %s", res.Content)
	}
	got, _ := os.ReadFile(fp)
	want := "package main\n\nfunc a() {\n\tprintln(\"same\")\n}\n\nfunc b() {\n\tprintln(\"SECOND\")\n}\n"
	if string(got) != want {
		t.Errorf("unexpected content:\n got: %q\nwant: %q", got, want)
	}
}

func TestEditFile_ReadFileWrapperLinesAreIgnored(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "test.go")
	content := "package main\n\nfunc main() {\n\tfmt.Println(\"hello\")\n}\n"
	if err := os.WriteFile(fp, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	input, _ := json.Marshal(map[string]any{
		"file_path": fp,
		"old_text":  "[indent: tab]\n     4\t\tfmt.Println(\"hello\")\n[File truncated: showing lines 1-4 of 5. Use read_file with offset/limit for more.]",
		"new_text":  "[indent: tab]\n     4\t\tfmt.Println(\"world\")",
	})
	res, err := EditFile{}.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("expected wrapper lines to be ignored; got: %s", res.Content)
	}
	got, _ := os.ReadFile(fp)
	want := "package main\n\nfunc main() {\n\tfmt.Println(\"world\")\n}\n"
	if string(got) != want {
		t.Errorf("unexpected content:\n got: %q\nwant: %q", got, want)
	}
}

func TestEditFile_ReadFileAnchorIgnoresDanglingLineNumber(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "test.go")
	content := "func describe(result string) string {\n\tcaseOne()\n\treturn result\n}\n"
	if err := os.WriteFile(fp, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	input, _ := json.Marshal(map[string]any{
		"file_path": fp,
		"old_text":  "     2\t\tcaseOne()\n     3\t\treturn result\n     4",
		"new_text":  "     2\t\tcaseTwo()\n     3\t\treturn result\n     4",
	})
	res, err := EditFile{}.Execute(context.Background(), input)
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

func TestEditFile_CorpusReplay_ValidCompatibilityCases(t *testing.T) {
	tests := []struct {
		name    string
		content string
		oldText string
		newText string
		want    string
	}{
		{
			name:    "top-level function block over-indented like corpus failures",
			content: "package tool\n\nfunc localizedGenericActivity(lang string, label string) string {\n\treturn label\n}\n",
			oldText: "\tfunc localizedGenericActivity(lang string, label string) string {\n\t\treturn label\n\t}",
			newText: "\tfunc localizedGenericActivity(lang string, label string) string {\n\t\treturn strings.TrimSpace(label)\n\t}",
			want:    "package tool\n\nfunc localizedGenericActivity(lang string, label string) string {\n\treturn strings.TrimSpace(label)\n}\n",
		},
		{
			name:    "nested schema block over-indented like parameters-json failures",
			content: "func params() string {\n\treturn `{\n\t\t\"properties\": {\n\t\t\t\"command\": {\n\t\t\t\t\"type\": \"string\",\n\t\t\t\t\"description\": \"Shell command to execute\"\n\t\t\t}\n\t\t}\n\t}`\n}\n",
			oldText: "\t\t\t\t\"command\": {\n\t\t\t\t\t\"type\": \"string\",\n\t\t\t\t\t\"description\": \"Shell command to execute\"\n\t\t\t\t}",
			newText: "\t\t\t\t\"command\": {\n\t\t\t\t\t\"type\": \"string\",\n\t\t\t\t\t\"description\": \"Shell command to execute in the background\"\n\t\t\t\t}",
			want:    "func params() string {\n\treturn `{\n\t\t\"properties\": {\n\t\t\t\"command\": {\n\t\t\t\t\"type\": \"string\",\n\t\t\t\t\"description\": \"Shell command to execute in the background\"\n\t\t\t}\n\t\t}\n\t}`\n}\n",
		},
		{
			name:    "read-file numbered import block ignores dangling final line number",
			content: "package main\n\nimport (\n\t\"strings\"\n\t\"syscall\"\n\t\"time\"\n\n\t\"github.com/hashicorp/mdns\"\n\t\"github.com/topcheer/ggcode/internal/debug\"\n)\n",
			oldText: "   4\t\t\"strings\"\n   5\t\t\"syscall\"\n   6\t\t\"time\"\n   7\n   8\t\t\"github.com/hashicorp/mdns\"\n   9\t\t\"github.com/topcheer/ggcode/internal/debug\"\n   10",
			newText: "   4\t\t\"strings\"\n   5\t\t\"syscall\"\n   6\t\t\"time\"\n   7\n   8\t\t\"github.com/hashicorp/mdns\"\n   9\t\t\"github.com/topcheer/ggcode/internal/debug\"\n   10\t\t\"github.com/topcheer/ggcode/internal/safego\"",
			want:    "package main\n\nimport (\n\t\"strings\"\n\t\"syscall\"\n\t\"time\"\n\n\t\"github.com/hashicorp/mdns\"\n\t\"github.com/topcheer/ggcode/internal/debug\"\n\t\"github.com/topcheer/ggcode/internal/safego\"\n)\n",
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
				"old_text":  tt.oldText,
				"new_text":  tt.newText,
			})
			res, err := EditFile{}.Execute(context.Background(), input)
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
