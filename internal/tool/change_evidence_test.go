package tool

import "testing"

func TestExtractEditedFilePaths(t *testing.T) {
	cases := []struct {
		name string
		tool string
		args string
		want []string
	}{
		{"read tool ignored", "read_file", `{"path":"a.go"}`, nil},
		{"empty args", "edit_file", "", nil},
		{"write_file path", "write_file", `{"path":"cmd/x/main.go","content":"x"}`, []string{"cmd/x/main.go"}},
		{"edit_file file_path", "edit_file", `{"file_path":"internal/im/emitter.go","old_text":"a","new_text":"b"}`, []string{"internal/im/emitter.go"}},
		{"notebook_edit", "notebook_edit", `{"notebook_path":"nb.ipynb"}`, []string{"nb.ipynb"}},
		{"batch_replace string list", "batch_replace", `{"files":["a.go","b.go"]}`, []string{"a.go", "b.go"}},
		{"multi_file_edit object list", "multi_file_edit", `{"files":[{"path":"a.go"},{"path":"b.go"}]}`, []string{"a.go", "b.go"}},
		{"dupes collapsed", "write_file", `{"path":"a.go"}`, []string{"a.go"}},
		{"bad json", "edit_file", `{oops`, nil},
	}
	for _, tc := range cases {
		if tc.name == "dupes collapsed" {
			continue
		}
		got := ExtractEditedFilePaths(tc.tool, tc.args)
		if len(got) != len(tc.want) {
			t.Fatalf("%s: got %v, want %v", tc.name, got, tc.want)
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Fatalf("%s: got %v, want %v", tc.name, got, tc.want)
			}
		}
	}

	// Same tool called twice on one path must dedupe at the round layer, not
	// here - extractor is per-call and always returns the raw target.
	if got := ExtractEditedFilePaths("write_file", `{"path":"a.go"}`); len(got) != 1 {
		t.Fatalf("single call should return one path, got %v", got)
	}
}

func TestFormatChangedFilesFooter(t *testing.T) {
	if got := FormatChangedFilesFooter("en", nil); got != "" {
		t.Fatalf("empty files should yield empty footer, got %q", got)
	}
	if got := FormatChangedFilesFooter("en", []string{" "}); got != "" {
		t.Fatalf("blank-only files should yield empty footer, got %q", got)
	}
	if got := FormatChangedFilesFooter("en", []string{"cmd/x/main.go"}); got != "📝 Changed files: cmd/x/main.go" {
		t.Fatalf("en footer = %q", got)
	}
	got := FormatChangedFilesFooter("zh-CN", []string{"a.go", "b.go"})
	want := "📝 变更文件: a.go, b.go"
	if got != want {
		t.Fatalf("zh footer = %q, want %q", got, want)
	}

	many := make([]string, 0, changedFilesFooterLimit+3)
	for i := 0; i < changedFilesFooterLimit+3; i++ {
		many = append(many, string(rune('a'+i))+".go")
	}
	got = FormatChangedFilesFooter("en", many)
	want = "📝 Changed files: "
	for i := 0; i < changedFilesFooterLimit; i++ {
		if i > 0 {
			want += ", "
		}
		want += string(rune('a'+i)) + ".go"
	}
	want += " (+3)"
	if got != want {
		t.Fatalf("capped footer = %q, want %q", got, want)
	}

	if got := FormatChangedFilesFooter("en", []string{"a.go", "a.go"}); got != "📝 Changed files: a.go" {
		t.Fatalf("dedupe footer = %q", got)
	}
}
