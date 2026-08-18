package extract

// Regression test for issue #687 (regression of #683): a corrupt tar stream
// was mislabeled "Truncated: archive exceeds extraction limits" — corruption
// and size-limit truncation are different failure modes and must be
// distinguished so the agent does not chase the wrong recovery path.

import (
	"archive/tar"
	"bytes"
	"strings"
	"testing"
)

func TestIssue687_CorruptTar_MarkedCorruptNotTruncated(t *testing.T) {
	// Build a valid tar, then corrupt the checksum/header mid-stream.
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for i := 0; i < 5; i++ {
		name := strings.Repeat("f", 20) + string(rune('a'+i))
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0644, Size: 4}); err != nil {
			t.Fatal(err)
		}
		tw.Write([]byte("data"))
	}
	tw.Close()

	data := buf.Bytes()
	// Corrupt a header deep in the stream (offset into the 2nd entry's
	// header block: 512 (entry1 hdr) + 512 (entry1 data) + 16).
	if len(data) > 1040 {
		for i := 1040; i < 1056 && i < len(data); i++ {
			if data[i] != 0 {
				data[i] = 0xff
			}
		}
	}

	res, err := Extract("corrupt.tar", data)
	if err != nil {
		t.Fatalf("corrupt tar must yield a partial listing, not a hard error: %v", err)
	}
	if strings.Contains(res.Text, "exceeds extraction limits") {
		t.Fatalf("corruption must not be mislabeled as a size-limit truncation: %q", res.Text)
	}
	if !strings.Contains(res.Text, "Corrupt archive") {
		t.Fatalf("corrupt stream must be explicitly marked: %q", res.Text)
	}
}

func TestIssue687_TruncatedTar_StillShowsEntryCapMarker(t *testing.T) {
	// Entry-cap truncation (>500 entries) keeps the original marker.
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for i := 0; i < 520; i++ {
		if err := tw.WriteHeader(&tar.Header{Name: strings.Repeat("n", 30) + string(rune('a'+i%26)) + string(rune('0'+i%10)), Mode: 0644, Size: 3}); err != nil {
			t.Fatal(err)
		}
		tw.Write([]byte("abc"))
	}
	tw.Close()

	res, err := Extract("big.tar", buf.Bytes())
	if err != nil {
		t.Fatalf("big tar: %v", err)
	}
	if !strings.Contains(res.Text, "Showing first 500 of 520") {
		t.Fatalf("entry-cap truncation marker missing/wrong: %q", res.Text)
	}
	if strings.Contains(res.Text, "Corrupt archive") {
		t.Fatalf("clean big tar must not be marked corrupt: %q", res.Text)
	}
}
