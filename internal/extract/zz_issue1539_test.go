package extract

import (
	"archive/tar"
	"bytes"
	"strings"
	"testing"
)

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
