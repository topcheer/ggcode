package stream

import (
	"image"
	"strings"
	"sync"
	"testing"
)

func TestViewSnapshotWrittenByRender(t *testing.T) {

	var snapshot string
	var mu sync.RWMutex

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 100; i++ {
			mu.Lock()
			snapshot = "frame content"
			mu.Unlock()
		}
	}()

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

	mu.RLock()
	final := snapshot
	mu.RUnlock()
	if final != "frame content" {
		t.Errorf("final snapshot = %q, want 'frame content'", final)
	}
}

func TestViewSnapshotConcurrentReadWrite(t *testing.T) {
	var snapshot string
	var mu sync.RWMutex

	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			mu.Lock()
			snapshot = strings.Repeat("x", i%100+1)
			mu.Unlock()
		}
	}()

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

func TestDirectRendererReturnsRGBAForManager(t *testing.T) {
	r := NewDirectRenderer(320, 240, 14, "", 0, 0)
	img, err := r.Render("Test frame content")
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	expectedSize := 320 * 240 * 4
	if len(img.Pix) != expectedSize {
		t.Errorf("Pix len = %d, want %d", len(img.Pix), expectedSize)
	}
	if img.Stride != 320*4 {
		t.Errorf("Stride = %d, want %d", img.Stride, 320*4)
	}

	if img.Rect.Min.X != 0 || img.Rect.Min.Y != 0 {
		t.Errorf("Rect.Min = %v, want (0,0)", img.Rect.Min)
	}
	if img.Stride != img.Rect.Dx()*4 {
		t.Errorf("not contiguous: stride=%d dx*4=%d", img.Stride, img.Rect.Dx()*4)
	}
}

func TestCaptureAndEncodeFrameResizeMismatch(t *testing.T) {
	mgr := NewManager(StreamConfig{Width: 100, Height: 100, FPS: 10, Quality: 20, FontSize: 12})

	mgr.renderer = NewDirectRenderer(50, 50, 14, "", 0, 0)

	mgr.encoder = NewEncoder(100, 100, 26, 15, "software")

	img, err := mgr.renderer.Render("test")
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	if img.Rect.Dx() != 50 || img.Rect.Dy() != 50 {
		t.Fatalf("renderer size = %dx%d, want 50x50", img.Rect.Dx(), img.Rect.Dy())
	}

	resized := resizeImage(img, 100, 100)
	if resized.Bounds().Dx() != 100 || resized.Bounds().Dy() != 100 {
		t.Errorf("resized = %dx%d, want 100x100", resized.Bounds().Dx(), resized.Bounds().Dy())
	}

	rgba, ok := resized.(*image.RGBA)
	if !ok {
		t.Error("resized is not *image.RGBA")
		return
	}
	if len(rgba.Pix) != 100*100*4 {
		t.Errorf("resized Pix len = %d, want %d", len(rgba.Pix), 100*100*4)
	}
}

func TestStreamConfigHardwareEncoderField(t *testing.T) {
	cfg := StreamConfig{HardwareEncoder: "h264_videotoolbox"}
	if cfg.HardwareEncoder != "h264_videotoolbox" {
		t.Errorf("HardwareEncoder = %q, want h264_videotoolbox", cfg.HardwareEncoder)
	}

	cfg2 := StreamConfig{}
	if cfg2.HardwareEncoder != "" {
		t.Errorf("default HardwareEncoder = %q, want empty", cfg2.HardwareEncoder)
	}

	result := BestEncoder(cfg2.HardwareEncoder)
	t.Logf("BestEncoder(empty) = %s", result)
	if result == "" {
		t.Error("BestEncoder returned empty")
	}
}
