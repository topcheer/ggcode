package stream

import (
	"fmt"
	"image"
	"io"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/safego"
)

// TerminalSize holds the current terminal dimensions in characters.
type TerminalSize struct {
	Cols int
	Rows int
}

// ViewFunc returns the current TUI rendering as an ANSI string.
// The manager calls this at the target FPS to capture frames.
type ViewFunc func() (content string, size TerminalSize)

// Manager orchestrates live streaming: captures TUI frames, encodes via FFmpeg,
// and fans out to multiple platform targets.
type Manager struct {
	config   StreamConfig
	renderer *DirectRenderer
	encoder  *Encoder
	targets  map[string]*Target

	viewFunc ViewFunc

	mu         sync.Mutex
	encoderMu  sync.RWMutex // protects encoder field across goroutines
	running    bool
	stopCh     chan struct{}
	encW, encH int // locked encoder resolution (set once at stream start)
}

// NewManager creates a new stream manager with the given configuration.
func NewManager(config StreamConfig) *Manager {
	config.ApplyDefaults()
	return &Manager{
		config:  config,
		targets: make(map[string]*Target),
		stopCh:  make(chan struct{}),
	}
}

// Start begins the streaming pipeline.
// viewFunc is called at the configured FPS to capture TUI frames.
func (m *Manager) Start(viewFunc ViewFunc) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.running {
		return fmt.Errorf("stream: already running")
	}

	if viewFunc == nil {
		return fmt.Errorf("stream: viewFunc is required")
	}
	m.viewFunc = viewFunc

	// Determine encoder resolution immediately from terminal orientation
	// so that EncoderSize() is available before the first frame tick.
	if _, size := viewFunc(); size.Cols > 0 && size.Rows > 0 {
		if size.Cols >= size.Rows {
			m.encW, m.encH = 1920, 1080
		} else {
			m.encW, m.encH = 1080, 1920
		}
	}

	// Initial renderer — will be resized on first frame based on terminal size.
	// Use config defaults as a starting point.
	m.renderer = NewDirectRenderer(m.config.Width, m.config.Height, m.config.FontSize, m.config.FontPath, 0, 0)

	// Prepare targets (but don't connect yet — encoder must exist first).
	// frameLoop will connect them after the first terminal size reading.
	for i := range m.config.Targets {
		tc := &m.config.Targets[i]
		if !tc.Enabled {
			continue
		}
		target := NewTarget(tc.Name, tc.FullURL())
		m.targets[tc.Name] = target
	}

	m.running = true

	debug.Log("stream", "started: %dx%d @ %dfps targets=%d",
		m.config.Width, m.config.Height, m.config.FPS, len(m.targets))

	// Start frame capture goroutine (creates encoder + connects targets on first frame)
	safego.Go("stream.frameLoop", func() { m.frameLoop() })

	return nil
}

// Stop shuts down the entire streaming pipeline.
func (m *Manager) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.running {
		return
	}

	m.running = false
	close(m.stopCh)

	// Stop all targets
	for _, t := range m.targets {
		t.Stop()
	}

	// Stop encoder
	m.encoderMu.RLock()
	enc := m.encoder
	m.encoderMu.RUnlock()
	if enc != nil {
		enc.Stop()
	}

	// stopped
}

// StopTarget stops a specific target by name.
func (m *Manager) StopTarget(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	t, ok := m.targets[name]
	if !ok {
		return fmt.Errorf("stream: target %q not found", name)
	}
	t.Stop()
	delete(m.targets, name)

	// target stopped
	return nil
}

// Status returns the status of all streaming targets.
// EncoderSize returns the actual encoder output resolution.
// Returns 0,0 if streaming hasn't started yet.
func (m *Manager) EncoderSize() (int, int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.encW, m.encH
}

func (m *Manager) FPS() int {
	return m.config.FPS
}

func (m *Manager) Status() []TargetStatus {
	m.mu.Lock()
	defer m.mu.Unlock()

	statuses := make([]TargetStatus, 0, len(m.targets))
	for _, t := range m.targets {
		statuses = append(statuses, t.Status())
	}
	return statuses
}

// IsRunning returns whether the manager is actively streaming.
func (m *Manager) IsRunning() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.running
}

// frameLoop runs in a goroutine, capturing TUI frames at the target FPS.
func (m *Manager) frameLoop() {
	interval := time.Second / time.Duration(m.config.FPS)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	lastCols, lastRows := 0, 0

	// frameLoopInit tracks whether the first frame has been processed.
	frameLoopInit := false

	frameCount := 0

	for {
		select {
		case <-m.stopCh:
			return
		case <-ticker.C:
			ansiText, termSize := m.viewFunc()
			frameCount++
			// Don't log frame ticks — extremely noisy at 30fps
			if ansiText == "" {
				continue
			}

			// Detect terminal resize (or first frame) — rebuild renderer and encoder.
			// Renderer uses natural terminal character size for correct proportions.
			// FFmpeg scale filter handles output resolution with letterboxing.
			cols, rows := termSize.Cols, termSize.Rows
			if cols <= 0 || rows <= 0 {
				cols, rows = 80, 24 // fallback
			}

			if cols != lastCols || rows != lastRows {
				// Lock encoder resolution for the entire streaming session.
				// YouTube Live locks resolution from the first frame and cannot change mid-stream.
				// Detect orientation at startup and keep it; ignore subsequent resize changes.
				m.mu.Lock()
				if m.encW == 0 {
					if cols >= rows {
						m.encW, m.encH = 1920, 1080
					} else {
						m.encW, m.encH = 1080, 1920
					}
				}
				encW, encH := m.encW, m.encH
				m.mu.Unlock()

				// Auto-calc fontSize to fit cols into encoder width.
				// DejaVu Mono: charW ≈ pt*10/16.
				// We size by width first, let rows overflow/clip like a real terminal.
				fontSize := int(float64(encW) / float64(cols) * 16.0 / 10.0)
				if fontSize < 10 {
					fontSize = 10
				}
				if fontSize > 28 {
					fontSize = 28
				}

				m.renderer = NewDirectRenderer(encW, encH, fontSize, m.config.FontPath, cols, rows)

				// Don't log terminal size on every resize

				m.encoderMu.Lock()
				if m.encoder != nil {
					m.encoder.Stop()
				}
				m.encoder = NewEncoder(encW, encH, m.config.Quality, m.config.FPS, m.config.HardwareEncoder)
				if err := m.encoder.Start(); err != nil {
					m.encoderMu.Unlock()
					debug.Log("stream", "encoder start failed: %v", err)
					return
				}
				m.encoderMu.Unlock()

				// Connect targets and start broadcaster.
				// On first frame, targets haven't connected yet.
				// On resize, old broadcaster exits when old encoder stdout closes.
				if !frameLoopInit {
					// Snapshot targets under lock to avoid concurrent map iteration race
					m.mu.Lock()
					snapshot := make([]*Target, 0, len(m.targets))
					for _, target := range m.targets {
						snapshot = append(snapshot, target)
					}
					m.mu.Unlock()
					for _, target := range snapshot {
						if _, err := target.Connect(); err != nil {
							debug.Log("stream", "target %s connect failed: %v", target.Name(), err)
						}
					}
				}
				// Single broadcaster goroutine reads encoder, broadcasts to all targets
				safego.Go("stream.fanOutBroadcaster", func() { m.fanOutBroadcaster() })
				frameLoopInit = true
				lastCols, lastRows = cols, rows
			}

			// Encode and send every frame — no dedup.
			// Live streaming requires steady frame rate for stable bitrate.
			m.captureAndEncodeFrame(ansiText)
		}
	}
}

// captureAndEncodeFrame renders an ANSI string and sends it to the encoder.
func (m *Manager) captureAndEncodeFrame(ansiText string) {
	if m.encoder == nil || !m.encoder.IsRunning() {
		return
	}

	// Render ANSI directly to RGBA — no PNG encode/decode
	img, err := m.renderer.Render(ansiText)
	if err != nil {
		debug.Log("stream", "render error: %v", err)
		return
	}
	if img == nil {
		return
	}

	// img is already *image.RGBA — get raw pixel data directly
	var frameData []byte
	if img.Stride == img.Rect.Dx()*4 && img.Rect.Min.X == 0 && img.Rect.Min.Y == 0 {
		// Contiguous pixels — use directly
		frameData = img.Pix
	} else {
		frameData = imageToRGBA(img)
	}

	// Size check: must match encoder expectations
	expected := m.encoder.ExpectedFrameSize()
	if len(frameData) != expected {
		// Resize if renderer produced different dimensions than encoder expects
		if img.Rect.Dx() != m.encoder.width || img.Rect.Dy() != m.encoder.height {
			resized := resizeImage(img, m.encoder.width, m.encoder.height)
			if rgba, ok := resized.(*image.RGBA); ok {
				frameData = rgba.Pix
			} else {
				frameData = imageToRGBA(resized)
			}
		}
		if len(frameData) != expected {
			debug.Log("stream", "frame size mismatch: got %d bytes, expected %d — skipping", len(frameData), expected)
			return
		}
	}

	// Send frame to encoder
	if err := m.encoder.WriteFrame(frameData); err != nil {
		debug.Log("stream", "encode error: %v", err)
		return
	}
}

// fanOutBroadcaster is the single reader from the encoder pipe.
// It broadcasts FLV data to all target goroutines via channels.
func (m *Manager) fanOutBroadcaster() {
	buf := make([]byte, 32*1024)
	broadcastCount := 0

	// Collect target channels under lock to prevent concurrent map write (StopTarget)
	var targets []chan []byte
	m.mu.Lock()
	for _, t := range m.targets {
		ch := make(chan []byte, 64)
		t.broadcastCh = ch
		targets = append(targets, ch)
		safego.Go("stream.targetWriter", func() { m.targetWriter(t, ch) })
	}
	m.mu.Unlock()

	defer func() {
		for _, ch := range targets {
			close(ch)
		}
	}()

	for {
		select {
		case <-m.stopCh:
			return
		default:
		}

		m.encoderMu.RLock()
		enc := m.encoder
		m.encoderMu.RUnlock()
		if enc == nil {
			debug.Log("stream", "broadcaster: encoder is nil, exiting")
			return
		}

		n, err := enc.Read(buf)
		if n > 0 {
			broadcastCount++
			// Copy data and send to each target
			data := make([]byte, n)
			copy(data, buf[:n])

			// Don't log byte counts — extremely noisy

			for _, ch := range targets {
				select {
				case ch <- data:
				default:
					// Channel full — drop frame silently
				}
			}
		}
		if err != nil {
			if err != io.EOF {
				debug.Log("stream", "broadcaster read error: %v", err)
			}
			return
		}
	}
}

// targetWriter reads from broadcast channel and writes to a single RTMP target.
func (m *Manager) targetWriter(target *Target, ch chan []byte) {
	total := 0
	for data := range ch {
		n, err := target.Write(data)
		if err != nil {
			debug.Log("stream", "target %s write error: %v", target.Name(), err)
			return
		}
		total += n
		// Don't log byte forwarding stats
	}
}

// imageToRGBA converts any image.Image to raw RGBA byte slice.
// Fast path: if the image is already *image.RGBA, copies the underlying pixel data.
func imageToRGBA(img image.Image) []byte {
	if rgba, ok := img.(*image.RGBA); ok {
		bounds := rgba.Bounds()
		w, h := bounds.Dx(), bounds.Dy()
		// If the image is contiguous (no sub-image), copy directly
		if bounds.Min.X == 0 && bounds.Min.Y == 0 && rgba.Stride == w*4 {
			out := make([]byte, len(rgba.Pix))
			copy(out, rgba.Pix)
			return out
		}
		// Stride doesn't match — copy row by row
		data := make([]byte, w*h*4)
		for y := 0; y < h; y++ {
			srcOff := (y+bounds.Min.Y)*rgba.Stride + bounds.Min.X*4
			dstOff := y * w * 4
			copy(data[dstOff:dstOff+w*4], rgba.Pix[srcOff:])
		}
		return data
	}

	// Slow path: generic image
	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	data := make([]byte, w*h*4)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			r, g, b, a := img.At(bounds.Min.X+x, bounds.Min.Y+y).RGBA()
			idx := (y*w + x) * 4
			data[idx+0] = byte(r >> 8)
			data[idx+1] = byte(g >> 8)
			data[idx+2] = byte(b >> 8)
			data[idx+3] = byte(a >> 8)
		}
	}
	return data
}

// resizeImage resizes an image to the target dimensions using nearest-neighbor.
func resizeImage(img image.Image, targetW, targetH int) image.Image {
	srcBounds := img.Bounds()
	srcW, srcH := srcBounds.Dx(), srcBounds.Dy()
	if srcW == targetW && srcH == targetH {
		return img
	}
	dst := image.NewRGBA(image.Rect(0, 0, targetW, targetH))
	for y := 0; y < targetH; y++ {
		sy := y * srcH / targetH
		for x := 0; x < targetW; x++ {
			sx := x * srcW / targetW
			dst.Set(x, y, img.At(sx+srcBounds.Min.X, sy+srcBounds.Min.Y))
		}
	}
	return dst
}
