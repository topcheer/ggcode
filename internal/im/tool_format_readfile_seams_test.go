package im

import (
	"fmt"
	"testing"
)

// TestGitStatusCountsBump pins the porcelain XY classification order
// extracted from the former inline switch: A beats R, D beats M, and
// unclassified pairs (e.g. "C ") bump nothing.
func TestGitStatusCountsBump(t *testing.T) {
	cases := []struct {
		name string
		x, y byte
		want gitStatusCounts
	}{
		{"untracked", '?', '?', gitStatusCounts{untracked: 1}},
		{"added on x", 'A', ' ', gitStatusCounts{added: 1}},
		{"added on y beats rename", 'R', 'A', gitStatusCounts{added: 1}},
		{"renamed on x", 'R', ' ', gitStatusCounts{renamed: 1}},
		{"renamed on y", ' ', 'R', gitStatusCounts{renamed: 1}},
		{"deleted beats modified", 'M', 'D', gitStatusCounts{deleted: 1}},
		{"deleted on x", 'D', ' ', gitStatusCounts{deleted: 1}},
		{"modified on x", 'M', ' ', gitStatusCounts{modified: 1}},
		{"modified on y", ' ', 'M', gitStatusCounts{modified: 1}},
		{"modified requires non-D", 'D', 'M', gitStatusCounts{deleted: 1}},
		{"unclassified bumps nothing", 'C', ' ', gitStatusCounts{}},
	}
	for _, tc := range cases {
		got := gitStatusCounts{}
		got.bumpGitStatusCount(tc.x, tc.y)
		if got != tc.want {
			t.Errorf("%s: bump(%q, %q) = %+v, want %+v", tc.name, tc.x, tc.y, got, tc.want)
		}
	}
}

// TestRenderGitStatusSummary pins the contract order (modified, added,
// deleted, renamed, untracked), the "%d kind" formatting, the ", " join,
// and the "clean" fallback.
func TestRenderGitStatusSummary(t *testing.T) {
	if got := renderGitStatusSummary(gitStatusCounts{}); got != "clean" {
		t.Errorf("zero counts = %q, want %q", got, "clean")
	}
	got := renderGitStatusSummary(gitStatusCounts{modified: 2, added: 1, deleted: 3, renamed: 4, untracked: 5})
	want := "2 modified, 1 added, 3 deleted, 4 renamed, 5 untracked"
	if got != want {
		t.Errorf("full counts = %q, want %q", got, want)
	}
	if got := renderGitStatusSummary(gitStatusCounts{untracked: 7}); got != "7 untracked" {
		t.Errorf("single untracked = %q", got)
	}
	if got := renderGitStatusSummary(gitStatusCounts{renamed: 1, modified: 1}); got != "1 modified, 1 renamed" {
		t.Errorf("partial order = %q", got)
	}
}

// TestFormatIMGitStatusSummarySeams pins the refactored orchestrator
// end-to-end: short/empty lines are skipped, XY pairs classified, and the
// summary joined in contract order.
func TestFormatIMGitStatusSummarySeams(t *testing.T) {
	in := "?? new.txt\n M a.go\nM  b.go\nA  c.md\nD  d.txt\nR  e.f -> g.h\nab\n\n"
	want := "2 modified, 1 added, 1 deleted, 1 renamed, 1 untracked"
	if got := formatIMGitStatusSummary(in); got != want {
		t.Errorf("mixed status = %q, want %q", got, want)
	}
	if got := formatIMGitStatusSummary(""); got != "clean" {
		t.Errorf("empty output = %q, want %q", got, "clean")
	}
	if got := formatIMGitStatusSummary("\nab\n"); got != "clean" {
		t.Errorf("only short lines = %q, want %q", got, "clean")
	}
}

// TestFormatIMReadFileErrorSeam pins the error branch byte-for-byte.
func TestFormatIMReadFileErrorSeam(t *testing.T) {
	if got := formatIMReadFileError("📖", "notes.md", "notes.md", "boom"); got != "📖 notes.md\n```\nboom\n```" {
		t.Errorf("error with path = %q", got)
	}
	if got := formatIMReadFileError("📖", "", "", "boom"); got != "📖 Read\n```\nboom\n```" {
		t.Errorf("error without path = %q", got)
	}
}

// TestFormatIMReadFileEmptySeam pins the empty-output branch byte-for-byte.
func TestFormatIMReadFileEmptySeam(t *testing.T) {
	if got := formatIMReadFileEmpty("📖", "notes.md", "notes.md"); got != "📖 notes.md" {
		t.Errorf("empty with path = %q", got)
	}
	if got := formatIMReadFileEmpty("📖", "", ""); got != "📖 Read" {
		t.Errorf("empty without path = %q", got)
	}
}

// TestFormatIMReadDocSeam pins the document-extraction branch: the
// pages/lines summary selection and the "(summary)" placement. The i18n
// summary text is recomputed in-process; everything else is literal.
func TestFormatIMReadDocSeam(t *testing.T) {
	out := "[Extracted from pdf, 3 pages]\nline1\nline2\nline3\nline4"
	got := formatIMReadDocResult(ToolLangZhCN, "📄", "doc.pdf", "doc.pdf", firstLineOf(out), out)
	want := fmt.Sprintf("📄 doc.pdf (%s)", imDocSummary(ToolLangZhCN, 3, 4))
	if got != want {
		t.Errorf("pages+lines = %q, want %q", got, want)
	}

	// no pages and no lines: bare name, no parens (fully literal)
	out2 := "[Extracted from docx]"
	if got := formatIMReadDocResult(ToolLangZhCN, "📄", "doc.docx", "doc.docx", firstLineOf(out2), out2); got != "📄 doc.docx" {
		t.Errorf("no summary = %q, want %q", got, "📄 doc.docx")
	}

	// pages only
	out3 := "[Extracted from pdf, 7 pages]"
	got = formatIMReadDocResult(ToolLangEn, "📄", "doc.pdf", "doc.pdf", firstLineOf(out3), out3)
	want = fmt.Sprintf("📄 doc.pdf (%s)", imPagesSummary(ToolLangEn, 7))
	if got != want {
		t.Errorf("pages only = %q, want %q", got, want)
	}

	// lines only
	out4 := "[Extracted from txt]\nhello"
	got = formatIMReadDocResult(ToolLangZhCN, "📄", "doc.txt", "doc.txt", firstLineOf(out4), out4)
	want = fmt.Sprintf("📄 doc.txt (%s)", imLinesSummary(ToolLangZhCN, 1))
	if got != want {
		t.Errorf("lines only = %q, want %q", got, want)
	}
}

// TestFormatIMReadArchiveSeam pins the archive branch byte-for-byte,
// including the localized files/showing_first labels.
func TestFormatIMReadArchiveSeam(t *testing.T) {
	hdr := "[Archive: zip format, 15 files]"
	if got := formatIMReadArchiveResult(ToolLangZhCN, "📦", "bundle.zip", hdr, hdr); got != "📦 bundle.zip (15 个文件)" {
		t.Errorf("file count zh = %q", got)
	}

	out := "[Archive: tar format, 1000 files]\n[Showing first 500 of 1000 files]\ninner"
	got := formatIMReadArchiveResult(ToolLangEn, "📦", "bundle.tar", firstLineOf(out), out)
	want := "📦 bundle.tar (1000 files, showing first 500)"
	if got != want {
		t.Errorf("truncated en = %q, want %q", got, want)
	}

	out = "[Archive: tar format, 1000 files]\n[Showing first 500 of 1000 files]\ninner"
	got = formatIMReadArchiveResult(ToolLangZhCN, "📦", "bundle.tar", firstLineOf(out), out)
	want = "📦 bundle.tar (1000 个文件, 展示前 500)"
	if got != want {
		t.Errorf("truncated zh = %q, want %q", got, want)
	}

	hdr2 := "[Archive: zip]"
	if got := formatIMReadArchiveResult(ToolLangZhCN, "📦", "b.zip", hdr2, hdr2); got != "📦 b.zip" {
		t.Errorf("bare archive = %q, want %q", got, "📦 b.zip")
	}
}

// TestFormatIMReadPlainSeam pins the plain-tail branch: range hint placement
// and the no-path fallbacks. The localized range hint is recomputed
// in-process; placement strings are literal.
func TestFormatIMReadPlainSeam(t *testing.T) {
	if got := formatIMReadPlainResult(ToolLangZhCN, "📖", "notes.md", "notes.md", `{"offset":10,"limit":5}`); got != "📖 notes.md [行 10-14]" {
		t.Errorf("range hint zh = %q", got)
	}

	got := formatIMReadPlainResult(ToolLangEn, "📖", "notes.md", "notes.md", `{"limit":3}`)
	want := fmt.Sprintf("📖 notes.md %s", imFormatReadRange(ToolLangEn, `{"limit":3}`))
	if got != want {
		t.Errorf("range hint en = %q, want %q", got, want)
	}

	if got := formatIMReadPlainResult(ToolLangZhCN, "📖", "", "", `{}`); got != "📖 Read" {
		t.Errorf("no path no hint = %q, want %q", got, "📖 Read")
	}

	got = formatIMReadPlainResult(ToolLangZhCN, "📖", "", "", `{"limit":3}`)
	want = fmt.Sprintf("📖 Read %s", imFormatReadRange(ToolLangZhCN, `{"limit":3}`))
	if got != want {
		t.Errorf("no path with hint = %q, want %q", got, want)
	}

	if got := formatIMReadPlainResult(ToolLangZhCN, "📖", "notes.md", "notes.md", `{}`); got != "📖 notes.md" {
		t.Errorf("plain no hint = %q, want %q", got, "📖 notes.md")
	}
}

// TestFormatIMReadFileResultRouting pins the refactored orchestrator's
// branch selection end-to-end through the public entry point.
func TestFormatIMReadFileResultRouting(t *testing.T) {
	cases := []struct {
		name string
		tr   ToolResultInfo
		want string
	}{
		{"error wins", ToolResultInfo{Args: `{"file_path":"notes.md"}`, IsError: true, Lang: "zh-CN"},
			"📖 notes.md\n```\n\n```"},
		{"empty success", ToolResultInfo{Args: `{"file_path":"notes.md"}`, Lang: "en"},
			"📖 notes.md"},
		{"archive routed", ToolResultInfo{Args: `{"file_path":"bundle.zip"}`, Result: "[Archive: zip format, 15 files]", Lang: "zh-CN"},
			"📦 bundle.zip (15 个文件)"},
		{"doc routed", ToolResultInfo{Args: `{"file_path":"doc.docx"}`, Result: "[Extracted from docx]", Lang: "zh-CN"},
			"📄 doc.docx"},
		{"plain routed with detail fallback", ToolResultInfo{Args: `{}`, Detail: "fallback.md", Result: "hello", Lang: "zh-CN"},
			"📖 fallback.md"},
	}
	for _, tc := range cases {
		if got := formatIMReadFileResult(&tc.tr); got != tc.want {
			t.Errorf("%s: formatIMReadFileResult = %q, want %q", tc.name, got, tc.want)
		}
	}
}
