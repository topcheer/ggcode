package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	freshnessCheckInterval = 3

	freshnessMaxFiles = 8
)

type fileFreshnessSentinel struct {
	readMtimes map[string]time.Time

	writeMtimes map[string]time.Time

	notified map[string]bool

	lastCheckIter int
}

func newFileFreshnessSentinel() *fileFreshnessSentinel {
	return &fileFreshnessSentinel{
		readMtimes:  make(map[string]time.Time),
		writeMtimes: make(map[string]time.Time),
		notified:    make(map[string]bool),
	}
}

func (s *fileFreshnessSentinel) reset() {
	s.readMtimes = make(map[string]time.Time)
	s.writeMtimes = make(map[string]time.Time)
	s.notified = make(map[string]bool)
	s.lastCheckIter = 0
}

func (s *fileFreshnessSentinel) recordRead(path string) {
	if path == "" {
		return
	}
	n := normalizePath(path)
	info, err := os.Stat(path)
	if err != nil {
		s.readMtimes[n] = time.Now()
	} else {
		s.readMtimes[n] = info.ModTime()
	}

	delete(s.writeMtimes, n)

	delete(s.notified, n)
}

func (s *fileFreshnessSentinel) recordWrite(path string) {
	if path == "" {
		return
	}
	n := normalizePath(path)
	info, err := os.Stat(path)
	if err != nil {
		s.writeMtimes[n] = time.Now()
	} else {
		s.writeMtimes[n] = info.ModTime()
	}
}

func (s *fileFreshnessSentinel) maybeCheckStaleFiles(iteration int) string {

	if iteration > 1 && (iteration-s.lastCheckIter) < freshnessCheckInterval {
		return ""
	}
	s.lastCheckIter = iteration

	if len(s.readMtimes) == 0 {
		return ""
	}

	var changed []string
	for path, readAt := range s.readMtimes {

		if s.notified[path] {
			continue
		}

		info, err := os.Stat(path)
		if err != nil {
			continue
		}

		writeAt, ok := s.writeMtimes[path]
		if ok && info.ModTime().Equal(writeAt) || (ok && info.ModTime().Before(writeAt)) {
			continue
		}

		if info.ModTime().After(readAt) {
			changed = append(changed, path)
			s.notified[path] = true
		}
	}

	if len(changed) == 0 {
		return ""
	}

	shown := changed
	truncated := 0
	if len(shown) > freshnessMaxFiles {
		shown = shown[:freshnessMaxFiles]
		truncated = len(changed) - freshnessMaxFiles
	}

	var lines []string
	for _, p := range shown {
		short := shortenForDisplay(p)
		lines = append(lines, "  - "+short)
	}

	msg := fmt.Sprintf(
		"[file-watch] %d file(s) changed on disk since you last read them. "+
			"Your cached understanding may be stale -- re-read before editing:\n%s",
		len(changed), strings.Join(lines, "\n"),
	)
	if truncated > 0 {
		msg += fmt.Sprintf("\n  ... and %d more", truncated)
	}

	debug.Log("agent", "file freshness sentinel: %d externally modified files detected at iteration %d", len(changed), iteration)
	return msg
}

func shortenForDisplay(path string) string {

	parts := strings.Split(filepath.ToSlash(path), "/")
	if len(parts) <= 3 {
		return filepath.ToSlash(path)
	}

	return ".../" + strings.Join(parts[len(parts)-3:], "/")
}
