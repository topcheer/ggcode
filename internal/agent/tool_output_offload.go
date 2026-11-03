package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	minSpillBytes = 8 * 1024

	maxSpillBytes = 20 * 1024 * 1024

	maxSpillFiles = 20
)

type outputOffloader struct {
	mu   sync.Mutex
	dir  string
	seq  int
	once sync.Once
}

func newOutputOffloader() *outputOffloader {
	return &outputOffloader{}
}

func (o *outputOffloader) spillDir() (string, error) {
	if o.dir != "" {
		if _, err := os.Stat(o.dir); err == nil {
			return o.dir, nil
		} else if !os.IsNotExist(err) {
			return "", err
		}

		o.dir = ""
	}

	dir, err := os.MkdirTemp("", fmt.Sprintf("ggcode-spill-%d-*", os.Getpid()))
	if err != nil {
		return "", err
	}
	o.dir = dir
	return dir, nil
}

var spillToolNamePattern = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

func (o *outputOffloader) spill(toolName, content string) string {
	if len(content) < minSpillBytes {
		return ""
	}

	o.mu.Lock()
	defer o.mu.Unlock()

	o.once.Do(func() {

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

func pruneStaleSpillDirs() {
	tmp := os.TempDir()
	entries, err := os.ReadDir(tmp)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-24 * time.Hour)
	legacyCutoff := time.Now().Add(-7 * 24 * time.Hour)
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() || !strings.HasPrefix(name, "ggcode-spill-") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if pid, ok := spillDirPID(name); ok {

			if processAlive(pid) {
				continue
			}
			if !info.ModTime().Before(cutoff) {
				continue
			}
		} else if !info.ModTime().Before(legacyCutoff) {

			continue
		}
		_ = os.RemoveAll(filepath.Join(tmp, name))
	}
}

func spillDirPID(name string) (int, bool) {
	rest := strings.TrimPrefix(name, "ggcode-spill-")
	pidStr := rest
	if i := strings.Index(rest, "-"); i >= 0 {
		pidStr = rest[:i]
	}
	pid, err := strconv.Atoi(pidStr)
	if err != nil || pid <= 0 {
		return 0, false
	}
	return pid, true
}

func utilSnapRune(s string, n int) int {
	if n >= len(s) {
		return len(s)
	}
	for n > 0 && n < len(s) && (s[n]&0xC0) == 0x80 {
		n--
	}
	return n
}

func spillNotice(path string, originalLen int) string {
	return fmt.Sprintf(
		"\n[Full original output (%s) saved to: %s — use read_file with offset/limit or grep on that path to inspect the omitted middle section. Do not re-run the tool to recover it.]",
		formatBytes(originalLen), path,
	)
}
