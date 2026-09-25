package tool

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

// Full-output spooling for run_command (r98).
//
// Problem: run_command captures stdout/stderr in a boundedOutputWriter
// (200KB per stream) and then applies truncateMiddle (100KB per stream).
// Both cut points are lossy in different places, and the dropped middle is
// gone forever. For long build/test/codegen logs the agent's only recovery
// was re-running the command — expensive, and non-deterministic for
// commands with side effects.
//
// Fix: mirror the full raw stream to a file under <workdir>/.ggcode/spool/
// while the command runs. Activation is lazy: nothing touches the disk
// until a stream crosses spoolThreshold, so the common short-output case
// behaves exactly as before. When active, the spool file holds the exact
// byte stream (bounded retention and truncateMiddle still shape what goes
// inline into the model, but the full log stays greppable on disk), and
// the tool result is annotated with the path so the agent can grep or
// read_file(offset/limit) specific sections instead of re-running.
//
// This follows the harness "observation" responsibility pattern of
// spooling oversized tool output to disk instead of losing it (see
// arXiv:2606.20683 six-responsibility harness decomposition; the same
// pattern is used by mainstream coding-agent harnesses in 2025-2026).

const (
	spoolDirName = ".ggcode"
	spoolSubDir  = "spool"

	// spoolThreshold is the stream size at which spooling activates. It must
	// stay below the bounded writer's head cap (2*maxOutputSize/2 ==
	// maxOutputSize == 100KB): at activation time we seed the file from the
	// bounded writer's retained content, and below the head cap nothing has
	// been dropped yet, so the seed is byte-exact.
	spoolThreshold = 64 * 1024

	// spoolMaxAge bounds how long spool artifacts live in the user's
	// workspace. Pruning is best-effort and runs only when a new spool file
	// is created.
	spoolMaxAge = 48 * time.Hour
)

// spoolOutputWriter wraps a boundedOutputWriter, mirroring the full raw
// stream to a lazily-created file once the stream crosses spoolThreshold.
// It is an io.Writer drop-in: file I/O failures degrade silently to the
// previous memory-only behavior and never break the command's output pipe
// (a failed cmd.Stdout write can kill the child process with EPIPE).
type spoolOutputWriter struct {
	bounded *boundedOutputWriter
	dir     string // <workdir>/.ggcode/spool

	mu      sync.Mutex
	file    *os.File
	path    string
	written int64
	failed  bool
}

// newSpoolOutputWriter wraps bounded capture with disk spooling rooted at
// workDir. An empty workDir resolves to the process working directory at
// activation time.
func newSpoolOutputWriter(bounded *boundedOutputWriter, workDir string) *spoolOutputWriter {
	return &spoolOutputWriter{
		bounded: bounded,
		dir:     spoolDirFor(workDir),
	}
}

// spoolDirFor resolves the spool directory for a working directory.
func spoolDirFor(workDir string) string {
	if workDir == "" {
		return filepath.Join(spoolDirName, spoolSubDir)
	}
	return filepath.Join(workDir, spoolDirName, spoolSubDir)
}

// Write feeds the bounded in-memory capture first (the inline result path)
// and then mirrors the raw chunk to the spool file, creating it lazily on
// the first chunk that crosses spoolThreshold.
func (w *spoolOutputWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	if !w.failed {
		before := w.written
		if w.file == nil && before+int64(len(p)) >= spoolThreshold {
			w.activateLocked(before)
		}
		if w.file != nil {
			if _, err := w.file.Write(p); err != nil {
				w.degradeLocked("mirror write: " + err.Error())
			}
		}
		w.written = before + int64(len(p))
	}
	w.mu.Unlock()
	return w.bounded.Write(p)
}

// activateLocked creates the spool file and seeds it with the bytes the
// bounded writer has retained so far. Called with w.mu held, before p is
// handed to the bounded writer, and only while written < headCap, so the
// retained content is the exact prefix of the stream.
func (w *spoolOutputWriter) activateLocked(before int64) {
	if err := os.MkdirAll(w.dir, 0o755); err != nil {
		w.degradeLocked("mkdir: " + err.Error())
		return
	}
	pruneStaleSpoolFiles(w.dir)

	f, err := os.CreateTemp(w.dir, "run-*.log")
	if err != nil {
		w.degradeLocked("create: " + err.Error())
		return
	}
	if before > 0 {
		// Byte-exact seed: at this point the bounded writer has seen exactly
		// `before` bytes (< headCap), so String() holds the full prefix with
		// no overflow marker.
		if _, err := f.WriteString(w.bounded.String()); err != nil {
			f.Close()
			os.Remove(f.Name())
			w.degradeLocked("seed write: " + err.Error())
			return
		}
	}
	w.file = f
	w.path = f.Name()
}

// degradeLocked silently disables spooling and removes any partial file so
// a truncated artifact is never annotated as the full output.
func (w *spoolOutputWriter) degradeLocked(reason string) {
	w.failed = true
	if w.file != nil {
		w.file.Close()
		os.Remove(w.path)
		w.file = nil
	}
	w.path = ""
	debug.Log("tool", "output spool disabled: %s", reason)
}

// entry returns the spool annotation for this stream without closing the
// file (used while a background job is still mirroring into it).
func (w *spoolOutputWriter) entry(label string) spoolEntry {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.entryLocked(label)
}

// finishEntry closes the spool file (if any) and returns the annotation
// entry. Path is empty when spooling never activated or degraded.
func (w *spoolOutputWriter) finishEntry(label string) spoolEntry {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file != nil {
		w.file.Close()
		w.file = nil
	}
	return w.entryLocked(label)
}

// entryLocked stats the spool file for the annotation size. A failed stat
// yields size -1, which spoolNote renders without a byte count.
func (w *spoolOutputWriter) entryLocked(label string) spoolEntry {
	if w.path == "" {
		return spoolEntry{Label: label}
	}
	size := int64(-1)
	if info, err := os.Stat(w.path); err == nil {
		size = info.Size()
	}
	return spoolEntry{Label: label, Path: w.path, Size: size}
}

// spoolNote renders the agent-facing annotation appended to a truncated
// run_command result. Empty when nothing was spooled.
func spoolNote(entries ...spoolEntry) string {
	var b strings.Builder
	for _, e := range entries {
		if e.Path == "" {
			continue
		}
		if b.Len() == 0 {
			b.WriteString("\n[full output saved (not truncated): ")
		} else {
			b.WriteString("; ")
		}
		if e.Size >= 0 {
			b.WriteString(e.Label + ": " + e.Path + " (" + humanBytes(e.Size) + ")")
		} else {
			b.WriteString(e.Label + ": " + e.Path)
		}
	}
	if b.Len() == 0 {
		return ""
	}
	b.WriteString(" — use grep or read_file(offset/limit) on it instead of re-running the command]")
	return b.String()
}

// spoolEntry describes one spooled stream for spoolNote.
type spoolEntry struct {
	Label string // "stdout" / "stderr"
	Path  string
	Size  int64
}

// humanBytes renders a byte count compactly for the annotation line.
func humanBytes(n int64) string {
	switch {
	case n >= 1024*1024:
		return itoa64(n/(1024*1024)) + "." + itoa64((n%(1024*1024))*10/(1024*1024)) + "MB"
	case n >= 1024:
		return itoa64(n/1024) + "KB"
	default:
		return itoa64(n) + "B"
	}
}

func itoa64(n int64) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// pruneStaleSpoolFiles removes spool artifacts older than spoolMaxAge.
// Best-effort: any error is ignored; spooling must never fail because of
// housekeeping.
func pruneStaleSpoolFiles(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-spoolMaxAge)
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "run-") {
			continue
		}
		info, err := e.Info()
		if err != nil || info.ModTime().After(cutoff) {
			continue
		}
		os.Remove(filepath.Join(dir, e.Name()))
	}
}
