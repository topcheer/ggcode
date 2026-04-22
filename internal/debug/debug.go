package debug

import (
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
)

const (
	defaultLogDir       = "/tmp/ggcode-debug"
	defaultMaxLogSize   = 50 * 1024 * 1024
	defaultMaxLogFiles  = 3
	defaultAsyncBufSize = 1024
	envKey              = "GGCODE_DEBUG"
)

// categoryMap maps debug.Log tag strings to log file categories.
// Tags not in this map go to the main debug log file.
var categoryMap = map[string]string{
	// agent
	"agent":      "agent",
	"precompact": "agent",
	"ctx":        "agent",
	// provider
	"openai":       "provider",
	"anthropic":    "provider",
	"gemini":       "provider",
	"provider":     "provider",
	"adaptive_cap": "provider",
	// IM
	"qq":         "im",
	"tg":         "im",
	"discord":    "im",
	"slack":      "im",
	"dingtalk":   "im",
	"feishu":     "im",
	"pc_adapter": "im",
	"pc_relay":   "im",
	"stt":        "im",
	"emitter":    "im",
	// other subsystems
	"knight": "knight",
	"swarm":  "swarm",
	"tui":    "tui",
	"mcp":    "mcp",
	"plugin": "mcp",
}

// knownCategories is the set of all category names used in categoryMap.
var knownCategories = func() map[string]bool {
	m := make(map[string]bool)
	for _, cat := range categoryMap {
		m[cat] = true
	}
	return m
}()

var (
	mu        sync.RWMutex
	enabled   bool
	once      sync.Once
	sinks     map[string]*asyncFileSink // category → sink (nil = use mainSink)
	mainSink  *asyncFileSink            // writes all logs (compat)
	loggers   map[string]*log.Logger    // category → logger
	tagFilter map[string]bool           // nil = all tags allowed; non-nil = only these tags
)

// Init initializes the debug logging system based on the GGCODE_DEBUG
// environment variable. If the variable is unset or empty, no log files are
// created and all Log/Logf calls are no-ops.
//
// Supported values:
//
//	GGCODE_DEBUG=1          — enable all categories
//	GGCODE_DEBUG=true       — enable all categories
//	GGCODE_DEBUG=all        — enable all categories
//	GGCODE_DEBUG=agent      — enable only the "agent" category
//	GGCODE_DEBUG=agent,im   — enable only "agent" and "im" categories
//
// Safe to call multiple times; only the first call takes effect.
func Init() {
	once.Do(func() {
		val := os.Getenv(envKey)
		if val == "" {
			return
		}

		// Parse tag filter
		filter := parseTagFilter(val)

		pid := os.Getpid()

		// Create main sink (writes everything, for backward compat)
		mainPath := filepath.Join(defaultLogDir, fmt.Sprintf("ggcode-debug-%d.log", pid))
		main, err := newAsyncFileSink(mainPath, "", defaultMaxLogSize, defaultMaxLogFiles, defaultAsyncBufSize)
		if err != nil {
			return
		}

		sinkMap := make(map[string]*asyncFileSink)
		loggerMap := make(map[string]*log.Logger)

		// Create per-category sinks
		for cat := range knownCategories {
			// If filter is active, skip categories not in the filter
			if filter != nil && !filter[cat] {
				continue
			}
			catPath := filepath.Join(defaultLogDir, fmt.Sprintf("ggcode-%s-%d.log", cat, pid))
			sink, err := newAsyncFileSink(catPath, "", defaultMaxLogSize, defaultMaxLogFiles, defaultAsyncBufSize)
			if err != nil {
				continue
			}
			sinkMap[cat] = sink
			loggerMap[cat] = log.New(sink, "", log.Lmicroseconds)
		}

		mu.Lock()
		enabled = true
		mainSink = main
		sinks = sinkMap
		loggers = loggerMap
		tagFilter = filter
		mu.Unlock()
	})
}

// parseTagFilter parses the GGCODE_DEBUG value into a tag filter set.
// Returns nil if all tags should be logged.
func parseTagFilter(val string) map[string]bool {
	val = strings.TrimSpace(strings.ToLower(val))
	if val == "" || val == "1" || val == "true" || val == "all" {
		return nil // log everything
	}
	filter := make(map[string]bool)
	for _, part := range strings.Split(val, ",") {
		p := strings.TrimSpace(part)
		if p != "" {
			filter[p] = true
		}
	}
	if len(filter) == 0 {
		return nil
	}
	return filter
}

// Active reports whether the debug logger is enabled.
func Active() bool {
	mu.RLock()
	defer mu.RUnlock()
	return enabled
}

// Log writes a formatted message with a package tag to the appropriate
// category-specific log file and the main log file.
// Format: [HH:MM:SS.mmm] [pkg] message
func Log(pkg, format string, args ...interface{}) {
	mu.RLock()
	if !enabled {
		mu.RUnlock()
		return
	}
	cat := categoryMap[pkg]
	sink := sinks[cat]
	l := loggers[cat]
	ms := mainSink
	filt := tagFilter
	mu.RUnlock()

	// Apply tag filter
	if filt != nil {
		if cat == "" || !filt[cat] {
			return
		}
	}

	msg := fmt.Sprintf("[%s] "+format+"\n", append([]interface{}{pkg}, args...)...)

	// Write to category-specific file
	if l != nil {
		l.Print(msg)
	}

	// Write to main file
	if ms != nil && sink != ms {
		_, _ = ms.Write([]byte(msg))
	}
}

// Logf writes a raw formatted message (no package tag).
func Logf(format string, args ...interface{}) {
	mu.RLock()
	if !enabled {
		mu.RUnlock()
		return
	}
	ms := mainSink
	mu.RUnlock()

	if ms != nil {
		msg := fmt.Sprintf(format+"\n", args...)
		_, _ = ms.Write([]byte(msg))
	}
}

// Close flushes, closes, and removes all debug log files for the current process.
func Close() {
	mu.Lock()
	enabled = false
	catSinks := sinks
	sinks = nil
	loggers = nil
	ms := mainSink
	mainSink = nil
	tagFilter = nil
	mu.Unlock()

	for _, s := range catSinks {
		s.Close()
	}
	if ms != nil {
		ms.Close()
	}
}

// --- async file sink (unchanged internals) ---

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
	compatDefault := "/tmp/ggcode-debug.log"
	if requestedPath != compatDefault {
		return requestedPath, ""
	}
	return filepath.Join(defaultLogDir, fmt.Sprintf("ggcode-debug-%d.log", pid)), compatDefault
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

// EnableForTest forces the debug system on for the given categories.
// If categories is empty, all categories are enabled.
func EnableForTest(t interface{ Cleanup(func()) }, categories ...string) {
	Close()
	mu.Lock()
	once = sync.Once{}
	enabled = false
	mainSink = nil
	sinks = nil
	loggers = nil
	tagFilter = nil
	mu.Unlock()

	// Set env so Init() will activate
	if len(categories) == 0 {
		os.Setenv(envKey, "1")
	} else {
		os.Setenv(envKey, strings.Join(categories, ","))
	}

	t.Cleanup(func() {
		Close()
		mu.Lock()
		once = sync.Once{}
		enabled = false
		mainSink = nil
		sinks = nil
		loggers = nil
		tagFilter = nil
		mu.Unlock()
		os.Unsetenv(envKey)
	})

	Init()
}
