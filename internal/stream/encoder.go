package stream

import (
	"bufio"
	"fmt"
	"io"
	"os/exec"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
)

// Encoder wraps an FFmpeg subprocess that encodes raw RGBA frames into FLV.
type Encoder struct {
	width     int
	height    int
	quality   int
	fps       int
	hwEncoder string

	cmd     *exec.Cmd
	stdin   io.WriteCloser
	stdout  io.ReadCloser
	running bool
	mu      sync.Mutex
}

// NewEncoder creates a new encoder for the given resolution and quality.
func NewEncoder(width, height, quality, fps int, hwEncoder string) *Encoder {
	return &Encoder{
		width:     width,
		height:    height,
		quality:   quality,
		fps:       fps,
		hwEncoder: hwEncoder,
	}
}

// Start launches the FFmpeg encoder subprocess.
func (e *Encoder) Start() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		return fmt.Errorf("stream: ffmpeg not found: %w", err)
	}

	encoderName, isHW := e.selectEncoder()
	args := e.buildArgs(encoderName, isHW)

	debug.Log("stream", "encoder starting: %s %v", ffmpegPath, args)

	e.cmd = exec.Command(ffmpegPath, args...)

	// Pipes for stdin (raw frames in) and stdout (FLV out)
	stdin, err := e.cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("stream: ffmpeg stdin pipe: %w", err)
	}
	e.stdin = stdin

	stdout, err := e.cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stream: ffmpeg stdout pipe: %w", err)
	}
	e.stdout = stdout

	// Capture stderr via pipe for diagnostics
	stderrPipe, err := e.cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("stream: ffmpeg stderr pipe: %w", err)
	}

	if err := e.cmd.Start(); err != nil {
		return fmt.Errorf("stream: ffmpeg start: %w", err)
	}

	e.running = true

	// Monitor stderr in background — log any errors
	go func() {
		scanner := bufio.NewScanner(stderrPipe)
		for scanner.Scan() {
			debug.Log("stream", "ffmpeg stderr: %s", scanner.Text())
		}
	}()

	return nil
}

// WriteFrame writes a single raw RGBA frame to the encoder.
func (e *Encoder) WriteFrame(data []byte) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.running || e.stdin == nil {
		return fmt.Errorf("encoder not running")
	}
	_, err := e.stdin.Write(data)
	return err
}

// Read reads encoded FLV data from the encoder stdout.
// Implements io.Reader for fan-out consumers.
func (e *Encoder) Read(p []byte) (int, error) {
	return e.stdout.Read(p)
}

// Stop signals the encoder to finish and waits for the process to exit.
func (e *Encoder) Stop() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.running {
		return nil
	}
	e.running = false

	// Close stdin to signal EOF to ffmpeg
	if e.stdin != nil {
		e.stdin.Close()
	}

	// Close stdout to unblock any readers
	if e.stdout != nil {
		e.stdout.Close()
	}

	// Kill the process if still running
	if e.cmd != nil && e.cmd.Process != nil {
		e.cmd.Process.Kill()
		e.cmd.Wait()
	}

	return nil
}

// IsRunning returns whether the encoder subprocess is active.
func (e *Encoder) IsRunning() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.running
}

// ExpectedFrameSize returns the expected raw RGBA frame size in bytes.
func (e *Encoder) ExpectedFrameSize() int {
	return e.width * e.height * 4
}

// buildArgs constructs FFmpeg args based on the selected encoder.
func (e *Encoder) buildArgs(encoderName string, isHW bool) []string {
	args := []string{
		"-hide_banner",
		"-loglevel", "error",
		// Video input: raw RGBA from stdin
		"-f", "rawvideo",
		"-pixel_format", "rgba",
		"-video_size", fmt.Sprintf("%dx%d", e.width, e.height),
		"-framerate", fmt.Sprintf("%d", e.fps),
		"-i", "pipe:0",
		// Audio input: silent (platforms require audio track)
		"-f", "lavfi",
		"-i", "anullsrc=r=44100:cl=stereo",
	}

	if isHW {
		// Hardware encoder args
		args = append(args,
			"-c:v", encoderName,
			"-b:v", "5000k",
			"-maxrate", "6000k",
			"-bufsize", "10000k",
			"-pix_fmt", "yuv420p",
			"-g", fmt.Sprintf("%d", e.fps*2),
		)
	} else {
		// Software encoder (libx264) — CBR for stable live bitrate.
		// CRF produces very low bitrate on static terminal screens.
		args = append(args,
			"-c:v", encoderName,
			"-preset", "fast",
			"-tune", "stillimage",
			"-x264-params", "nal-hrd=cbr:force-cfr=1",
			"-b:v", "5000k",
			"-maxrate", "5000k",
			"-bufsize", "5000k",
			"-pix_fmt", "yuv420p",
			"-g", fmt.Sprintf("%d", e.fps*2),
			"-keyint_min", fmt.Sprintf("%d", e.fps),
		)
	}

	// Audio + output
	args = append(args,
		"-c:a", "aac",
		"-b:a", "128k",
		"-ar", "44100",
		"-f", "flv",
		"-flvflags", "no_duration_filesize",
		"pipe:1",
	)

	return args
}

// selectEncoder picks the best available encoder.
func (e *Encoder) selectEncoder() (name string, isHW bool) {
	if e.hwEncoder != "" {
		return e.hwEncoder, true
	}
	return "libx264", false
}
