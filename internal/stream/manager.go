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

type TerminalSize struct {
	Cols int
	Rows int
}

type ViewFunc func() (content string, size TerminalSize)

type Manager struct {
	config   StreamConfig
	renderer *DirectRenderer
	encoder  *Encoder
	targets  map[string]*Target

	viewFunc ViewFunc

	mu         sync.Mutex
	encoderMu  sync.RWMutex
	running    bool
	stopCh     chan struct{}
	encW, encH int
}

func NewManager(config StreamConfig) *Manager {
	config.ApplyDefaults()
	return &Manager{
		config:  config,
		targets: make(map[string]*Target),
		stopCh:  make(chan struct{}),
	}
}

func (m *Manager) Start(viewFunc ViewFunc) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.running {
		return fmt.Errorf("stream: already running")
	}

	m.stopCh = make(chan struct{})

	if viewFunc == nil {
		return fmt.Errorf("stream: viewFunc is required")
	}
	m.viewFunc = viewFunc

	if _, size := viewFunc(); size.Cols > 0 && size.Rows > 0 {
		if size.Cols >= size.Rows {
			m.encW, m.encH = 1920, 1080
		} else {
			m.encW, m.encH = 1080, 1920
		}
	}

	m.renderer = NewDirectRenderer(m.config.Width, m.config.Height, m.config.FontSize, m.config.FontPath, 0, 0)

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

	safego.Go("stream.frameLoop", func() { m.frameLoop() })

	return nil
}

func (m *Manager) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.running {
		return
	}

	m.running = false

	m.encoderMu.RLock()
	enc := m.encoder
	m.encoderMu.RUnlock()
	if enc != nil {
		enc.Stop()
	}

	close(m.stopCh)

	for _, t := range m.targets {
		t.Stop()
	}

}

func (m *Manager) StopTarget(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	t, ok := m.targets[name]
	if !ok {
		return fmt.Errorf("stream: target %q not found", name)
	}
	t.Stop()
	delete(m.targets, name)

	return nil
}

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

func (m *Manager) IsRunning() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.running
}

func (m *Manager) frameLoop() {
	interval := time.Second / time.Duration(m.config.FPS)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	lastCols, lastRows := 0, 0

	frameLoopInit := false

	frameCount := 0

	for {
		select {
		case <-m.stopCh:
			return
		case <-ticker.C:
			ansiText, termSize := m.viewFunc()
			frameCount++

			if ansiText == "" {
				continue
			}

			cols, rows := termSize.Cols, termSize.Rows
			if cols <= 0 || rows <= 0 {
				cols, rows = 80, 24
			}

			if cols != lastCols || rows != lastRows {

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

				fontSize := int(float64(encW) / float64(cols) * 16.0 / 10.0)
				if fontSize < 10 {
					fontSize = 10
				}
				if fontSize > 28 {
					fontSize = 28
				}

				m.renderer = NewDirectRenderer(encW, encH, fontSize, m.config.FontPath, cols, rows)

				if !frameLoopInit {

					m.encoderMu.Lock()
					m.encoder = NewEncoder(encW, encH, m.config.Quality, m.config.FPS, m.config.HardwareEncoder)
					if err := m.encoder.Start(); err != nil {
						m.encoderMu.Unlock()
						debug.Log("stream", "encoder start failed: %v", err)

						m.Stop()
						return
					}
					m.encoderMu.Unlock()

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

					safego.Go("stream.fanOutBroadcaster", func() { m.fanOutBroadcaster() })
					frameLoopInit = true

					select {
					case <-m.stopCh:
						debug.Log("stream", "stop raced first-frame init: reaping encoder/targets")
						m.encoderMu.RLock()
						enc := m.encoder
						m.encoderMu.RUnlock()
						if enc != nil {
							enc.Stop()
						}
						for _, target := range snapshot {
							target.Stop()
						}
						return
					default:
					}
				}

				lastCols, lastRows = cols, rows
			}

			m.captureAndEncodeFrame(ansiText)
		}
	}
}

func (m *Manager) captureAndEncodeFrame(ansiText string) {
	if m.encoder == nil || !m.encoder.IsRunning() {
		return
	}

	img, err := m.renderer.Render(ansiText)
	if err != nil {
		debug.Log("stream", "render error: %v", err)
		return
	}
	if img == nil {
		return
	}

	var frameData []byte
	if img.Stride == img.Rect.Dx()*4 && img.Rect.Min.X == 0 && img.Rect.Min.Y == 0 {

		frameData = img.Pix
	} else {
		frameData = imageToRGBA(img)
	}

	expected := m.encoder.ExpectedFrameSize()
	if len(frameData) != expected {

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

	if err := m.encoder.WriteFrame(frameData); err != nil {
		debug.Log("stream", "encode error: %v", err)
		return
	}
}

func (m *Manager) fanOutBroadcaster() {
	buf := make([]byte, 32*1024)
	broadcastCount := 0

	var targets []chan []byte
	var targetPtrs []*Target
	m.mu.Lock()
	for _, t := range m.targets {
		ch := make(chan []byte, 64)
		t.broadcastCh = ch
		targets = append(targets, ch)
		targetPtrs = append(targetPtrs, t)
		safego.Go("stream.targetWriter", func() { m.targetWriter(t, ch) })
	}
	m.mu.Unlock()

	defer func() {
		for _, ch := range targets {
			close(ch)
		}
	}()

	stopAllTargets := func() {
		for _, t := range targetPtrs {
			t.Stop()
		}
	}

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
			stopAllTargets()
			return
		}

		n, err := enc.Read(buf)
		if n > 0 {
			broadcastCount++

			data := make([]byte, n)
			copy(data, buf[:n])

			for _, ch := range targets {
				select {
				case ch <- data:
				default:

				}
			}
		}
		if err != nil {
			if err != io.EOF {
				debug.Log("stream", "broadcaster read error: %v", err)
			}

			stopAllTargets()
			return
		}
	}
}

func (m *Manager) targetWriter(target *Target, ch chan []byte) {
	total := 0
	for data := range ch {
		n, err := target.Write(data)
		if err != nil {
			debug.Log("stream", "target %s write error: %v", target.Name(), err)

			target.Stop()
			return
		}
		total += n

	}
}

func imageToRGBA(img image.Image) []byte {
	if rgba, ok := img.(*image.RGBA); ok {
		bounds := rgba.Bounds()
		w, h := bounds.Dx(), bounds.Dy()

		if bounds.Min.X == 0 && bounds.Min.Y == 0 && rgba.Stride == w*4 {
			out := make([]byte, len(rgba.Pix))
			copy(out, rgba.Pix)
			return out
		}

		data := make([]byte, w*h*4)
		for y := 0; y < h; y++ {
			srcOff := (y+bounds.Min.Y)*rgba.Stride + bounds.Min.X*4
			dstOff := y * w * 4
			copy(data[dstOff:dstOff+w*4], rgba.Pix[srcOff:])
		}
		return data
	}

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
