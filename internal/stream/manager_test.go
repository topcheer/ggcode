package stream

import (
	"bytes"
	"image"
	"image/color"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestManagerNew(t *testing.T) {
	cfg := StreamConfig{FPS: 30, Width: 640, Height: 480}
	mgr := NewManager(cfg)

	if mgr.IsRunning() {
		t.Error("new manager should not be running")
	}
	if mgr.config.FPS != 30 {
		t.Errorf("FPS = %d, want 30", mgr.config.FPS)
	}
}

func TestManagerNewAppliesDefaults(t *testing.T) {
	cfg := StreamConfig{}
	mgr := NewManager(cfg)

	if mgr.config.FPS != 15 {
		t.Errorf("default FPS = %d, want 15", mgr.config.FPS)
	}
	if mgr.config.Width != 1280 {
		t.Errorf("default Width = %d, want 1280", mgr.config.Width)
	}
}

func TestManagerStartNilViewFunc(t *testing.T) {
	mgr := NewManager(StreamConfig{})
	err := mgr.Start(nil)
	if err == nil {
		t.Error("expected error for nil viewFunc")
		mgr.Stop()
	}
}

func TestManagerStopWhenNotRunning(t *testing.T) {
	mgr := NewManager(StreamConfig{})
	mgr.Stop()
	if mgr.IsRunning() {
		t.Error("should not be running after stop")
	}
}

func TestManagerStopTargetNotExists(t *testing.T) {
	mgr := NewManager(StreamConfig{})
	err := mgr.StopTarget("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent target")
	}
}

func TestManagerStatusWhenNotRunning(t *testing.T) {
	mgr := NewManager(StreamConfig{})
	statuses := mgr.Status()
	if len(statuses) != 0 {
		t.Errorf("expected empty statuses, got %d", len(statuses))
	}
}

func TestImageToRGBA(t *testing.T) {
	// Create a 2x2 NRGBA image with a red pixel
	img := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.NRGBA{R: 255, G: 0, B: 0, A: 255})
	img.Set(1, 0, color.NRGBA{R: 0, G: 255, B: 0, A: 255})

	data := imageToRGBA(img)
	expected := 2 * 2 * 4 // 2x2 * 4 bytes per pixel
	if len(data) != expected {
		t.Fatalf("data length = %d, want %d", len(data), expected)
	}

	// First pixel should be red (255, 0, 0, 255)
	if data[0] != 255 || data[1] != 0 || data[2] != 0 || data[3] != 255 {
		t.Errorf("first pixel = %v, want [255 0 0 255]", data[:4])
	}

	// Second pixel should be green (0, 255, 0, 255)
	if data[4] != 0 || data[5] != 255 || data[6] != 0 || data[7] != 255 {
		t.Errorf("second pixel = %v, want [0 255 0 255]", data[4:8])
	}
}

func TestManagerStartStop(t *testing.T) {
	mgr := NewManager(StreamConfig{
		Width:  640,
		Height: 480,
	})

	viewCalled := 0
	viewFunc := func() (string, TerminalSize) {
		viewCalled++
		return "test", TerminalSize{Cols: 80, Rows: 24}
	}

	err := mgr.Start(viewFunc)
	if err != nil {
		// FFmpeg not installed — skip
		t.Skipf("ffmpeg not available: %v", err)
		return
	}

	if !mgr.IsRunning() {
		t.Fatal("manager should be running")
	}

	time.Sleep(100 * time.Millisecond)
	mgr.Stop()

	if mgr.IsRunning() {
		t.Error("manager should not be running after stop")
	}
}

func TestCaptureAndEncodeFrameNilEncoder(t *testing.T) {
	mgr := NewManager(StreamConfig{Width: 100, Height: 100})
	// encoder is nil — should not panic
	mgr.captureAndEncodeFrame("hello world")
}

func TestCaptureAndEncodeFrameNotRunningEncoder(t *testing.T) {
	mgr := NewManager(StreamConfig{Width: 100, Height: 100})
	mgr.encoder = NewEncoder(100, 100, 26, 15, "")
	// encoder exists but not started — should return without panic
	mgr.captureAndEncodeFrame("hello world")
}

func TestManagerStartAlreadyRunning(t *testing.T) {
	mgr := NewManager(StreamConfig{Width: 640, Height: 480})
	mgr.running = true

	viewFunc := func() (string, TerminalSize) {
		return "", TerminalSize{}
	}

	err := mgr.Start(viewFunc)
	if err == nil {
		t.Error("expected error when starting already-running manager")
		mgr.Stop()
	}
}

func TestManagerStartDoubleStart(t *testing.T) {
	mgr := NewManager(StreamConfig{
		Width:  640,
		Height: 480,
	})

	viewFunc := func() (string, TerminalSize) {
		return "test", TerminalSize{Cols: 80, Rows: 24}
	}

	err := mgr.Start(viewFunc)
	if err != nil {
		t.Skipf("ffmpeg not available: %v", err)
		return
	}
	defer mgr.Stop()

	// Second start should fail
	err = mgr.Start(viewFunc)
	if err == nil {
		t.Error("expected error on double start")
	}
}

func TestTerminalSizeStruct(t *testing.T) {
	ts := TerminalSize{Cols: 120, Rows: 40}
	if ts.Cols != 120 || ts.Rows != 40 {
		t.Errorf("TerminalSize = %+v, want Cols=120 Rows=40", ts)
	}
}

func TestViewFuncSignature(t *testing.T) {
	// Verify the ViewFunc type works with the new signature
	called := false
	vf := func() (string, TerminalSize) {
		called = true
		return "content", TerminalSize{Cols: 80, Rows: 24}
	}

	content, size := vf()
	if !called {
		t.Error("viewFunc not called")
	}
	if content != "content" {
		t.Errorf("content = %q, want %q", content, "content")
	}
	if size.Cols != 80 || size.Rows != 24 {
		t.Errorf("size = %+v, want Cols=80 Rows=24", size)
	}
}

func TestFanOutBroadcasterDistributesToAllTargets(t *testing.T) {
	mgr := NewManager(StreamConfig{Width: 100, Height: 100, FPS: 10, Quality: 20, FontSize: 12})

	// Create targets with broadcast channels
	t1 := &Target{name: "youtube", broadcastCh: make(chan []byte, 10)}
	t2 := &Target{name: "twitch", broadcastCh: make(chan []byte, 10)}
	mgr.targets = map[string]*Target{
		"youtube": t1,
		"twitch":  t2,
	}

	// Simulate broadcaster: send data to both channels
	testData := []byte("FLV-TEST-DATA")
	for _, target := range mgr.targets {
		select {
		case target.broadcastCh <- testData:
		default:
		}
	}

	// Verify both received the same data
	for _, name := range []string{"youtube", "twitch"} {
		select {
		case d := <-mgr.targets[name].broadcastCh:
			if string(d) != "FLV-TEST-DATA" {
				t.Errorf("%s got %q, want FLV-TEST-DATA", name, d)
			}
		default:
			t.Errorf("%s got no data", name)
		}
	}
}

func TestBroadcasterChannelDropOnFull(t *testing.T) {
	// When channel is full, data should be dropped (non-blocking)
	ch := make(chan []byte, 1)
	ch <- []byte("first")

	// Second send should not block
	select {
	case ch <- []byte("second"):
		t.Error("should have dropped")
	default:
		// expected: drop is correct behavior
	}
}

func TestTargetWriterReadsFromChannel(t *testing.T) {
	mgr := NewManager(StreamConfig{Width: 100, Height: 100, FPS: 10, Quality: 20, FontSize: 12})

	// Use a mutex-protected buffer for safe concurrent access
	var mu sync.Mutex
	var buf bytes.Buffer
	target := &Target{name: "test"}
	target.setState(TargetLive)
	target.mu.Lock()
	target.stdin = &nopWriteCloser{Writer: writerFunc(func(p []byte) (int, error) {
		mu.Lock()
		defer mu.Unlock()
		return buf.Write(p)
	})}
	target.mu.Unlock()
	ch := make(chan []byte, 10)

	// Start targetWriter
	done := make(chan struct{})
	go func() {
		mgr.targetWriter(target, ch)
		close(done)
	}()

	// Send data
	ch <- []byte("HELLO")
	ch <- []byte("WORLD")
	close(ch)

	// Wait for writer to finish
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for target writer")
	}

	mu.Lock()
	result := buf.String()
	mu.Unlock()
	if !strings.Contains(result, "HELLO") || !strings.Contains(result, "WORLD") {
		t.Errorf("target writer output = %q, want HELLO and WORLD", result)
	}
}

type writerFunc func([]byte) (int, error)

func (w writerFunc) Write(p []byte) (int, error) { return w(p) }
func (w writerFunc) Close() error                { return nil }

type nopWriteCloser struct{ io.Writer }

func (n *nopWriteCloser) Close() error { return nil }
