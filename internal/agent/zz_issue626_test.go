package agent

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestIssue626_PartialReadDoesNotSeedMtimeBaseline(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "big.go")

	content := make([]byte, redundantReadMinSize+2048)
	for i := range content {
		content[i] = byte('a' + i%26)
	}
	if err := os.WriteFile(f, content, 0o644); err != nil {
		t.Fatal(err)
	}

	time.Sleep(2 * time.Millisecond)

	r := newRedundantReadState()

	if got := r.checkRedundantRead(f, true); got != "" {
		t.Fatalf("partial read should never warn, got %q", got)
	}

	if got := r.checkRedundantRead(f, false); got != "" {
		t.Fatalf("full read after partial read mis-flagged as redundant (#626): %q", got)
	}
}

func TestIssue626_SecondFullReadStillWarns(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "big2.go")
	content := make([]byte, redundantReadMinSize+2048)
	if err := os.WriteFile(f, content, 0o644); err != nil {
		t.Fatal(err)
	}

	r := newRedundantReadState()
	if got := r.checkRedundantRead(f, false); got != "" {
		t.Fatalf("first full read should not warn, got %q", got)
	}
	if got := r.checkRedundantRead(f, false); got == "" {
		t.Fatal("second full read without changes should warn (regression of core behavior)")
	}
}

func TestIssue626_RepeatedPartialReadsNeverWarn(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "win.go")
	if err := os.WriteFile(f, make([]byte, redundantReadMinSize*4), 0o644); err != nil {
		t.Fatal(err)
	}
	r := newRedundantReadState()
	for i := 0; i < 3; i++ {
		if got := r.checkRedundantRead(f, true); got != "" {
			t.Fatalf("partial read %d warned: %q", i, got)
		}
	}
}
