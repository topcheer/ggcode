package extract

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"strings"
	"testing"
)

func buildOversizedTarGz(t *testing.T, totalBytes int64) []byte {
	t.Helper()
	var compressed bytes.Buffer
	gzw := gzip.NewWriter(&compressed)
	tw := tar.NewWriter(gzw)
	const nEntries = 3
	entrySize := (totalBytes + nEntries - 1) / nEntries
	chunk := make([]byte, 1024*1024)
	for entry := 0; entry < nEntries; entry++ {
		if err := tw.WriteHeader(&tar.Header{
			Name: "blob" + strings.Repeat("z", 20) + string(rune('a'+entry%26)),
			Mode: 0644, Size: entrySize,
		}); err != nil {
			t.Fatal(err)
		}
		var remaining = entrySize
		for remaining > 0 {
			n := remaining
			if n > int64(len(chunk)) {
				n = int64(len(chunk))
			}
			if _, err := tw.Write(chunk[:n]); err != nil {
				t.Fatal(err)
			}
			remaining -= n
		}
	}
	tw.Close()
	gzw.Close()
	return compressed.Bytes()
}

func TestIssue692_SizeLimitHit_NotLabeledCorrupt(t *testing.T) {

	if testing.Short() {
		t.Skip("streams 205MB through gzip")
	}
	data := buildOversizedTarGz(t, maxTarDecompressSize+5*1024*1024)

	res, err := Extract("big.tar.gz", data)
	if err != nil {
		t.Fatalf("healthy oversized tar.gz must yield a partial listing, not an error: %v", err)
	}
	if strings.Contains(res.Text, "Corrupt archive") {
		t.Fatalf("size-limit hit must not be mislabeled as corruption (#692): %q", firstLine(res.Text, 1))
	}
	if !strings.Contains(res.Text, "Truncated: archive exceeds extraction limits") &&
		!strings.Contains(res.Text, "[Showing first ") {
		t.Fatalf("size-limit hit must surface a truncation marker: %q", firstLine(res.Text, 1))
	}
}

func TestIssue692_CorruptGz_StillLabeledCorrupt(t *testing.T) {

	data := buildOversizedTarGz(t, 64*1024)

	mid := len(data) / 2
	for i := mid; i < mid+64 && i < len(data); i++ {
		data[i] ^= 0xff
	}

	res, err := Extract("broken.tar.gz", data)
	if err != nil {
		t.Fatalf("corrupt tar.gz must yield a partial listing, not an error: %v", err)
	}
	if strings.Contains(res.Text, "exceeds extraction limits") {
		t.Fatalf("corruption must not be mislabeled as a size limit: %q", firstLine(res.Text, 1))
	}
}

func TestIssue692_ListTarFromReader_LimitHitAttributesTruncated(t *testing.T) {

	if testing.Short() {
		t.Skip("streams 200MB through the reader")
	}
	data := buildOversizedTarGz(t, maxTarDecompressSize+1024*1024)

	_, total, truncated, corrupt, err := listTarGz(data)
	if err != nil {
		t.Fatalf("listTarGz: %v", err)
	}
	if corrupt {
		t.Fatal("healthy oversized stream must not be marked corrupt")
	}
	if !truncated {
		t.Fatalf("size-limit hit must set truncated (total=%d)", total)
	}
}

func TestIssue692_PlainTar_NoCapStillWorks(t *testing.T) {

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for i := 0; i < 3; i++ {
		if err := tw.WriteHeader(&tar.Header{Name: "f" + string(rune('a'+i)) + ".txt", Mode: 0644, Size: 5}); err != nil {
			t.Fatal(err)
		}
		tw.Write([]byte("hello"))
	}
	tw.Close()

	files, total, truncated, corrupt, err := listTar(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if truncated || corrupt {
		t.Fatalf("clean small tar must be unmarked: truncated=%v corrupt=%v", truncated, corrupt)
	}
	if len(files) != 3 || total != 3 {
		t.Fatalf("expected 3 files / total 3, got %d/%d", len(files), total)
	}
}
