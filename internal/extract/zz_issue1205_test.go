package extract

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
)

func TestIssue1205_UnsupportedNestedFormats_Marked(t *testing.T) {
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)

	f1, _ := w.Create("readme.txt")
	f1.Write([]byte("Hello from readme"))

	f2, _ := w.Create("inner.zip")
	var innerBuf bytes.Buffer
	innerW := zip.NewWriter(&innerBuf)
	if3, _ := innerW.Create("inner.txt")
	if3.Write([]byte("nested content"))
	innerW.Close()
	f2.Write(innerBuf.Bytes())

	f3, _ := w.Create("archive.tar.xz")
	f3.Write(bytes.Repeat([]byte{0xaa, 0x55}, 100))

	f4, _ := w.Create("data.rar")
	f4.Write(bytes.Repeat([]byte{0xff, 0x00}, 100))

	f5, _ := w.Create("backup.7z")
	f5.Write(bytes.Repeat([]byte{0x12, 0x34}, 100))

	w.Close()

	res, err := Extract("outer.zip", buf.Bytes())
	if err != nil {
		t.Fatalf("archive extract: %v", err)
	}

	if !strings.Contains(res.Text, "archive.tar.xz") {
		t.Fatalf(".tar.xz entry must be listed, got %q", res.Text)
	}
	if !strings.Contains(res.Text, "[Unsupported nested archive format: .tar.xz]") {
		t.Fatalf(".tar.xz must have unsupported-format marker, got %q", res.Text)
	}

	if !strings.Contains(res.Text, "data.rar") {
		t.Fatalf(".rar entry must be listed, got %q", res.Text)
	}
	if !strings.Contains(res.Text, "[Unsupported nested archive format: .rar]") {
		t.Fatalf(".rar must have unsupported-format marker, got %q", res.Text)
	}

	if !strings.Contains(res.Text, "backup.7z") {
		t.Fatalf(".7z entry must be listed, got %q", res.Text)
	}
	if !strings.Contains(res.Text, "[Unsupported nested archive format: .7z]") {
		t.Fatalf(".7z must have unsupported-format marker, got %q", res.Text)
	}

	if !strings.Contains(res.Text, "inner.zip") {
		t.Fatalf("valid nested zip must be listed, got %q", res.Text)
	}
	if !strings.Contains(res.Text, "nested content") {
		t.Fatalf("valid nested zip content must be extracted, got %q", res.Text)
	}
}

func TestIssue1205_CorruptNestedZip_Marked(t *testing.T) {
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)

	f1, _ := w.Create("readme.txt")
	f1.Write([]byte("Hello from readme"))

	f2, _ := w.Create("corrupt.zip")

	f2.Write([]byte{0x50, 0x4b, 0x03, 0x04})
	f2.Write(bytes.Repeat([]byte{0xff, 0xff, 0xff, 0xff}, 100))

	w.Close()

	res, err := Extract("outer.zip", buf.Bytes())
	if err != nil {
		t.Fatalf("archive extract: %v", err)
	}

	if !strings.Contains(res.Text, "corrupt.zip") {
		t.Fatalf("corrupt zip entry must be listed, got %q", res.Text)
	}
	if !strings.Contains(res.Text, "[Extraction failed:") {
		t.Fatalf("corrupt zip must have extraction-failed marker, got %q", res.Text)
	}
}

func TestIssue1205_MaxDepth_Unmarked(t *testing.T) {

	var buf bytes.Buffer
	w := zip.NewWriter(&buf)

	f, _ := w.Create("level1.tar.gz")
	f.Write([]byte("fake tar.gz data"))

	w.Close()

	res, err := Extract("test.zip", buf.Bytes())
	if err != nil {
		t.Fatalf("archive extract: %v", err)
	}

	if strings.Contains(res.Text, "depth") {
		t.Fatalf("depth guard must be silent, got %q", res.Text)
	}
}
