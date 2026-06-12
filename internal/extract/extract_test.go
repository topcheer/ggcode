package extract

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"embed"
	"fmt"
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

func TestZIPExtraction(t *testing.T) {
	data := readTestFile(t, "sample.zip")
	result, err := Extract("test.zip", data)
	if err != nil {
		t.Fatalf("ZIP: %v", err)
	}
	if result.Format != "zip" {
		t.Errorf("format = %q, want zip", result.Format)
	}
	if !strings.Contains(result.Text, "hello from zip") {
		t.Errorf("expected 'hello from zip', got: %q", result.Text)
	}
	if !strings.Contains(result.Text, "README") {
		t.Errorf("expected 'README', got: %q", result.Text)
	}
	if !strings.Contains(result.Text, "Alice") {
		t.Errorf("expected 'Alice' from CSV, got: %q", result.Text)
	}
}

func TestTarGzExtraction(t *testing.T) {
	data := readTestFile(t, "sample.tar.gz")
	result, err := Extract("test.tar.gz", data)
	if err != nil {
		t.Fatalf("tar.gz: %v", err)
	}
	if result.Format != "tar.gz" {
		t.Errorf("format = %q, want tar.gz", result.Format)
	}
	if !strings.Contains(result.Text, "port: 8080") {
		t.Errorf("expected 'port: 8080' from yaml, got: %q", result.Text)
	}
	if !strings.Contains(result.Text, "deploying") {
		t.Errorf("expected 'deploying' from sh, got: %q", result.Text)
	}
}

func TestTarExtraction(t *testing.T) {
	data := readTestFile(t, "sample.tar")
	result, err := Extract("test.tar", data)
	if err != nil {
		t.Fatalf("tar: %v", err)
	}
	if result.Format != "tar" {
		t.Errorf("format = %q, want tar", result.Format)
	}
	if !strings.Contains(result.Text, "plain text in tar") {
		t.Errorf("expected 'plain text in tar', got: %q", result.Text)
	}
}

func TestSVGExtraction(t *testing.T) {
	data := readTestFile(t, "sample.svg")
	result, err := Extract("test.svg", data)
	if err != nil {
		t.Fatalf("SVG: %v", err)
	}
	if result.Format != "svg" {
		t.Errorf("format = %q, want svg", result.Format)
	}
	if !strings.Contains(result.Text, "Demo Chart") {
		t.Errorf("expected 'Demo Chart' from title, got: %q", result.Text)
	}
	if !strings.Contains(result.Text, "Sales: 1000") {
		t.Errorf("expected 'Sales: 1000' from text, got: %q", result.Text)
	}
	if !strings.Contains(result.Text, "Q1: 250") {
		t.Errorf("expected 'Q1: 250' from tspan, got: %q", result.Text)
	}
}

func TestPagesExtraction(t *testing.T) {
	data := readTestFile(t, "sample.pages")
	result, err := Extract("test.pages", data)
	if err != nil {
		t.Fatalf("Pages: %v", err)
	}
	if result.Format != "pages" {
		t.Errorf("format = %q, want pages", result.Format)
	}
	if !strings.Contains(result.Text, "Hello from Pages") {
		t.Errorf("expected 'Hello from Pages', got: %q", result.Text)
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
		{"test.zip", true},
		{"test.tar", true},
		{"test.tar.gz", true},
		{"test.tgz", true},
		{"test.pages", true},
		{"test.numbers", true},
		{"test.key", true},
		{"test.svg", true},
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

func TestLargeZipIsBounded(t *testing.T) {
	// Create a ZIP with 600 files to verify maxArchiveEntries=500 limit
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for i := 0; i < 600; i++ {
		f, _ := w.Create(fmt.Sprintf("file_%04d.txt", i))
		f.Write([]byte(fmt.Sprintf("content %d", i)))
	}
	w.Close()

	result, err := Extract("large.zip", buf.Bytes())
	if err != nil {
		t.Fatalf("large ZIP: %v", err)
	}
	// Should mention 500 files were read (truncated from 600)
	if !strings.Contains(result.Text, "500 files") {
		t.Errorf("expected '500 files' in output, got first 200 chars: %q", result.Text[:min(200, len(result.Text))])
	}
	if !strings.Contains(result.Text, "Showing first") {
		t.Errorf("expected 'Showing first' truncation notice, got: %q", result.Text[:min(200, len(result.Text))])
	}
	// Should NOT contain file_0500+ since we only read 500
	if strings.Contains(result.Text, "file_0500") {
		t.Error("expected file_0500 to be truncated, but it was found")
	}
}

func TestTarGzOutputIsBounded(t *testing.T) {
	// Create a tar.gz with many files, verify output doesn't exceed limits
	var tarBuf bytes.Buffer
	tw := tar.NewWriter(&tarBuf)
	for i := 0; i < 100; i++ {
		hdr := &tar.Header{
			Name: fmt.Sprintf("src/pkg_%d/main.go", i),
			Mode: 0644,
			Size: int64(len([]byte("package main"))),
		}
		tw.WriteHeader(hdr)
		tw.Write([]byte("package main"))
	}
	tw.Close()

	var gzBuf bytes.Buffer
	gw := gzip.NewWriter(&gzBuf)
	gw.Write(tarBuf.Bytes())
	gw.Close()

	result, err := Extract("test.tar.gz", gzBuf.Bytes())
	if err != nil {
		t.Fatalf("tar.gz: %v", err)
	}
	if !strings.Contains(result.Text, "package main") {
		t.Error("expected 'package main' in output")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
