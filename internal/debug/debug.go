package debug

import (
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
)

const (
	defaultLogPath      = "/tmp/ggcode-debug.log"
	defaultLogDir       = "/tmp/ggcode-debug"
	defaultMaxLogSize   = 50 * 1024 * 1024
	defaultMaxLogFiles  = 3
	defaultAsyncBufSize = 1024
)

var (
	logger  *log.Logger
	logSink *asyncFileSink
	once    sync.Once
	mu      sync.RWMutex

	logPath      = defaultLogPath
	maxLogSize   int64 = defaultMaxLogSize
	maxLogFiles        = defaultMaxLogFiles
	asyncBufSize       = defaultAsyncBufSize
)

// Init opens the debug log file. Safe to call multiple times.
func Init() {
	once.Do(func() {
		basePath, compatPath := resolveLogPaths(logPath, os.Getpid())
		sink, err := newAsyncFileSink(basePath, compatPath, maxLogSize, maxLogFiles, asyncBufSize)
		if err != nil {
			return
		}
		mu.Lock()
		logSink = sink
		logger = log.New(sink, "", log.Lmicroseconds)
		mu.Unlock()
	})
}

// Log writes a formatted message with a package tag.
// Format: [HH:MM:SS.mmm] [pkg] message
func Log(pkg, format string, args ...interface{}) {
	mu.RLock()
	l := logger
	mu.RUnlock()
	if l == nil {
		return
	}
	l.Printf("[%s] "+format, append([]interface{}{pkg}, args...)...)
}

// Logf writes a raw formatted message (no package tag).
func Logf(format string, args ...interface{}) {
	mu.RLock()
	l := logger
	mu.RUnlock()
	if l == nil {
		return
	}
	l.Printf(format, args...)
}

// Close flushes, closes, and removes all debug log files for the current process.
func Close() {
	mu.Lock()
	sink := logSink
	logSink = nil
	logger = nil
	mu.Unlock()

	if sink != nil {
		sink.Close()
	}
}

type asyncFileSink struct {
	basePath   string
	compatPath string
	maxSize    int64
	maxFiles   int

	ch      chan []byte
	dropped atomic.Uint64

	sendMu    sync.RWMutex
	closeOnce sync.Once
	wg        sync.WaitGroup
	closed    bool

	file *os.File
	size int64
}

func newAsyncFileSink(basePath, compatPath string, maxSize int64, maxFiles, buffer int) (*asyncFileSink, error) {
	if maxFiles < 1 {
		maxFiles = 1
	}
	if buffer < 1 {
		buffer = 1
	}
	s := &asyncFileSink{
		basePath:   basePath,
		compatPath: compatPath,
		maxSize:    maxSize,
		maxFiles:   maxFiles,
		ch:         make(chan []byte, buffer),
	}
	if err := os.MkdirAll(filepath.Dir(basePath), 0o755); err != nil {
		return nil, err
	}
	if err := s.openFreshFile(); err != nil {
		return nil, err
	}
	s.refreshCompatPath()
	s.wg.Add(1)
	go s.run()
	return s, nil
}

func (s *asyncFileSink) Write(p []byte) (int, error) {
	buf := append([]byte(nil), p...)
	s.sendMu.RLock()
	defer s.sendMu.RUnlock()
	if s.closed {
		return len(p), nil
	}
	select {
	case s.ch <- buf:
	default:
		s.dropped.Add(1)
	}
	return len(p), nil
}

func (s *asyncFileSink) Close() {
	s.closeOnce.Do(func() {
		s.sendMu.Lock()
		s.closed = true
		close(s.ch)
		s.sendMu.Unlock()
		s.wg.Wait()
		s.cleanup()
	})
}

func (s *asyncFileSink) run() {
	defer s.wg.Done()
	for msg := range s.ch {
		s.flushDropped()
		s.write(msg)
	}
	s.flushDropped()
	if s.file != nil {
		_ = s.file.Sync()
		_ = s.file.Close()
		s.file = nil
	}
}

func (s *asyncFileSink) flushDropped() {
	dropped := s.dropped.Swap(0)
	if dropped == 0 {
		return
	}
	msg := fmt.Sprintf("debug logger dropped %d messages because the async buffer was full\n", dropped)
	s.write([]byte(msg))
}

func (s *asyncFileSink) write(msg []byte) {
	if s.file == nil {
		return
	}
	if s.maxSize > 0 && s.size > 0 && s.size+int64(len(msg)) > s.maxSize {
		if err := s.rotate(); err != nil {
			return
		}
	}
	n, err := s.file.Write(msg)
	if err != nil {
		return
	}
	s.size += int64(n)
}

func (s *asyncFileSink) rotate() error {
	if s.file != nil {
		_ = s.file.Close()
		s.file = nil
	}

	backupCount := s.maxFiles - 1
	if backupCount > 0 {
		for i := backupCount; i >= 1; i-- {
			src := s.backupPath(i)
			if i == backupCount {
				_ = os.Remove(src)
				continue
			}
			_ = os.Rename(src, s.backupPath(i+1))
		}
		_ = os.Rename(s.basePath, s.backupPath(1))
	} else {
		_ = os.Remove(s.basePath)
	}
	return s.openFreshFile()
}

func (s *asyncFileSink) openFreshFile() error {
	f, err := os.OpenFile(s.basePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	s.file = f
	s.size = 0
	return nil
}

func (s *asyncFileSink) backupPath(idx int) string {
	return fmt.Sprintf("%s.%d", s.basePath, idx)
}

func (s *asyncFileSink) cleanup() {
	matches, err := filepath.Glob(s.basePath + "*")
	if err != nil {
		return
	}
	for _, path := range matches {
		info, statErr := os.Stat(path)
		if statErr != nil {
			continue
		}
		if info.Mode().IsRegular() || info.Mode()&fs.ModeSymlink != 0 {
			_ = os.Remove(path)
		}
	}
	s.cleanupCompatPath()
}

func resolveLogPaths(requestedPath string, pid int) (basePath, compatPath string) {
	if requestedPath != defaultLogPath {
		return requestedPath, ""
	}
	return filepath.Join(defaultLogDir, fmt.Sprintf("ggcode-debug-%d.log", pid)), defaultLogPath
}

func (s *asyncFileSink) refreshCompatPath() {
	if s.compatPath == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(s.compatPath), 0o755); err != nil {
		return
	}
	_ = os.Remove(s.compatPath)
	_ = os.Symlink(s.basePath, s.compatPath)
}

func (s *asyncFileSink) cleanupCompatPath() {
	if s.compatPath == "" {
		return
	}
	target, err := os.Readlink(s.compatPath)
	if err != nil {
		return
	}
	if samePath(target, s.basePath) {
		_ = os.Remove(s.compatPath)
	}
}

func samePath(a, b string) bool {
	return filepath.Clean(a) == filepath.Clean(b)
}
