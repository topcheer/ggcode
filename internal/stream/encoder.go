package stream

import (
	"bufio"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/safego"
)

const ffmpegGracefulExitTimeout = 5 * time.Second

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

	exitCh chan struct{}
	mu     sync.Mutex
}

func NewEncoder(width, height, quality, fps int, hwEncoder string) *Encoder {
	return &Encoder{
		width:     width,
		height:    height,
		quality:   quality,
		fps:       fps,
		hwEncoder: hwEncoder,
	}
}

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

	stdin, err := e.cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("stream: ffmpeg stdin pipe: %w", err)
	}
	e.stdin = stdin

	stdout, err := e.cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		return fmt.Errorf("stream: ffmpeg stdout pipe: %w", err)
	}
	e.stdout = stdout

	stderrPipe, err := e.cmd.StderrPipe()
	if err != nil {
		stdin.Close()
		stdout.Close()
		return fmt.Errorf("stream: ffmpeg stderr pipe: %w", err)
	}

	if err := e.cmd.Start(); err != nil {
		stdin.Close()
		stdout.Close()
		stderrPipe.Close()
		return fmt.Errorf("stream: ffmpeg start: %w", err)
	}

	e.running = true

	cmd := e.cmd
	exitCh := make(chan struct{})
	e.exitCh = exitCh

	stderrDone := make(chan struct{})
	safego.Go("stream.encoderMonitor", func() {
		<-stderrDone
		err := cmd.Wait()
		debug.Log("stream", "ffmpeg exited: %v", err)
		e.mu.Lock()
		e.running = false
		stdout := e.stdout
		e.stdout = nil
		e.exitCh = nil
		e.mu.Unlock()
		if stdout != nil {
			_ = stdout.Close()
		}
		close(exitCh)
	})

	safego.Go("stream.encoderStderr", func() {
		defer close(stderrDone)
		scanner := bufio.NewScanner(stderrPipe)
		scanner.Buffer(make([]byte, 0, 64*1024), 256*1024)
		for scanner.Scan() {
			debug.Log("stream", "ffmpeg stderr: %s", scanner.Text())
		}
		if err := scanner.Err(); err != nil {
			debug.Log("stream", "ffmpeg stderr read: %v", err)
		}
	})

	return nil
}

func (e *Encoder) WriteFrame(data []byte) error {
	e.mu.Lock()
	running := e.running
	stdin := e.stdin
	e.mu.Unlock()
	if !running || stdin == nil {
		return fmt.Errorf("encoder not running")
	}
	_, err := stdin.Write(data)
	return err
}

func (e *Encoder) Read(p []byte) (int, error) {
	e.mu.Lock()
	stdout := e.stdout
	e.mu.Unlock()
	if stdout == nil {
		return 0, io.EOF
	}
	n, err := stdout.Read(p)
	if err != nil {

		return n, err
	}
	return n, nil
}

func (e *Encoder) Stop() error {
	e.mu.Lock()
	ch := e.exitCh
	wasRunning := e.running
	e.running = false
	stdin := e.stdin
	e.stdin = nil
	cmd := e.cmd
	e.mu.Unlock()

	if ch == nil {
		return nil
	}
	if wasRunning && stdin != nil {
		_ = stdin.Close()
	}
	select {
	case <-ch:
	case <-time.After(ffmpegGracefulExitTimeout):
		if cmd != nil && cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		<-ch
	}
	return nil
}

func (e *Encoder) IsRunning() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.running
}

func (e *Encoder) ExpectedFrameSize() int {
	return e.width * e.height * 4
}

func (e *Encoder) buildArgs(encoderName string, isHW bool) []string {
	args := []string{
		"-hide_banner",
		"-loglevel", "error",

		"-f", "rawvideo",
		"-pixel_format", "rgba",
		"-video_size", fmt.Sprintf("%dx%d", e.width, e.height),
		"-framerate", fmt.Sprintf("%d", e.fps),
		"-i", "pipe:0",

		"-f", "lavfi",
		"-i", "anullsrc=r=44100:cl=stereo",
	}

	if isHW {

		hwArgs := []string{
			"-c:v", encoderName,
			"-b:v", "5000k",
			"-maxrate", "6000k",
			"-bufsize", "10000k",
			"-pix_fmt", "yuv420p",
			"-g", fmt.Sprintf("%d", e.fps*2),
		}

		if strings.Contains(encoderName, "videotoolbox") {
			hwArgs = append(hwArgs,
				"-profile", "high",
				"-quality", "realtime",
			)
		}
		args = append(args, hwArgs...)
	} else {

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

func (e *Encoder) selectEncoder() (name string, isHW bool) {
	if e.hwEncoder != "" {

		name := BestEncoder(e.hwEncoder)
		return name, EncoderIsHardware(name)
	}

	if hw := detectHWEncoder(); hw != "" {
		return hw, true
	}
	return "libx264", false
}

func detectHWEncoder() string {
	candidates := []string{
		"h264_videotoolbox",
		"hevc_videotoolbox",
		"h264_vaapi",
		"h264_nvenc",
		"h264_amf",
		"h264_qsv",
	}
	for _, enc := range candidates {
		if probeEncoder(enc) {
			return enc
		}
	}
	return ""
}

func probeEncoder(name string) bool {
	cmd := exec.Command("ffmpeg", "-hide_banner", "-encoders")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, name) {
			return true
		}
	}
	return false
}
