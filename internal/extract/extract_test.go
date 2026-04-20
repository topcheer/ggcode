package extract

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
)

// --- RTF tests ---

func TestRTFExtraction(t *testing.T) {
	rtf := "{\\rtf1\\ansi\nHello \\b{world}\\par\nSecond line\\line Third\\tab tabbed\\par\n\\u233? \\u4567? unicode\n}"
	result, err := Extract("test.rtf", []byte(rtf))
	if err != nil {
		t.Fatalf("RTF extraction failed: %v", err)
	}
	if result.Format != "rtf" {
		t.Errorf("expected format rtf, got %s", result.Format)
	}
	if !strings.Contains(result.Text, "Hello") {
		t.Error("expected text to contain 'Hello'")
	}
	if !strings.Contains(result.Text, "Second line") {
		t.Error("expected text to contain 'Second line'")
	}
	if !strings.Contains(result.Text, "\t") {
		t.Error("expected text to contain tab character")
	}
}

func TestRTFInvalidInput(t *testing.T) {
	_, err := Extract("test.rtf", []byte("not rtf at all"))
	if err == nil {
		t.Error("expected error for non-RTF input")
	}
}

// --- ODF tests ---

func createODF(t *testing.T, content string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)

	// mimetype
	f, _ := w.Create("mimetype")
	f.Write([]byte("application/vnd.oasis.opendocument.text"))

	// content.xml
	f, _ = w.Create("content.xml")
	f.Write([]byte(content))

	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestODTExtraction(t *testing.T) {
	xml := `<?xml version="1.0" encoding="UTF-8"?>
<office:document-content xmlns:office="urn:oasis:names:tc:opendocument:xmlns:office:1.0"
  xmlns:text="urn:oasis:names:tc:opendocument:xmlns:text:1.0">
  <office:body>
    <office:text>
      <text:p>Hello World</text:p>
      <text:p>Second paragraph</text:p>
    </office:text>
  </office:body>
</office:document-content>`

	data := createODF(t, xml)
	result, err := Extract("test.odt", data)
	if err != nil {
		t.Fatalf("ODT extraction failed: %v", err)
	}
	if result.Format != "odt" {
		t.Errorf("expected format odt, got %s", result.Format)
	}
	if !strings.Contains(result.Text, "Hello World") {
		t.Error("expected text to contain 'Hello World'")
	}
	if !strings.Contains(result.Text, "Second paragraph") {
		t.Error("expected text to contain 'Second paragraph'")
	}
}

func TestODSExtraction(t *testing.T) {
	xml := `<?xml version="1.0" encoding="UTF-8"?>
<office:document-content xmlns:office="urn:oasis:names:tc:opendocument:xmlns:office:1.0"
  xmlns:table="urn:oasis:names:tc:opendocument:xmlns:table:1.0"
  xmlns:text="urn:oasis:names:tc:opendocument:xmlns:text:1.0">
  <office:body>
    <office:spreadsheet>
      <table:table>
        <table:table-row>
          <table:table-cell><text:p>A1</text:p></table:table-cell>
          <table:table-cell><text:p>B1</text:p></table:table-cell>
        </table:table-row>
        <table:table-row>
          <table:table-cell><text:p>A2</text:p></table:table-cell>
          <table:table-cell><text:p>B2</text:p></table:table-cell>
        </table:table-row>
      </table:table>
    </office:spreadsheet>
  </office:body>
</office:document-content>`

	data := createODF(t, xml)
	result, err := Extract("test.ods", data)
	if err != nil {
		t.Fatalf("ODS extraction failed: %v", err)
	}
	if result.Format != "ods" {
		t.Errorf("expected format ods, got %s", result.Format)
	}
	if !strings.Contains(result.Text, "A1") || !strings.Contains(result.Text, "B1") {
		t.Error("expected text to contain cell data")
	}
}

// --- EPUB tests ---

func createEPUB(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)

	// mimetype (must be first, uncompressed)
	f, _ := w.Create("mimetype")
	f.Write([]byte("application/epub+zip"))

	// META-INF/container.xml
	f, _ = w.Create("META-INF/container.xml")
	f.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles>
    <rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/>
  </rootfiles>
</container>`))

	// OPF
	f, _ = w.Create("OEBPS/content.opf")
	f.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:title>Test Book</dc:title>
  </metadata>
  <manifest>
    <item id="ch1" href="chapter1.xhtml" media-type="application/xhtml+xml"/>
    <item id="ch2" href="chapter2.xhtml" media-type="application/xhtml+xml"/>
  </manifest>
  <spine>
    <itemref idref="ch1"/>
    <itemref idref="ch2"/>
  </spine>
</package>`))

	// Chapter 1
	f, _ = w.Create("OEBPS/chapter1.xhtml")
	f.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<html xmlns="http://www.w3.org/1999/xhtml">
<head><title>Chapter 1</title></head>
<body><h1>Chapter One</h1><p>Hello from chapter 1.</p></body>
</html>`))

	// Chapter 2
	f, _ = w.Create("OEBPS/chapter2.xhtml")
	f.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<html xmlns="http://www.w3.org/1999/xhtml">
<head><title>Chapter 2</title></head>
<body><h1>Chapter Two</h1><p>Hello from chapter 2.</p></body>
</html>`))

	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestEPUBExtraction(t *testing.T) {
	data := createEPUB(t)
	result, err := Extract("test.epub", data)
	if err != nil {
		t.Fatalf("EPUB extraction failed: %v", err)
	}
	if result.Format != "epub" {
		t.Errorf("expected format epub, got %s", result.Format)
	}
	if !strings.Contains(result.Text, "Chapter One") {
		t.Error("expected text to contain 'Chapter One'")
	}
	if !strings.Contains(result.Text, "Hello from chapter 2") {
		t.Error("expected text to contain 'Hello from chapter 2'")
	}
	if result.Pages != 2 {
		t.Errorf("expected 2 chapters, got %d", result.Pages)
	}
}

// --- Registry tests ---

func TestIsDocumentFile(t *testing.T) {
	tests := []struct {
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
	}
	for _, tt := range tests {
		got := IsDocumentFile(tt.path)
		if got != tt.expect {
			t.Errorf("IsDocumentFile(%q) = %v, want %v", tt.path, got, tt.expect)
		}
	}
}

func TestUnsupportedFormat(t *testing.T) {
	_, err := Extract("test.xyz", []byte("data"))
	if err == nil {
		t.Error("expected error for unsupported format")
	}
}
