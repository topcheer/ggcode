package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/tool"
)

type logWriter struct{}

func (logWriter) Write(p []byte) (int, error) {
	debug.Log("stderr", "%s", string(p))
	return len(p), nil
}

var origStderr *os.File

var restoreStderrFD func()

func redirectStderr() {
	origStderr = os.Stderr

	r, w, err := os.Pipe()
	if err != nil {

		if devNull, err2 := os.OpenFile(os.DevNull, os.O_WRONLY, 0); err2 == nil {
			os.Stderr = devNull
		}
		return
	}

	os.Stderr = w

	if restore, ok := teeStderrFD(w); ok {
		restoreStderrFD = restore
	}

	var mu sync.Mutex
	buf := make([]byte, 0, 4096)

	const ringCap = 256 << 10
	ring := make([]byte, 0, ringCap+4096)
	var dumpFile *os.File

	startFatalCapture := func() {
		if dumpFile != nil {
			return
		}
		dir := config.ConfigDir() + string(os.PathSeparator) + "crash"
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return
		}
		path := filepath.Join(dir, fmt.Sprintf("fatal-%s.log", time.Now().Format("20060102-150405")))
		f, ferr := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if ferr != nil {
			return
		}
		dumpFile = f
		_, _ = f.Write(ring)
	}

	go func() {
		tmp := make([]byte, 1024)
		for {
			n, err := r.Read(tmp)
			if n > 0 {
				mu.Lock()
				chunk := tmp[:n]

				ring = append(ring, chunk...)
				if len(ring) > ringCap {
					ring = ring[len(ring)-ringCap:]
				}
				if dumpFile != nil {

					_, _ = dumpFile.Write(chunk)
					mu.Unlock()
					if err != nil {
						_ = dumpFile.Close()
					}
					if err != nil {
						break
					}
					continue
				}
				buf = append(buf, chunk...)
				for {
					idx := -1
					for i, b := range buf {
						if b == '\n' {
							idx = i
							break
						}
					}
					if idx < 0 {
						break
					}
					line := string(buf[:idx])
					buf = buf[idx+1:]
					if line != "" {
						if looksFatal(line) {

							buf = append([]byte(line+"\n"), buf...)
							startFatalCapture()
							if dumpFile != nil {
								_, _ = dumpFile.Write([]byte(line + "\n"))
							}
							break
						}
						debug.Log("stderr", "%s", line)
					}
				}
				mu.Unlock()
			}
			if err != nil {
				break
			}
		}
		if dumpFile != nil {
			mu.Lock()
			rest := buf
			buf = nil
			mu.Unlock()
			if len(rest) > 0 {
				_, _ = dumpFile.Write(rest)
			}
			_ = dumpFile.Close()
		}
	}()
}

func looksFatal(line string) bool {
	return strings.HasPrefix(line, "fatal error:") ||
		strings.HasPrefix(line, "panic:") ||
		strings.HasPrefix(line, "SIGSEGV") ||
		strings.HasPrefix(line, "SIGABRT") ||
		strings.HasPrefix(line, "goroutine ") && strings.HasSuffix(line, " [running]:")
}

func shouldRedirectStderr(args []string) bool {
	for _, a := range args {
		if a == "-p" || a == "--prompt" || strings.HasPrefix(a, "--prompt=") {
			return false
		}
	}

	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		return false
	}
	return true
}

func main() {

	if len(os.Args) > 2 && os.Args[1] == tool.SandboxLaunchMarker {
		if tool.SandboxLaunchEntry == nil {
			fmt.Fprintln(os.Stderr, "ggcode sandbox launcher: missing on this platform")
			os.Exit(126)
		}
		tool.SandboxLaunchEntry(os.Args[2], os.Args[3:])
		os.Exit(126)
	}

	defer func() {
		if r := recover(); r != nil {
			path := agent.WriteCrashLog("cli", r)

			debug.Close()

			if restoreStderrFD != nil {
				restoreStderrFD()
			}
			if origStderr != nil {
				os.Stderr = origStderr
			}
			fmt.Fprintf(os.Stderr, "ggcode crashed: %v\npanic log: %s\nPlease report this with the log file if it repeats.\n", r, path)
			os.Exit(1)
		}
	}()
	defer debug.Close()

	if shouldRedirectStderr(os.Args[1:]) {
		redirectStderr()

		log.SetOutput(logWriter{})
		log.SetFlags(0)
	}

	cmd := NewRootCmd()
	if err := cmd.Execute(); err != nil {

		if restoreStderrFD != nil {
			restoreStderrFD()
		}
		if origStderr != nil {
			os.Stderr = origStderr
		}
		fmt.Fprintln(os.Stderr, err)
		debug.Close()
		os.Exit(1)
	}
}
