package extract

import (
	"archive/tar"
	"bytes"
	"strings"
	"testing"
)

// Regression for #1539 (tar side, left open by the zip-side fix): a tar
// entry whose body read fails mid-way (truncated stream) used to
// `continue` silently - the entry vanished from the inventory with no
// marker, while the zip path kept it with "[unreadable: ...]" (#686
// honest-loss convention).
func TestTarUnreadableEntryMarked(t *testing.T) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	good := "good content"
	if err := tw.WriteHeader(&tar.Header{Name: "good.txt", Mode: 0o644, Size: int64(len(good))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(good)); err != nil {
		t.Fatal(err)
	}
	// bad.txt declares 100 bytes of body. The stream is then sliced to
	// keep only its first 10 body bytes (the remaining 90 body bytes, the
	// 412-byte block padding, and the 1024-byte zero end blocks are all
	// dropped) - a streaming reader hits EOF 90 bytes early and must
	// surface ErrUnexpectedEOF while reading bad.txt's body. Slicing off
	// only the tail (leaving the padding) would NOT work: the streaming
	// reader happily fills the declared 100 bytes from padding bytes.
	if err := tw.WriteHeader(&tar.Header{Name: "bad.txt", Mode: 0o644, Size: 100}); err != nil {
		t.Fatal(err)
	}
	afterBadBodyStart := buf.Len()
	if _, err := tw.Write(bytes.Repeat([]byte("b"), 100)); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	data := buf.Bytes()[:afterBadBodyStart+10]

	result, err := Extract("x.tar", data)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Text, "good.txt") || !strings.Contains(result.Text, good) {
		t.Fatalf("intact entry must extract normally; got %q", tailOf(result.Text, 120))
	}
	if !strings.Contains(result.Text, "bad.txt") {
		t.Fatalf("unreadable entry must stay in the inventory (name visible); got %q", tailOf(result.Text, 120))
	}
	if !strings.Contains(result.Text, "[unreadable:") {
		t.Fatalf("unreadable entry must carry a marker; got %q", tailOf(result.Text, 120))
	}
}
