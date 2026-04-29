package extract

import (
	"testing"
)

// --- Format() methods ---

func TestArchiveExtractor_Format(t *testing.T) {
	e := archiveExtractor{subFormat: "zip"}
	if e.Format() != "zip" {
		t.Errorf("expected 'zip', got %q", e.Format())
	}
}

func TestEpubExtractor_Format(t *testing.T) {
	var e epubExtractor
	if e.Format() != "epub" {
		t.Error("expected 'epub'")
	}
}

func TestIWorkExtractor_Format(t *testing.T) {
	e := iworkExtractor{subFormat: "pages"}
	if e.Format() != "pages" {
		t.Errorf("expected 'pages', got %q", e.Format())
	}
}

func TestOdfExtractor_Format(t *testing.T) {
	e := odfExtractor{subFormat: "odt"}
	if e.Format() != "odt" {
		t.Errorf("expected 'odt', got %q", e.Format())
	}
}

func TestDocxExtractor_Format(t *testing.T) {
	var e docxExtractor
	if e.Format() != "docx" {
		t.Error("expected 'docx'")
	}
}

func TestXlsxExtractor_Format(t *testing.T) {
	var e xlsxExtractor
	if e.Format() != "xlsx" {
		t.Error("expected 'xlsx'")
	}
}

func TestPptxExtractor_Format(t *testing.T) {
	var e pptxExtractor
	if e.Format() != "pptx" {
		t.Error("expected 'pptx'")
	}
}

func TestPdfExtractor_Format(t *testing.T) {
	var e pdfExtractor
	if e.Format() != "pdf" {
		t.Error("expected 'pdf'")
	}
}

func TestRtfExtractor_Format(t *testing.T) {
	var e rtfExtractor
	if e.Format() != "rtf" {
		t.Error("expected 'rtf'")
	}
}

func TestSvgExtractor_Format(t *testing.T) {
	var e svgExtractor
	if e.Format() != "svg" {
		t.Error("expected 'svg'")
	}
}

// --- Pure utility functions ---

func TestIsArchiveExt(t *testing.T) {
	tests := []struct {
		ext  string
		want bool
	}{
		{".zip", true},
		{".tar", true},
		{".tar.gz", true},
		{".tgz", true},
		{".tar.bz2", true},
		{".tar.xz", true},
		{".txt", false},
		{"", false},
	}
	for _, tt := range tests {
		got := isArchiveExt(tt.ext)
		if got != tt.want {
			t.Errorf("isArchiveExt(%q) = %v, want %v", tt.ext, got, tt.want)
		}
	}
}

func TestIsImageExt(t *testing.T) {
	if !isImageExt(".png") {
		t.Error("expected .png to be image")
	}
	if isImageExt(".txt") {
		t.Error("expected .txt to not be image")
	}
}

func TestIsBinaryExt(t *testing.T) {
	if !isBinaryExt(".exe") {
		t.Error("expected .exe to be binary")
	}
	if isBinaryExt(".txt") {
		t.Error("expected .txt to not be binary")
	}
}

func TestFormatSize(t *testing.T) {
	tests := []struct {
		bytes    int
		expected string
	}{
		{0, "0B"},
		{1023, "1023B"},
		{1024, "1.0KB"},
		{1048576, "1.0MB"},
	}
	for _, tt := range tests {
		got := formatSize(tt.bytes)
		if got != tt.expected {
			t.Errorf("formatSize(%d) = %q, want %q", tt.bytes, got, tt.expected)
		}
	}
}

func TestStripTags(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"<p>hello</p>", "hello"},
		{"<b>bold</b> text", "bold text"},
		{"no tags", "no tags"},
		{"<div><p>nested</p></div>", "nested"},
		{"<br/>", ""},
	}
	for _, tt := range tests {
		got := stripTags(tt.input)
		if got != tt.expected {
			t.Errorf("stripTags(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestResolvePath(t *testing.T) {
	tests := []struct {
		base, rel, expected string
	}{
		{"OEBPS/", "chapter1.html", "OEBPS/chapter1.html"},
		{"", "chapter1.html", "chapter1.html"},
		{"OEBPS/", "/abs.html", "abs.html"},
	}
	for _, tt := range tests {
		got := resolvePath(tt.base, tt.rel)
		if got != tt.expected {
			t.Errorf("resolvePath(%q, %q) = %q, want %q", tt.base, tt.rel, got, tt.expected)
		}
	}
}

func TestIsLikelyText(t *testing.T) {
	if !isLikelyText([]byte("hello world")) {
		t.Error("expected text to be detected")
	}
	if isLikelyText([]byte{0x00, 0x01, 0x02, 0x03}) {
		t.Error("expected binary to not be detected as text")
	}
}
