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
	"time"

	"github.com/topcheer/ggcode/internal/safego"
	"github.com/topcheer/ggcode/internal/util"
)

func init() {

	safego.SetLogger(Log)
}

var (
	defaultLogDir string
)

const (
	defaultMaxLogSize   int64 = 50 * 1024 * 1024
	defaultMaxLogFiles        = 3
	defaultAsyncBufSize       = 1024
	maxMessageLen             = 4096
	envKey                    = "GGCODE_DEBUG"
)

type Category struct {
	Name        string
	EnvSuffix   string
	Description string
	Tags        []string
	aliases     map[string]string
}

var Categories = []Category{

	{
		Name: "agent", EnvSuffix: "AGENT",
		Description: "Agent loop, tool execution, autopilot, compaction, sub-agents",
		Tags:        []string{"agent", "precompact", "subagent", "acp"},
	},
	{
		Name: "context", EnvSuffix: "CONTEXT",
		Description: "Context window management, summarize, microcompact",
		Tags:        []string{"ctx"},
	},

	{
		Name: "openai", EnvSuffix: "OPENAI",
		Description: "OpenAI provider — chat, streaming, message conversion",
		Tags:        []string{"openai", "openai-stream", "openai-chat", "openai-probe"},
	},
	{
		Name: "anthropic", EnvSuffix: "ANTHROPIC",
		Description: "Anthropic provider — chat, streaming, message conversion",
		Tags:        []string{"anthropic", "anthropic-stream", "anthropic-chat", "anthropic-probe"},
	},
	{
		Name: "gemini", EnvSuffix: "GEMINI",
		Description: "Gemini provider — chat, streaming, message conversion",
		Tags:        []string{"gemini", "gemini-stream", "gemini-chat", "gemini-probe"},
	},
	{
		Name: "provider", EnvSuffix: "PROVIDER",
		Description: "Provider registry, token estimation, adaptive cap, generic streaming",
		Tags:        []string{"provider", "adaptive_cap", "stream", "llm-classifier"},
	},
	{
		Name: "probe", EnvSuffix: "PROBE",
		Description: "Context window probing and cache",
		Tags:        []string{"probe"},
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
		Name: "whatsapp", EnvSuffix: "WHATSAPP",
		Description: "WhatsApp IM adapter (whatsmeow)",
		Tags:        []string{"whatsapp"},
	},
	{
		Name: "matrix", EnvSuffix: "MATRIX",
		Description: "Matrix IM adapter",
		Tags:        []string{"matrix"},
	},
	{
		Name: "wechat", EnvSuffix: "WECHAT",
		Description: "WeChat (微信) IM adapter",
		Tags:        []string{"wechat"},
	},
	{
		Name: "wecom", EnvSuffix: "WECOM",
		Description: "WeCom (企业微信) IM adapter",
		Tags:        []string{"wecom"},
	},
	{
		Name: "signal", EnvSuffix: "SIGNAL",
		Description: "Signal IM adapter",
		Tags:        []string{"signal"},
	},
	{
		Name: "mattermost", EnvSuffix: "MATTERMOST",
		Description: "Mattermost IM adapter",
		Tags:        []string{"mattermost"},
	},
	{
		Name: "twitch", EnvSuffix: "TWITCH",
		Description: "Twitch IM adapter",
		Tags:        []string{"twitch"},
	},
	{
		Name: "nostr", EnvSuffix: "NOSTR",
		Description: "Nostr IM adapter",
		Tags:        []string{"nostr"},
	},
	{
		Name: "irc", EnvSuffix: "IRC",
		Description: "IRC IM adapter",
		Tags:        []string{"irc"},
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
		Name: "tui", EnvSuffix: "TUI",
		Description: "TUI model updates, repl, completion, submit",
		Tags:        []string{"tui", "repl", "completion", "command-gate", "layout"},
	},
	{
		Name: "webui", EnvSuffix: "WEBUI",
		Description: "WebUI HTTP server, WebSocket chat, bridges",
		Tags:        []string{"webui", "tui-bridge"},
	},
	{
		Name: "daemon", EnvSuffix: "DAEMON",
		Description: "Daemon mode, daemon bridge, restart",
		Tags:        []string{"daemon", "daemon-bridge", "restart"},
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
		Description: "Agent-to-agent server, OAuth, mDNS, registry",
		Tags:        []string{"a2a", "a2a.oauth", "a2a.mdns", "a2a.registry"},
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
		Name: "cron", EnvSuffix: "CRON",
		Description: "Cron scheduler, job lifecycle, migration",
		Tags:        []string{"cron"},
	},
	{
		Name: "runfile", EnvSuffix: "RUNFILE",
		Description: "Port file write/read/cleanup for instance discovery",
		Tags:        []string{"runfile"},
	},
	{
		Name: "bubbletea", EnvSuffix: "BUBBLETEA",
		Description: "Bubble Tea framework internal trace (TEA_TRACE), controlled independently via GGCODE_DEBUG_BUBBLETEA",
		Tags:        []string{},
	},
}

var tagToCategory map[string]string

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
	sinks     map[string]*asyncFileSink
	mainSink  *asyncFileSink
	liveSink  func(category, msg string)
	loggers   map[string]*log.Logger
	tagFilter map[string]bool
	verbose   map[string]bool
)

func Init() {
	once.Do(func() {

		if defaultLogDir == "" {
			defaultLogDir = resolveDebugDir()
		}

		cleanupStaleLogs()

		perCategoryEnvs, verbCats := scanPerCategoryEnvs()

		var filter map[string]bool
		globalVal := os.Getenv(envKey)

		if perCategoryEnvs != nil {

			filter = perCategoryEnvs
		} else if globalVal != "" {
			filter = parseTagFilter(globalVal)
		} else {

			return
		}

		pid := os.Getpid()

		mainPath := filepath.Join(defaultLogDir, fmt.Sprintf("ggcode-debug-%d.log", pid))
		main, err := newAsyncFileSink(mainPath, "", defaultMaxLogSize, defaultMaxLogFiles, defaultAsyncBufSize)
		if err != nil {
			return
		}

		sinkMap := make(map[string]*asyncFileSink)
		loggerMap := make(map[string]*log.Logger)

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
		verbose = verbCats
		mu.Unlock()
	})
}

func scanPerCategoryEnvs() (enabled map[string]bool, verb map[string]bool) {
	enabled = make(map[string]bool)
	verb = make(map[string]bool)
	for _, cat := range Categories {
		envName := envKey + "_" + cat.EnvSuffix
		val := os.Getenv(envName)
		if val == "" {
			continue
		}
		if isVerbose(val) {
			enabled[cat.Name] = true
			verb[cat.Name] = true
		} else if isTruthy(val) {
			enabled[cat.Name] = true
		}
	}
	if len(enabled) == 0 {
		return nil, nil
	}
	return enabled, verb
}

func isVerbose(val string) bool {
	v := strings.TrimSpace(strings.ToLower(val))
	return v == "2" || v == "verbose" || v == "trace"
}

func isTruthy(val string) bool {
	v := strings.TrimSpace(strings.ToLower(val))
	return v == "1" || v == "true" || v == "yes" || v == "on"
}

func parseTagFilter(val string) map[string]bool {
	if isTruthy(val) || strings.EqualFold(val, "all") {
		return nil
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

func Active() bool {
	mu.RLock()
	defer mu.RUnlock()
	return enabled
}

func MainLogPath() string {
	mu.RLock()
	defer mu.RUnlock()
	if !enabled || mainSink == nil {
		return ""
	}
	return mainSink.basePath
}

var ensureMu sync.Mutex

func EnsureFileLogging() (wasEnabled bool, path string) {
	ensureMu.Lock()
	defer ensureMu.Unlock()

	if Active() {
		return true, MainLogPath()
	}

	Close()
	mu.Lock()
	once = sync.Once{}
	enabled = false
	mainSink = nil
	sinks = nil
	loggers = nil
	tagFilter = nil
	mu.Unlock()

	os.Setenv(envKey, "1")
	Init()

	return false, MainLogPath()
}

func IsVerbose(pkg string) bool {
	mu.RLock()
	defer mu.RUnlock()
	if !enabled {
		return false
	}
	cat := tagToCategory[pkg]
	return verbose[cat]
}

func truncateMessage(msg string) string {
	if len(msg) <= maxMessageLen {
		return msg
	}
	cut := util.SnapToRuneStart(msg, maxMessageLen-3)
	return msg[:cut] + "..."
}

func Log(pkg, format string, args ...interface{}) {
	cat := tagToCategory[pkg]
	msg := fmt.Sprintf("[%s] "+format, append([]interface{}{pkg}, args...)...)
	msg = truncateMessage(msg)

	mu.RLock()
	sink := liveSink
	mu.RUnlock()
	if sink != nil {
		sink(cat, msg)
	}

	ringAppend(cat, msg)

	mu.RLock()
	if !enabled {
		mu.RUnlock()
		return
	}
	l := loggers[cat]
	ms := mainSink
	filt := tagFilter
	mu.RUnlock()

	if filt != nil {
		if cat == "" || !filt[cat] {
			return
		}
	}

	fileMsg := msg + "\n"

	if l != nil {
		l.Print(fileMsg)
	}

	if ms != nil {
		ts := time.Now().Format("15:04:05.000000")
		_, _ = ms.Write([]byte(ts + " " + fileMsg))
	}
}

func SetLiveSink(fn func(category, msg string)) {
	mu.Lock()
	liveSink = fn
	mu.Unlock()
}

func Logf(format string, args ...interface{}) {
	mu.RLock()
	if !enabled {
		mu.RUnlock()
		return
	}
	ms := mainSink
	mu.RUnlock()

	if ms != nil {
		ts := time.Now().Format("15:04:05.000000")
		msg := fmt.Sprintf(format, args...)
		msg = truncateMessage(msg)
		msg += "\n"
		_, _ = ms.Write([]byte(ts + " " + msg))
	}
}

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

	cleanupBubbleteaTrace()
}

const staleLogMaxAge = 14 * 24 * time.Hour

func cleanupStaleLogs() {
	entries, err := os.ReadDir(defaultLogDir)
	if err != nil {
		return
	}
	selfPid := os.Getpid()
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		pid := extractPidFromLogName(name)
		if pid <= 0 {
			continue
		}
		if pid == selfPid {
			continue
		}

		proc, _ := os.FindProcess(pid)
		if !util.IsProcessAliveProc(proc) {

			_ = os.Remove(filepath.Join(defaultLogDir, name))
			continue
		}

		if info, statErr := entry.Info(); statErr == nil && time.Since(info.ModTime()) > staleLogMaxAge {
			_ = os.Remove(filepath.Join(defaultLogDir, name))
		}
	}
}

func extractPidFromLogName(name string) int {

	if !strings.HasPrefix(name, "ggcode-") {
		return 0
	}
	base := name

	if idx := strings.LastIndex(base, ".log"); idx >= 0 {
		base = base[:idx]
	}

	lastDash := strings.LastIndex(base, "-")
	if lastDash < 0 {
		return 0
	}
	pidStr := base[lastDash+1:]
	pid := 0
	for _, c := range pidStr {
		if c >= '0' && c <= '9' {
			pid = pid*10 + int(c-'0')
		} else {
			return 0
		}
	}
	return pid
}

func cleanupBubbleteaTrace() {
	pid := os.Getpid()
	path := filepath.Join(defaultLogDir, fmt.Sprintf("ggcode-bubbletea-%d.log", pid))

	if te := os.Getenv("TEA_TRACE"); te == path {
		_ = os.Remove(path)

		matches, _ := filepath.Glob(path + ".*")
		for _, m := range matches {
			_ = os.Remove(m)
		}
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

func resolveDebugDir() string {
	home := util.HomeDir()
	if home != "" {
		dir := filepath.Join(home, ".ggcode", "debug")
		if os.MkdirAll(dir, 0o700) == nil {
			return dir
		}
	}

	uidNum := os.Getuid()
	if uidNum > 0 {
		return filepath.Join(os.TempDir(), fmt.Sprintf("ggcode-debug-%d", uidNum))
	}
	return filepath.Join(os.TempDir(), "ggcode-debug-0")
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
	if err := os.MkdirAll(filepath.Dir(basePath), 0o700); err != nil {
		return nil, err
	}
	if err := s.openFreshFile(); err != nil {
		return nil, err
	}
	s.refreshCompatPath()
	s.wg.Add(1)
	safego.Go("debug.asyncFileSink", func() { s.run() })
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
	f, err := os.OpenFile(s.basePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
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
	logDir := resolveDebugDir()
	compatDefault := "/tmp/ggcode-debug.log"
	if requestedPath != compatDefault {
		return requestedPath, ""
	}
	return filepath.Join(logDir, fmt.Sprintf("ggcode-debug-%d.log", pid)), compatDefault
}

func (s *asyncFileSink) refreshCompatPath() {
	if s.compatPath == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(s.compatPath), 0o700); err != nil {
		return
	}
	_ = os.Remove(s.compatPath)
	_ = util.SafeSymlink(s.basePath, s.compatPath)
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
