package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// tool_output_offload.go implements Tool Output Offloading — a 2026 harness
// engineering pattern (LangChain "Anatomy of an Agent Harness"; also used by
// Claude Code and Manus): when a large tool result must be truncated before
// entering context, the FULL output is written to a durable file and the
// truncation notice carries the path, so the agent can re-read the omitted
// middle section on demand instead of losing it forever.
//
// Before this change, guardToolOutput discarded the middle section
// permanently: a 200KB build log truncated to 40KB at 50% context fill lost
// ~160KB of content with no recovery path other than re-running the tool —
// which is wasteful for deterministic tools and impossible for one-shot
// side effects (flaky test runs, deploy logs, long CI output).
//
// Design constraints:
//   - Spilling is best-effort: any I/O failure returns "" and the agent falls
//     back to plain truncation. Never fail a tool result because of offloading.
//   - Spill files live under os.TempDir()/ggcode-spill-<pid>/ so user
//     repositories are never polluted and OS temp cleanup applies.
//   - File count is capped (keep newest); oversized content is capped too.
//   - The directory is created lazily on first spill; sessions that never
//     truncate pay zero filesystem cost.

const (
	// minSpillBytes: below this size the head+tail preservation already
	// captures most content and advisoryForTruncation is suppressed too —
	// keep both thresholds aligned.
	minSpillBytes = 8 * 1024

	// maxSpillBytes caps a single spilled file as a safety valve against
	// pathological outputs (e.g. a 2GB minified bundle). Beyond this the
	// head and tail are written with a marker in between.
	maxSpillBytes = 20 * 1024 * 1024

	// maxSpillFiles caps how many spill files accumulate per session;
	// oldest files are deleted when the cap is exceeded.
	maxSpillFiles = 20
)

// outputOffloader persists truncated tool outputs to disk for later
// re-reading by the agent. It is safe for concurrent use.
type outputOffloader struct {
	mu   sync.Mutex
	dir  string
	seq  int
	once sync.Once
}

// newOutputOffloader creates an offloader; the spill directory is created
// lazily on first use.
func newOutputOffloader() *outputOffloader {
	return &outputOffloader{}
}

// spillDir returns (creating if needed) the directory spill files live in.
func (o *outputOffloader) spillDir() (string, error) {
	if o.dir != "" {
		if _, err := os.Stat(o.dir); err == nil {
			return o.dir, nil
		} else if !os.IsNotExist(err) {
			return "", err
		}
		// Directory vanished (OS temp cleanup); recreate on next write.
		o.dir = ""
	}
	dir, err := os.MkdirTemp("", "ggcode-spill-*")
	if err != nil {
		return "", err
	}
	o.dir = dir
	return dir, nil
}

// spillToolNamePattern strips characters that are unsafe in file names.
var spillToolNamePattern = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

// spill writes the full original content to a timestamped file and returns
// its absolute path, or "" when spilling is skipped or fails.
// toolName is used for the file name only.
func (o *outputOffloader) spill(toolName, content string) string {
	if len(content) < minSpillBytes {
		return ""
	}

	o.mu.Lock()
	defer o.mu.Unlock()

	o.once.Do(func() {
		// Best-effort: prune stale spill dirs from previous crashed
		// sessions (older than 24h) once per session.
		pruneStaleSpillDirs()
	})

	dir, err := o.spillDir()
	if err != nil {
		return ""
	}

	o.seq++
	name := fmt.Sprintf("%s-%03d-%s.txt",
		time.Now().Format("150405.000"),
		o.seq,
		spillToolNamePattern.ReplaceAllString(strings.ToLower(toolName), "-"),
	)
	path := filepath.Join(dir, filepath.Base(name))

	if len(content) > maxSpillBytes {
		content = content[:utilSnapRune(content, maxSpillBytes)] +
			"\n... (spilled content capped at 20MB; original was " + formatBytes(len(content)) + ")"
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return ""
	}

	o.pruneLocked(dir)
	return path
}

// pruneLocked deletes the oldest spill files when the count cap is exceeded.
func (o *outputOffloader) pruneLocked(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) <= maxSpillFiles {
		return
	}
	type fileInfo struct {
		name string
		mod  time.Time
	}
	files := make([]fileInfo, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, fileInfo{e.Name(), info.ModTime()})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod.Before(files[j].mod) })
	excess := len(files) - maxSpillFiles
	for i := 0; i < excess; i++ {
		_ = os.Remove(filepath.Join(dir, files[i].name))
	}
}

// pruneStaleSpillDirs removes ggcode-spill-* directories under the system
// temp dir that are older than 24h (crashed sessions). Errors are ignored.
func pruneStaleSpillDirs() {
	tmp := os.TempDir()
	entries, err := os.ReadDir(tmp)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-24 * time.Hour)
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() || !strings.HasPrefix(name, "ggcode-spill-") {
			continue
		}
		info, err := e.Info()
		if err != nil || info.ModTime().After(cutoff) {
			continue
		}
		_ = os.RemoveAll(filepath.Join(tmp, name))
	}
}

// utilSnapRune snaps a byte offset to a UTF-8 rune boundary.
func utilSnapRune(s string, n int) int {
	if n >= len(s) {
		return len(s)
	}
	for n > 0 && n < len(s) && (s[n]&0xC0) == 0x80 {
		n--
	}
	return n
}

// spillNotice formats the agent-facing pointer to a spilled file. It is
// appended to the truncated content so the model knows the full output is
// recoverable from disk without re-running the tool.
func spillNotice(path string, originalLen int) string {
	return fmt.Sprintf(
		"\n[Full original output (%s) saved to: %s — use read_file with offset/limit or grep on that path to inspect the omitted middle section. Do not re-run the tool to recover it.]",
		formatBytes(originalLen), path,
	)
}
