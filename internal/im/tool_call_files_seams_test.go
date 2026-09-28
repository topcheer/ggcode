package im

import (
	"fmt"
	"testing"
)

// Pins for the formatCallFiles seams (r201). The split is behavior-preserving:
// each seam transcribes its case body 1:1 from the pre-split switch, and the
// byte-level dispatcher output is additionally pinned by TestFormatToolCallGolden
// (testdata/im_format_golden.txt).

// TestFormatReadFileSeam pins the read_file case: type-label branch, no-label
// branch, detail fallback, and range-hint suffix. The localized range hint is
// recomputed in-process; all placement strings are literal.
func TestFormatReadFileSeam(t *testing.T) {
	if got := formatReadFile(ToolLangEn, `{"file_path":"docs/report.docx"}`, ""); got != "📄 Reading Word: `report.docx`" {
		t.Errorf("labeled = %q", got)
	}
	if got := formatReadFile(ToolLangEn, `{"path":"src/main.go"}`, ""); got != "📖 Reading file: `src/main.go`" {
		t.Errorf("unlabeled = %q", got)
	}
	if got := formatReadFile(ToolLangEn, `{}`, "notes.md"); got != "📖 Reading file: `notes.md`" {
		t.Errorf("detail fallback = %q", got)
	}
	args := `{"file_path":"a.md","limit":3}`
	want := fmt.Sprintf("📖 Reading file: `a.md` %s", imFormatReadRange(ToolLangEn, args))
	if got := formatReadFile(ToolLangEn, args, ""); got != want {
		t.Errorf("range hint = %q, want %q", got, want)
	}
}

// TestFormatPathedFileCall pins the shared edit/write/notebook shape: path
// from args, detail fallback, and icon+label prefixing.
func TestFormatPathedFileCall(t *testing.T) {
	if got := formatPathedFileCall(ToolLangEn, `{"file_path":"x.go"}`, "", "✏", "edit_file"); got != "✏ Edit file: `x.go`" {
		t.Errorf("edit path = %q", got)
	}
	if got := formatPathedFileCall(ToolLangEn, `{}`, "draft.txt", "📝", "write_file"); got != "📝 Write file: `draft.txt`" {
		t.Errorf("write fallback = %q", got)
	}
	if got := formatPathedFileCall(ToolLangZhCN, `{"file_path":"n.ipynb"}`, "", "📓", "edit_notebook"); got != "📓 编辑笔记本: `n.ipynb`" {
		t.Errorf("notebook zh = %q", got)
	}
}

// TestFormatGlobSearchListSeams pins the glob / grep-search_files /
// list_directory extraction and detail fallbacks.
func TestFormatGlobSearchListSeams(t *testing.T) {
	if got := formatGlobCall(ToolLangEn, `{"pattern":"**/*.go"}`, ""); got != "🔍 Find files: `**/*.go`" {
		t.Errorf("glob pattern = %q", got)
	}
	if got := formatGlobCall(ToolLangEn, `{}`, "pkg"); got != "🔍 Find files: `pkg`" {
		t.Errorf("glob fallback = %q", got)
	}
	if got := formatSearchCall(ToolLangEn, `{"pattern":"TODO"}`, ""); got != "🔍 Search: `TODO`" {
		t.Errorf("search pattern = %q", got)
	}
	if got := formatSearchCall(ToolLangEn, `{"query":"auth"}`, ""); got != "🔍 Search: `auth`" {
		t.Errorf("search query = %q", got)
	}
	if got := formatSearchCall(ToolLangEn, `{}`, "q"); got != "🔍 Search: `q`" {
		t.Errorf("search fallback = %q", got)
	}
	if got := formatListDirectoryCall(ToolLangEn, `{"path":"/tmp"}`, ""); got != "📂 List directory: `/tmp`" {
		t.Errorf("list path = %q", got)
	}
	if got := formatListDirectoryCall(ToolLangEn, `{"directory":"internal"}`, ""); got != "📂 List directory: `internal`" {
		t.Errorf("list directory key = %q", got)
	}
	if got := formatListDirectoryCall(ToolLangEn, `{}`, "."); got != "📂 List directory: `.`" {
		t.Errorf("list fallback = %q", got)
	}
}

// TestFormatMultiFileCall pins the one-line multi-file markers in both
// languages.
func TestFormatMultiFileCall(t *testing.T) {
	cases := []struct {
		tool string
		lang ToolLanguage
		want string
	}{
		{"multi_file_read", ToolLangEn, "📖 Read multiple files"},
		{"multi_file_edit", ToolLangEn, "✏ Edit multiple files"},
		{"multi_file_write", ToolLangEn, "📝 Write multiple files"},
		{"multi_file_read", ToolLangZhCN, "📖 读取多文件"},
		{"multi_file_write", ToolLangZhCN, "📝 写入多文件"},
	}
	for _, c := range cases {
		if got := formatMultiFileCall(c.lang, c.tool); got != c.want {
			t.Errorf("%s/%v = %q, want %q", c.tool, c.lang, got, c.want)
		}
	}
}

// TestFormatCallFilesRouting pins the flattened orchestrator: every tool name
// routes to the expected rendering, unknown names return (\"\", false), and
// case order stays mutually exclusive.
func TestFormatCallFilesRouting(t *testing.T) {
	cases := []struct {
		name   string
		tool   string
		args   string
		detail string
		lang   ToolLanguage
		want   string
	}{
		{"read labeled", "read_file", `{"file_path":"docs/r.docx"}`, "", ToolLangEn, "📄 Reading Word: `r.docx`"},
		{"edit", "edit_file", `{"file_path":"x.go"}`, "", ToolLangEn, "✏ Edit file: `x.go`"},
		{"edit zh detail", "edit_file", `{}`, "y.go", ToolLangZhCN, "✏ 编辑文件: `y.go`"},
		{"write", "write_file", `{"file_path":"z.txt"}`, "", ToolLangEn, "📝 Write file: `z.txt`"},
		{"glob", "glob", `{"pattern":"a*"}`, "", ToolLangEn, "🔍 Find files: `a*`"},
		{"grep", "grep", `{"pattern":"p"}`, "", ToolLangEn, "🔍 Search: `p`"},
		{"search_files", "search_files", `{"query":"qq"}`, "", ToolLangEn, "🔍 Search: `qq`"},
		{"list", "list_directory", `{"path":"/tmp"}`, "", ToolLangEn, "📂 List directory: `/tmp`"},
		{"multi read", "multi_file_read", `{}`, "", ToolLangEn, "📖 Read multiple files"},
		{"multi edit", "multi_file_edit", `{}`, "", ToolLangEn, "✏ Edit multiple files"},
		{"multi write", "multi_file_write", `{}`, "", ToolLangEn, "📝 Write multiple files"},
		{"notebook", "notebook_edit", `{"file_path":"n.ipynb"}`, "", ToolLangEn, "📓 Edit notebook: `n.ipynb`"},
	}
	for _, c := range cases {
		tc := &ToolCallInfo{ToolName: c.tool, Args: c.args, Detail: c.detail, Lang: string(c.lang)}
		got, ok := formatCallFiles(c.lang, tc)
		if !ok {
			t.Errorf("%s: not handled", c.name)
			continue
		}
		if got != c.want {
			t.Errorf("%s = %q, want %q", c.name, got, c.want)
		}
	}
	got, ok := formatCallFiles(ToolLangEn, &ToolCallInfo{ToolName: "custom_tool"})
	if ok || got != "" {
		t.Errorf("unknown tool = (%q, %v), want (\"\", false)", got, ok)
	}
}
