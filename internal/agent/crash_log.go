package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sort"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	ggdebug "github.com/topcheer/ggcode/internal/debug"
)

const crashLogDir = "crash"

func WriteCrashLog(component string, val any) string {

	component = filepath.Base(filepath.Clean(component))
	dir := filepath.Join(config.ConfigDir(), crashLogDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Sprintf("<crash log not written: %v>", err)
	}

	now := time.Now()
	name := fmt.Sprintf("%s-%s-%09d.log", component, now.Format("20060102-150405"), now.Nanosecond())
	path := filepath.Join(dir, name)

	panicText := func() (out string) {
		defer func() {
			if recover() != nil {
				out = fmt.Sprintf("<unprintable %T>", val)
			}
		}()
		return fmt.Sprintf("%v", val)
	}()

	stack := debug.Stack()
	const maxStack = 1 << 20
	if len(stack) > maxStack {

		stack = append(stack[:maxStack], []byte(fmt.Sprintf("\n[primary stack truncated at %d bytes; oldest frames dropped]\n", maxStack))...)
	}
	const maxAllStack = 8 << 20
	allBuf := make([]byte, 1<<20)
	for {
		n := runtime.Stack(allBuf, true)
		if n < len(allBuf) {
			allBuf = allBuf[:n]
			break
		}
		if len(allBuf) >= maxAllStack {

			allBuf = append(allBuf[:maxAllStack], []byte(fmt.Sprintf("\n[all-goroutine dump truncated at %d bytes; newest goroutines dropped]\n", maxAllStack))...)
			break
		}
		allBuf = make([]byte, len(allBuf)*2)
	}
	body := fmt.Sprintf("time:      %s\ncomponent: %s\npanic:     %s\n\n%s\n\n=== all goroutines ===\n%s\n",
		time.Now().Format(time.RFC3339), component, panicText, stack, allBuf)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return fmt.Sprintf("<crash log not written: %v>", err)
	}

	pruneCrashLogs(dir)
	return path
}

const crashLogRetention = 20

const crashLogMaxAge = 7 * 24 * time.Hour

func pruneCrashLogs(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type logFile struct {
		name    string
		modTime time.Time
	}
	var logs []logFile
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".log") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		logs = append(logs, logFile{name: e.Name(), modTime: info.ModTime()})
	}
	if len(logs) <= crashLogRetention {
		return
	}

	sort.Slice(logs, func(i, j int) bool { return logs[i].modTime.After(logs[j].modTime) })
	for _, lf := range logs[crashLogRetention:] {
		if time.Since(lf.modTime) < crashLogMaxAge {
			continue
		}
		if err := os.Remove(filepath.Join(dir, lf.name)); err == nil {
			ggdebug.Log("crash-log", "pruned old crash log %s", lf.name)
		}
	}
}
