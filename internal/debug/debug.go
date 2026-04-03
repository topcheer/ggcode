package debug

import (
	"io"
	"log"
	"os"
	"sync"
)

var (
	logFile *os.File
	logger  *log.Logger
	once    sync.Once
)

// Init opens the debug log file. Safe to call multiple times.
func Init() {
	once.Do(func() {
		f, err := os.OpenFile("/tmp/ggcode-debug.log", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
		if err != nil {
			return
		}
		logFile = f
		logger = log.New(io.MultiWriter(os.Stderr, f), "", log.Lmicroseconds)
	})
}

// Log writes a formatted message with a package tag.
// Format: [HH:MM:SS.mmm] [pkg] message
func Log(pkg, format string, args ...interface{}) {
	if logger == nil {
		return
	}
	logger.Printf("[%s] "+format, append([]interface{}{pkg}, args...)...)
}

// Logf writes a raw formatted message (no package tag).
func Logf(format string, args ...interface{}) {
	if logger == nil {
		return
	}
	logger.Printf(format, args...)
}

// Close flushes and closes the log file.
func Close() {
	if logFile != nil {
		_ = logFile.Close()
	}
}
