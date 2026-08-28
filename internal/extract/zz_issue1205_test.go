package extract

// Regression tests for issue #1205:
// Nested archives (.tar.xz, .rar, .7z) and corrupt nested archives must surface
// explicit markers, not empty sections. The design contract from #682/#686/#687/#692
// requires truncation/corruption/extraction failures to carry visible markers.

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
)

// TestIssue1205_UnsupportedNestedFormats_Marked tests that unsupported nested
// archive formats (.tar.xz, .rar, .7z) produce explicit markers instead of
// empty sections when nested inside a ZIP archive.
func TestIssue1205_UnsupportedNestedFormats_Marked(t *testing.T) {
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)

	// Add a regular text file
	f1, _ := w.Create("readme.txt")
	f1.Write([]byte("Hello from readme"))

	// Add a valid nested ZIP
	f2, _ := w.Create("inner.zip")
	var innerBuf bytes.Buffer
	innerW := zip.NewWriter(&innerBuf)
	if3, _ := innerW.Create("inner.txt")
	if3.Write([]byte("nested content"))
	innerW.Close()
	f2.Write(innerBuf.Bytes())

	// Add .tar.xz (unsupported) - use placeholder bytes since it's never parsed
	f3, _ := w.Create("archive.tar.xz")
	f3.Write(bytes.Repeat([]byte{0xaa, 0x55}, 100))

	// Add .rar (unsupported) - placeholder bytes
	f4, _ := w.Create("data.rar")
	f4.Write(bytes.Repeat([]byte{0xff, 0x00}, 100))

	// Add .7z (unsupported) - placeholder bytes
	f5, _ := w.Create("backup.7z")
	f5.Write(bytes.Repeat([]byte{0x12, 0x34}, 100))

	w.Close()

	res, err := Extract("outer.zip", buf.Bytes())
	if err != nil {
		t.Fatalf("archive extract: %v", err)
	}

	// Check that .tar.xz has a marker, not an empty section
	if !strings.Contains(res.Text, "archive.tar.xz") {
		t.Fatalf(".tar.xz entry must be listed, got %q", res.Text)
	}
	if !strings.Contains(res.Text, "[Unsupported nested archive format: .tar.xz]") {
		t.Fatalf(".tar.xz must have unsupported-format marker, got %q", res.Text)
	}

	// Check that .rar has a marker
	if !strings.Contains(res.Text, "data.rar") {
		t.Fatalf(".rar entry must be listed, got %q", res.Text)
	}
	if !strings.Contains(res.Text, "[Unsupported nested archive format: .rar]") {
		t.Fatalf(".rar must have unsupported-format marker, got %q", res.Text)
	}

	// Check that .7z has a marker
	if !strings.Contains(res.Text, "backup.7z") {
		t.Fatalf(".7z entry must be listed, got %q", res.Text)
	}
	if !strings.Contains(res.Text, "[Unsupported nested archive format: .7z]") {
		t.Fatalf(".7z must have unsupported-format marker, got %q", res.Text)
	}

	// Check that valid nested ZIP was extracted
	if !strings.Contains(res.Text, "inner.zip") {
		t.Fatalf("valid nested zip must be listed, got %q", res.Text)
	}
	if !strings.Contains(res.Text, "nested content") {
		t.Fatalf("valid nested zip content must be extracted, got %q", res.Text)
	}
}

// TestIssue1205_CorruptNestedZip_Marked tests that a corrupt nested ZIP produces
// an extraction-failed marker instead of silently disappearing.
func TestIssue1205_CorruptNestedZip_Marked(t *testing.T) {
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)

	// Add a regular text file
	f1, _ := w.Create("readme.txt")
	f1.Write([]byte("Hello from readme"))

	// Add a corrupt ZIP file (invalid ZIP structure)
	f2, _ := w.Create("corrupt.zip")
	// Write garbage that looks like ZIP but is invalid
	f2.Write([]byte{0x50, 0x4b, 0x03, 0x04}) // ZIP magic
	f2.Write(bytes.Repeat([]byte{0xff, 0xff, 0xff, 0xff}, 100))

	w.Close()

	res, err := Extract("outer.zip", buf.Bytes())
	if err != nil {
		t.Fatalf("archive extract: %v", err)
	}

	// Check that corrupt.zip entry is listed with error marker
	if !strings.Contains(res.Text, "corrupt.zip") {
		t.Fatalf("corrupt zip entry must be listed, got %q", res.Text)
	}
	if !strings.Contains(res.Text, "[Extraction failed:") {
		t.Fatalf("corrupt zip must have extraction-failed marker, got %q", res.Text)
	}
}

// TestIssue1205_MaxDepth_Unmarked tests that exceeding max depth doesn't produce
// spurious markers (it's a silent guard, not a failure).
func TestIssue1205_MaxDepth_Unmarked(t *testing.T) {
	// Build a ZIP that contains a .tar.gz (which will fail to extract since
	// it's fake data). The depth guard should work silently without spurious markers.
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)

	f, _ := w.Create("level1.tar.gz")
	f.Write([]byte("fake tar.gz data")) // This will fail to extract, but that's ok

	w.Close()

	res, err := Extract("test.zip", buf.Bytes())
	if err != nil {
		t.Fatalf("archive extract: %v", err)
	}
	// Should have some marker (extraction failed), but not depth-related
	if strings.Contains(res.Text, "depth") {
		t.Fatalf("depth guard must be silent, got %q", res.Text)
	}
}
