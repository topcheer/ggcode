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
	maxMessageLen       = 50
	envKey              = "GGCODE_DEBUG"
)

// Category represents a debug log output channel. Each category maps to its
// own log file and can be independently enabled via GGCODE_DEBUG_<CATEGORY>.
type Category struct {
	Name        string            // e.g. "agent"
	EnvSuffix   string            // e.g. "AGENT" → GGCODE_DEBUG_AGENT=1
	Description string            // human-readable
	Tags        []string          // debug.Log tags routed to this category
	aliases     map[string]string // tag → canonical tag (populated by init)
}

// Categories is the ordered list of all debug log categories.
// Order matters: listed first = checked first when building tag routing.
var Categories = []Category{
	{
		Name: "agent", EnvSuffix: "AGENT",
		Description: "Agent loop, tool execution, autopilot, compaction",
		Tags:        []string{"agent", "precompact"},
	},
	{
		Name: "context", EnvSuffix: "CONTEXT",
		Description: "Context window management, summarize, microcompact",
		Tags:        []string{"ctx"},
	},
	{
		Name: "openai", EnvSuffix: "OPENAI",
		Description: "OpenAI provider streaming and message conversion",
		Tags:        []string{"openai"},
	},
	{
		Name: "anthropic", EnvSuffix: "ANTHROPIC",
		Description: "Anthropic provider streaming",
		Tags:        []string{"anthropic"},
	},
	{
		Name: "gemini", EnvSuffix: "GEMINI",
		Description: "Gemini provider",
		Tags:        []string{"gemini"},
	},
	{
		Name: "provider", EnvSuffix: "PROVIDER",
		Description: "Provider creation, token estimation, adaptive cap",
		Tags:        []string{"provider", "adaptive_cap"},
	},
	{
		Name: "qq", EnvSuffix: "QQ",
		Description: "QQ IM adapter",
		Tags:        []string{"qq"},
	},
	{
		Name: "tg", EnvSuffix: "TG",
		Description: "Telegram IM adapter",
		Tags:        []string{"tg"},
	},
	{
		Name: "discord", EnvSuffix: "DISCORD",
		Description: "Discord IM adapter",
		Tags:        []string{"discord"},
	},
	{
		Name: "slack", EnvSuffix: "SLACK",
		Description: "Slack IM adapter",
		Tags:        []string{"slack"},
	},
	{
		Name: "dingtalk", EnvSuffix: "DINGTALK",
		Description: "DingTalk IM adapter",
		Tags:        []string{"dingtalk"},
	},
	{
		Name: "feishu", EnvSuffix: "FEISHU",
		Description: "Feishu/Lark IM adapter",
		Tags:        []string{"feishu", "feishu-sdk"},
	},
	{
		Name: "pc", EnvSuffix: "PC",
		Description: "PC relay adapter and client",
		Tags:        []string{"pc", "pc_adapter", "pc_relay"},
	},
	{
		Name: "im", EnvSuffix: "IM",
		Description: "IM runtime, emitter, STT, dummy adapter",
		Tags:        []string{"im", "emitter", "stt", "dummy", "im-send"},
	},
	{
		Name: "knight", EnvSuffix: "KNIGHT",
		Description: "Knight scheduler, analyzer, runner",
		Tags:        []string{"knight"},
	},
	{
		Name: "swarm", EnvSuffix: "SWARM",
		Description: "Swarm multi-agent teammates and task board",
		Tags:        []string{"swarm"},
	},
	{
		Name: "tui", EnvSuffix: "TUI",
		Description: "TUI model updates, repl, completion, submit",
		Tags:        []string{"tui", "repl", "completion", "command-gate"},
	},
	{
		Name: "mcp", EnvSuffix: "MCP",
		Description: "MCP client, discovery, connect, HTTP, OAuth",
		Tags:        []string{"mcp", "mcp-oauth", "mcp-discover", "mcp-connect", "mcp-http"},
	},
	{
		Name: "plugin", EnvSuffix: "PLUGIN",
		Description: "Plugin and MCP loader",
		Tags:        []string{"plugin"},
	},
	{
		Name: "a2a", EnvSuffix: "A2A",
		Description: "Agent-to-agent server and handler",
		Tags:        []string{"a2a"},
	},
	{
		Name: "daemon", EnvSuffix: "DAEMON",
		Description: "Daemon mode checkpointing",
		Tags:        []string{"daemon"},
	},
	{
		Name: "config", EnvSuffix: "CONFIG",
		Description: "Config loading",
		Tags:        []string{"config"},
	},
	{
		Name: "permission", EnvSuffix: "PERMISSION",
		Description: "Permission policy decisions",
		Tags:        []string{"permission"},
	},
	{
		Name: "run-command", EnvSuffix: "RUN_COMMAND",
		Description: "Command execution (foreground/background)",
		Tags:        []string{"run_command"},
	},
	{
		Name: "safego", EnvSuffix: "SAFEGO",
		Description: "Goroutine panic recovery",
		Tags:        []string{"safego"},
	},
	{
		Name: "bubbletea", EnvSuffix: "BUBBLETEA",
		Description: "Bubble Tea framework internal trace (TEA_TRACE), controlled independently via GGCODE_DEBUG_BUBBLETEA",
		Tags:        []string{}, // not routed through debug.Log; bubbletea writes directly
	},
}

// tagToCategory maps each debug.Log tag to its category name.
// Populated from Categories by init().
var tagToCategory map[string]string

// knownCategoryNames is the set of all category.Name values.
var knownCategoryNames map[string]bool

func init() {
	tagToCategory = make(map[string]string)
	knownCategoryNames = make(map[string]bool)
	for _, cat := range Categories {
		knownCategoryNames[cat.Name] = true
		for _, tag := range cat.Tags {
			tagToCategory[tag] = cat.Name
		}
	}
}

var (
	mu        sync.RWMutex
	enabled   bool
	once      sync.Once
	sinks     map[string]*asyncFileSink // category name → sink
	mainSink  *asyncFileSink            // writes all logs (compat)
	loggers   map[string]*log.Logger    // category name → logger
	tagFilter map[string]bool           // nil = all; non-nil = only these categories
)

// Init initializes the debug logging system.
//
// ## Global control
//
// GGCODE_DEBUG=1|true|all — enable ALL categories.
// GGCODE_DEBUG=<cat1>,<cat2> — enable only listed categories.
// GGCODE_DEBUG unset or empty — logging disabled (default).
//
// ## Per-category control (overrides global)
//
// If any GGCODE_DEBUG_<SUFFIX> variable is set to a truthy value,
// that category is enabled regardless of the global GGCODE_DEBUG value.
// When per-category env vars exist, the global GGCODE_DEBUG is ignored
// entirely for the filter decision.
//
// Examples:
//
//	GGCODE_DEBUG_AGENT=1         — only agent logs
//	GGCODE_DEBUG_OPENAI=1 GGCODE_DEBUG_AGENT=1 — openai + agent logs
//	GGCODE_DEBUG=1               — all logs (every category)
//	GGCODE_DEBUG=agent,provider  — agent + provider categories
//
// ## Category environment variable names
//
// Each category has an EnvSuffix; the env var is GGCODE_DEBUG_<SUFFIX>.
// See the Categories variable for the full list.
func Init() {
	once.Do(func() {
		// Check per-category env vars first
		perCategoryEnvs := scanPerCategoryEnvs()

		var filter map[string]bool
		globalVal := os.Getenv(envKey)

		if len(perCategoryEnvs) > 0 {
			// Per-category env vars take precedence; ignore global GGCODE_DEBUG
			filter = perCategoryEnvs
		} else if globalVal != "" {
			filter = parseTagFilter(globalVal)
		} else {
			// No env vars set — logging disabled
			return
		}

		pid := os.Getpid()

		// Create main sink (all messages combined)
		mainPath := filepath.Join(defaultLogDir, fmt.Sprintf("ggcode-debug-%d.log", pid))
		main, err := newAsyncFileSink(mainPath, "", defaultMaxLogSize, defaultMaxLogFiles, defaultAsyncBufSize)
		if err != nil {
			return
		}

		sinkMap := make(map[string]*asyncFileSink)
		loggerMap := make(map[string]*log.Logger)

		// Create per-category sinks
		for _, cat := range Categories {
			if filter != nil && !filter[cat.Name] {
				continue
			}
			catPath := filepath.Join(defaultLogDir, fmt.Sprintf("ggcode-%s-%d.log", cat.Name, pid))
			sink, err := newAsyncFileSink(catPath, "", defaultMaxLogSize, defaultMaxLogFiles, defaultAsyncBufSize)
			if err != nil {
				continue
			}
			sinkMap[cat.Name] = sink
			loggerMap[cat.Name] = log.New(sink, "", log.Lmicroseconds)
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

// scanPerCategoryEnvs checks for GGCODE_DEBUG_<SUFFIX> env vars.
// Returns a set of enabled category names, or nil if none found.
func scanPerCategoryEnvs() map[string]bool {
	enabled := make(map[string]bool)
	found := false
	for _, cat := range Categories {
		envName := envKey + "_" + cat.EnvSuffix
		if isTruthy(os.Getenv(envName)) {
			enabled[cat.Name] = true
			found = true
		}
	}
	if !found {
		return nil
	}
	return enabled
}

// isTruthy returns true for "1", "true", "yes", "on" (case-insensitive).
func isTruthy(val string) bool {
	v := strings.TrimSpace(strings.ToLower(val))
	return v == "1" || v == "true" || v == "yes" || v == "on"
}

// parseTagFilter parses the GGCODE_DEBUG value into a category filter set.
// Returns nil if all categories should be logged.
func parseTagFilter(val string) map[string]bool {
	if isTruthy(val) || strings.EqualFold(val, "all") {
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
	cat := tagToCategory[pkg]
	l := loggers[cat]
	ms := mainSink
	filt := tagFilter
	mu.RUnlock()

	// Apply category filter
	if filt != nil {
		if cat == "" || !filt[cat] {
			return
		}
	}

	msg := fmt.Sprintf("[%s] "+format, append([]interface{}{pkg}, args...)...)
	if len(msg) > maxMessageLen {
		msg = msg[:maxMessageLen]
	}
	msg += "\n"

	// Write to category-specific file
	if l != nil {
		l.Print(msg)
	}

	// Write to main file
	if ms != nil {
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
		msg := fmt.Sprintf(format, args...)
		if len(msg) > maxMessageLen {
			msg = msg[:maxMessageLen]
		}
		msg += "\n"
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

	// Clean up bubbletea trace log if we created it via TEA_TRACE.
	cleanupBubbleteaTrace()
}

// cleanupBubbleteaTrace removes the ggcode-bubbletea-{pid}.log file
// that was created by enableBubbleteaTrace() in the tui package.
func cleanupBubbleteaTrace() {
	pid := os.Getpid()
	path := filepath.Join(defaultLogDir, fmt.Sprintf("ggcode-bubbletea-%d.log", pid))
	// Only remove if TEA_TRACE points at our file (not user-set).
	if te := os.Getenv("TEA_TRACE"); te == path {
		_ = os.Remove(path)
		// Also remove rotated copies
		matches, _ := filepath.Glob(path + ".*")
		for _, m := range matches {
			_ = os.Remove(m)
		}
	}
}

// --- async file sink ---

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
