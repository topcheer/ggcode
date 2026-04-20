package extract

import (
	"embed"
	"strings"
	"testing"
)

//go:embed testdata/*
var testdata embed.FS

func readTestFile(t *testing.T, name string) []byte {
	t.Helper()
	data, err := testdata.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("read testdata/%s: %v", name, err)
	}
	return data
}

func TestPDFExtraction(t *testing.T) {
	data := readTestFile(t, "sample.pdf")
	result, err := Extract("test.pdf", data)
	if err != nil {
		t.Fatalf("PDF: %v", err)
	}
	if result.Format != "pdf" {
		t.Errorf("format = %q, want pdf", result.Format)
	}
	if result.Pages != 1 {
		t.Errorf("pages = %d, want 1", result.Pages)
	}
	if !strings.Contains(result.Text, "Dummy PDF") {
		t.Errorf("expected 'Dummy PDF' in text, got: %q", result.Text)
	}
}

func TestDOCXExtraction(t *testing.T) {
	data := readTestFile(t, "sample.docx")
	result, err := Extract("test.docx", data)
	if err != nil {
		t.Fatalf("DOCX: %v", err)
	}
	if result.Format != "docx" {
		t.Errorf("format = %q, want docx", result.Format)
	}
	if !strings.Contains(result.Text, "Hello World") {
		t.Errorf("expected 'Hello World', got: %q", result.Text)
	}
	if !strings.Contains(result.Text, "test document") {
		t.Errorf("expected 'test document', got: %q", result.Text)
	}
	if !strings.Contains(result.Text, "Third paragraph") {
		t.Errorf("expected 'Third paragraph', got: %q", result.Text)
	}
}

func TestXLSXExtraction(t *testing.T) {
	data := readTestFile(t, "sample.xlsx")
	result, err := Extract("test.xlsx", data)
	if err != nil {
		t.Fatalf("XLSX: %v", err)
	}
	if result.Format != "xlsx" {
		t.Errorf("format = %q, want xlsx", result.Format)
	}
	// Should contain header row: Name, Age, City
	if !strings.Contains(result.Text, "Name") {
		t.Errorf("expected 'Name', got: %q", result.Text)
	}
	if !strings.Contains(result.Text, "Alice") {
		t.Errorf("expected 'Alice', got: %q", result.Text)
	}
	if !strings.Contains(result.Text, "Beijing") {
		t.Errorf("expected 'Beijing', got: %q", result.Text)
	}
}

func TestPPTXExtraction(t *testing.T) {
	data := readTestFile(t, "sample.pptx")
	result, err := Extract("test.pptx", data)
	if err != nil {
		t.Fatalf("PPTX: %v", err)
	}
	if result.Format != "pptx" {
		t.Errorf("format = %q, want pptx", result.Format)
	}
	if !strings.Contains(result.Text, "Slide One Title") {
		t.Errorf("expected 'Slide One Title', got: %q", result.Text)
	}
	if !strings.Contains(result.Text, "Bullet point one") {
		t.Errorf("expected 'Bullet point one', got: %q", result.Text)
	}
	if !strings.Contains(result.Text, "Slide Two Title") {
		t.Errorf("expected 'Slide Two Title', got: %q", result.Text)
	}
	if !strings.Contains(result.Text, "Content on slide two") {
		t.Errorf("expected 'Content on slide two', got: %q", result.Text)
	}
}

func TestODTExtraction(t *testing.T) {
	data := readTestFile(t, "sample.odt")
	result, err := Extract("test.odt", data)
	if err != nil {
		t.Fatalf("ODT: %v", err)
	}
	if result.Format != "odt" {
		t.Errorf("format = %q, want odt", result.Format)
	}
	if !strings.Contains(result.Text, "Hello from ODT") {
		t.Errorf("expected 'Hello from ODT', got: %q", result.Text)
	}
	if !strings.Contains(result.Text, "Second paragraph") {
		t.Errorf("expected 'Second paragraph', got: %q", result.Text)
	}
}

func TestEPUBExtraction(t *testing.T) {
	data := readTestFile(t, "sample.epub")
	result, err := Extract("test.epub", data)
	if err != nil {
		t.Fatalf("EPUB: %v", err)
	}
	if result.Format != "epub" {
		t.Errorf("format = %q, want epub", result.Format)
	}
	if result.Pages != 2 {
		t.Errorf("chapters = %d, want 2", result.Pages)
	}
	if !strings.Contains(result.Text, "quick brown fox") {
		t.Errorf("expected 'quick brown fox', got: %q", result.Text)
	}
	if !strings.Contains(result.Text, "Second chapter content") {
		t.Errorf("expected 'Second chapter content', got: %q", result.Text)
	}
}

func TestRTFExtraction(t *testing.T) {
	data := readTestFile(t, "sample.rtf")
	result, err := Extract("test.rtf", data)
	if err != nil {
		t.Fatalf("RTF: %v", err)
	}
	if result.Format != "rtf" {
		t.Errorf("format = %q, want rtf", result.Format)
	}
	if !strings.Contains(result.Text, "Hello World") {
		t.Errorf("expected 'Hello World', got: %q", result.Text)
	}
	if !strings.Contains(result.Text, "test RTF document") {
		t.Errorf("expected 'test RTF document', got: %q", result.Text)
	}
	if !strings.Contains(result.Text, "\t") {
		t.Errorf("expected tab character from \\tab, got: %q", result.Text)
	}
}

func TestIsDocumentFile(t *testing.T) {
	cases := []struct {
		path   string
		expect bool
	}{
		{"test.pdf", true},
		{"test.docx", true},
		{"test.xlsx", true},
		{"test.pptx", true},
		{"test.odt", true},
		{"test.ods", true},
		{"test.odp", true},
		{"test.epub", true},
		{"test.rtf", true},
		{"test.txt", false},
		{"test.go", false},
		{"test.png", false},
		{"README", false},
		{"file.PDF", true},  // case insensitive
		{"file.DOCX", true}, // case insensitive
	}
	for _, tc := range cases {
		got := IsDocumentFile(tc.path)
		if got != tc.expect {
			t.Errorf("IsDocumentFile(%q) = %v, want %v", tc.path, got, tc.expect)
		}
	}
}

func TestUnsupportedFormat(t *testing.T) {
	_, err := Extract("test.xyz", []byte("data"))
	if err == nil {
		t.Error("expected error for unsupported format")
	}
}

func TestRTFInvalidInput(t *testing.T) {
	_, err := Extract("test.rtf", []byte("not rtf at all"))
	if err == nil {
		t.Error("expected error for non-RTF input")
	}
}
