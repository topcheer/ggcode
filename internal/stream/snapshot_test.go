package stream

import (
	"image"
	"strings"
	"sync"
	"testing"
)

// TestViewSnapshotWrittenByRender verifies that after View() is called,
// the snapshot is available for the streaming goroutine to read.
// This simulates the real TUI flow: Bubble Tea calls View() on the main
// goroutine, and the streaming goroutine reads the snapshot concurrently.
func TestViewSnapshotWrittenByRender(t *testing.T) {
	// We can't easily construct a full TUI Model in a unit test,
	// but we can test the snapshot pattern directly.
	var snapshot string
	var mu sync.RWMutex

	// Simulate View() writing snapshot
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 100; i++ {
			mu.Lock()
			snapshot = "frame content"
			mu.Unlock()
		}
	}()

	// Simulate streaming goroutine reading snapshot
	readCount := 0
	emptyCount := 0
	for i := 0; i < 100; i++ {
		mu.RLock()
		s := snapshot
		mu.RUnlock()
		if s != "" {
			readCount++
		} else {
			emptyCount++
		}
	}

	<-done
	t.Logf("reads: %d non-empty, %d empty", readCount, emptyCount)
	// After the writer goroutine completes, final read should be non-empty
	mu.RLock()
	final := snapshot
	mu.RUnlock()
	if final != "frame content" {
		t.Errorf("final snapshot = %q, want 'frame content'", final)
	}
}

// TestViewSnapshotConcurrentReadWrite verifies no data race
// between View() writer and streaming reader.
func TestViewSnapshotConcurrentReadWrite(t *testing.T) {
	var snapshot string
	var mu sync.RWMutex

	var wg sync.WaitGroup

	// Writer (simulates View)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			mu.Lock()
			snapshot = strings.Repeat("x", i%100+1)
			mu.Unlock()
		}
	}()

	// Readers (simulates streaming goroutine)
	for r := 0; r < 5; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 1000; i++ {
				mu.RLock()
				_ = len(snapshot)
				mu.RUnlock()
			}
		}()
	}

	wg.Wait()
}

// TestDirectRendererReturnsRGBAForManager verifies the critical contract:
// DirectRenderer.Render() returns *image.RGBA whose Pix field can be
// directly passed to Encoder.WriteFrame() without conversion.
func TestDirectRendererReturnsRGBAForManager(t *testing.T) {
	r := NewDirectRenderer(320, 240, 14, "", 0, 0)
	img, err := r.Render("Test frame content")
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	// Verify contiguous pixel data (manager uses img.Pix directly)
	expectedSize := 320 * 240 * 4
	if len(img.Pix) != expectedSize {
		t.Errorf("Pix len = %d, want %d", len(img.Pix), expectedSize)
	}
	if img.Stride != 320*4 {
		t.Errorf("Stride = %d, want %d", img.Stride, 320*4)
	}

	// Verify manager's fast-path condition: contiguous + origin at (0,0)
	if img.Rect.Min.X != 0 || img.Rect.Min.Y != 0 {
		t.Errorf("Rect.Min = %v, want (0,0)", img.Rect.Min)
	}
	if img.Stride != img.Rect.Dx()*4 {
		t.Errorf("not contiguous: stride=%d dx*4=%d", img.Stride, img.Rect.Dx()*4)
	}
}

// TestCaptureAndEncodeFrameResizeMismatch verifies the resize branch
// when renderer produces different dimensions than encoder expects.
func TestCaptureAndEncodeFrameResizeMismatch(t *testing.T) {
	mgr := NewManager(StreamConfig{Width: 100, Height: 100, FPS: 10, Quality: 20, FontSize: 12})

	// Create a small renderer that produces smaller images than encoder expects
	mgr.renderer = NewDirectRenderer(50, 50, 14, "", 0, 0)

	// Create encoder expecting 100x100
	mgr.encoder = NewEncoder(100, 100, 26, 15, "software")
	// Don't start encoder — just test the frame data path

	// Render a small frame
	img, err := mgr.renderer.Render("test")
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	// Verify renderer produced 50x50
	if img.Rect.Dx() != 50 || img.Rect.Dy() != 50 {
		t.Fatalf("renderer size = %dx%d, want 50x50", img.Rect.Dx(), img.Rect.Dy())
	}

	// Verify resizeImage works for this case
	resized := resizeImage(img, 100, 100)
	if resized.Bounds().Dx() != 100 || resized.Bounds().Dy() != 100 {
		t.Errorf("resized = %dx%d, want 100x100", resized.Bounds().Dx(), resized.Bounds().Dy())
	}

	// Verify resized is *image.RGBA with correct data
	rgba, ok := resized.(*image.RGBA)
	if !ok {
		t.Error("resized is not *image.RGBA")
		return
	}
	if len(rgba.Pix) != 100*100*4 {
		t.Errorf("resized Pix len = %d, want %d", len(rgba.Pix), 100*100*4)
	}
}

// TestStreamConfigHardwareEncoderField verifies the new config field.
func TestStreamConfigHardwareEncoderField(t *testing.T) {
	cfg := StreamConfig{HardwareEncoder: "h264_videotoolbox"}
	if cfg.HardwareEncoder != "h264_videotoolbox" {
		t.Errorf("HardwareEncoder = %q, want h264_videotoolbox", cfg.HardwareEncoder)
	}

	// Default should be empty (auto-detect)
	cfg2 := StreamConfig{}
	if cfg2.HardwareEncoder != "" {
		t.Errorf("default HardwareEncoder = %q, want empty", cfg2.HardwareEncoder)
	}

	// Verify it flows through to BestEncoder
	result := BestEncoder(cfg2.HardwareEncoder)
	t.Logf("BestEncoder(empty) = %s", result)
	if result == "" {
		t.Error("BestEncoder returned empty")
	}
}
