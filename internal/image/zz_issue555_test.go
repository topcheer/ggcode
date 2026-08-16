package image

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// --- Bug F (#555): Decode must validate the image body, not just the header.

func makeValidPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 128, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestIssue555DecodeRejectsTruncatedPNG(t *testing.T) {
	valid := makeValidPNG(t, 64, 32)
	// Keep a valid header (PNG sig + IHDR, 33 bytes) plus a corrupt fragment
	// of the body: header parses via DecodeConfig, body must fail full decode.
	truncated := valid[:53]
	img, err := Decode(truncated)
	if err == nil {
		t.Fatalf("Decode accepted a truncated PNG (header ok, body corrupt): got %+v", img)
	}
	if !strings.Contains(err.Error(), "corrupt") && !strings.Contains(err.Error(), "truncated") {
		t.Errorf("expected corrupt/truncated error, got: %v", err)
	}
}

func TestIssue555DecodeAcceptsValidPNG(t *testing.T) {
	img, err := Decode(makeValidPNG(t, 64, 32))
	if err != nil {
		t.Fatalf("Decode rejected a valid PNG: %v", err)
	}
	if img.Width != 64 || img.Height != 32 {
		t.Errorf("dimensions = %dx%d, want 64x32", img.Width, img.Height)
	}
}

func TestIssue555DecodeRejectsCorruptPNGBody(t *testing.T) {
	valid := makeValidPNG(t, 16, 16)
	corrupt := make([]byte, len(valid))
	copy(corrupt, valid)
	// Flip bytes in the middle (IDAT payload) so the header stays valid.
	for i := len(corrupt) / 2; i < len(corrupt)/2+16 && i < len(corrupt); i++ {
		corrupt[i] ^= 0xFF
	}
	if _, err := Decode(corrupt); err == nil {
		t.Fatal("Decode accepted a PNG with a corrupted body")
	}
}

// --- Bug E (#555): FIFOs must not bypass the MaxSize precheck.

func TestIssue555ReadFileFIFORejectsOversized(t *testing.T) {
	dir := t.TempDir()
	fifoPath := filepath.Join(dir, "huge.png")
	if err := mkFifo(fifoPath); err != nil {
		t.Skipf("cannot create FIFO on this platform: %v", err)
	}

	// Writer pushes far more than MaxSize through the FIFO. The reader must
	// stop after MaxSize+1 bytes and reject, not consume it all.
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		f, err := os.OpenFile(fifoPath, os.O_WRONLY, 0)
		if err != nil {
			return
		}
		defer f.Close()
		chunk := make([]byte, 512*1024)
		deadline := time.Now().Add(10 * time.Second)
		for written := 0; written < MaxSize+2*512*1024; {
			if time.Now().After(deadline) {
				return // reader stopped consuming; that's the pass condition
			}
			n, err := f.Write(chunk)
			written += n
			if err != nil {
				return
			}
		}
	}()

	start := time.Now()
	_, err := ReadFile(fifoPath)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected too-large error for oversized FIFO")
	}
	if !strings.Contains(err.Error(), "too large") {
		t.Errorf("expected too-large error, got: %v", err)
	}
	// The core OOM regression: the read must stop promptly, not slurp
	// everything the writer produces.
	if elapsed > 8*time.Second {
		t.Errorf("ReadFile took %v to reject oversized FIFO; bounded read regressed", elapsed)
	}
	select {
	case <-writerDone:
	case <-time.After(2 * time.Second):
		// Writer still blocked writing is fine; do not wait for it.
	}
}

func TestIssue555ReadFileFIFOAcceptsSmall(t *testing.T) {
	dir := t.TempDir()
	fifoPath := filepath.Join(dir, "small.png")
	if err := mkFifo(fifoPath); err != nil {
		t.Skipf("cannot create FIFO on this platform: %v", err)
	}
	valid := makeValidPNG(t, 8, 8)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			f, err := os.OpenFile(fifoPath, os.O_WRONLY, 0)
			if err != nil {
				return
			}
			f.Write(valid)
			f.Close()
			return
		}
	}()

	img, err := ReadFile(fifoPath)
	wg.Wait()
	if err != nil {
		t.Fatalf("ReadFile rejected a small valid PNG via FIFO: %v", err)
	}
	if img.MIME != MIMEPNG || img.Width != 8 || img.Height != 8 {
		t.Errorf("image via FIFO = %+v, want 8x8 PNG", img)
	}
}

// --- Bug B/C/D (#555) abstract logic shared across platforms.

func TestIssue555DisplayScreenIndex(t *testing.T) {
	// Windows full-screen path: Display<=1 means primary, N means AllScreens[N-1].
	if idx, ok := displayScreenIndex(0); ok || idx != 0 {
		t.Errorf("displayScreenIndex(0) = %d,%v; want 0,false", idx, ok)
	}
	if idx, ok := displayScreenIndex(1); ok || idx != 0 {
		t.Errorf("displayScreenIndex(1) = %d,%v; want 0,false", idx, ok)
	}
	if idx, ok := displayScreenIndex(2); !ok || idx != 1 {
		t.Errorf("displayScreenIndex(2) = %d,%v; want 1,true", idx, ok)
	}
	if idx, ok := displayScreenIndex(3); !ok || idx != 2 {
		t.Errorf("displayScreenIndex(3) = %d,%v; want 2,true", idx, ok)
	}
}

func TestIssue555MatchWindowQueryExactFirst(t *testing.T) {
	windows := []WindowInfo{
		{ID: 10, Title: "Terminal", App: "gnome-terminal-server"},
		{ID: 20, Title: "Terminal — Drafts", App: "code"},
		{ID: 30, Title: "Report", App: "Terminal"},
	}
	// Exact title match must win over an earlier-listed window that merely
	// contains the query as a substring (#555 fuzzy-title bug D).
	id, err := matchWindowQuery(windows, "terminal")
	if err != nil {
		t.Fatal(err)
	}
	if id != 10 {
		t.Errorf("matchWindowQuery exact-title = %d, want 10", id)
	}
	// Substring still works when no exact match exists.
	if id, _ := matchWindowQuery(windows, "drafts"); id != 20 {
		t.Errorf("matchWindowQuery substring = %d, want 20", id)
	}
	// Exact app match.
	if id, _ := matchWindowQuery(windows, "report"); id != 30 {
		t.Errorf("matchWindowQuery exact-title 'report' = %d, want 30", id)
	}
	// No match errors.
	if _, err := matchWindowQuery(windows, "nope"); err == nil {
		t.Error("expected error for unmatched query")
	}
}
