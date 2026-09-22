package watchmode

import (
	"bytes"
	"context"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	// maxScanFileBytes caps how much of a file is scanned for markers.
	maxScanFileBytes = 1 << 20 // 1 MiB
	// binaryProbeLen is how many leading bytes are checked for NUL to
	// treat a file as binary.
	binaryProbeLen = 8192
)

// fileState is the freshness record for one watched file.
type fileState struct {
	size    int64
	mtimeNs int64
}

// Scanner incrementally scans a project directory for new @ggcode markers.
// It is not safe for concurrent use; the watch loop drives it from one
// goroutine.
type Scanner struct {
	root  string
	state map[string]fileState
	seen  map[string]bool
}

// NewScanner returns a scanner rooted at dir.
func NewScanner(dir string) *Scanner {
	return &Scanner{
		root:  dir,
		state: make(map[string]fileState),
		seen:  make(map[string]bool),
	}
}

// PrimeBaseline performs one silent scan: every marker that already exists on
// disk is recorded as seen so the loop only dispatches annotations the user
// adds after watch mode started.
func (s *Scanner) PrimeBaseline(ctx context.Context) {
	markers, _ := s.scan(ctx)
	for _, m := range markers {
		s.seen[m.Key()] = true
	}
}

// Scan returns markers added since the last call (or since PrimeBaseline).
func (s *Scanner) Scan(ctx context.Context) ([]Marker, error) {
	return s.scan(ctx)
}

func (s *Scanner) scan(ctx context.Context) ([]Marker, error) {
	files, err := s.listFiles()
	if err != nil {
		return nil, err
	}
	var out []Marker
	for _, path := range files {
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		full := path
		if !filepath.IsAbs(full) {
			full = filepath.Join(s.root, path)
		}
		info, err := os.Stat(full)
		if err != nil || !info.Mode().IsRegular() {
			delete(s.state, path)
			continue
		}
		st := fileState{size: info.Size(), mtimeNs: info.ModTime().UnixNano()}
		if prev, ok := s.state[path]; ok && prev == st {
			continue // unchanged since last scan
		}
		s.state[path] = st
		if info.Size() > maxScanFileBytes {
			continue
		}
		data, err := os.ReadFile(full)
		if err != nil || isBinary(data) {
			continue
		}
		for _, m := range ExtractMarkersFromLines(path, strings.Split(string(data), "\n")) {
			if !s.seen[m.Key()] {
				s.seen[m.Key()] = true
				out = append(out, m)
			}
		}
	}
	return out, nil
}

// listFiles enumerates candidate text files. It prefers `git ls-files`
// (respects .gitignore and includes untracked-but-not-ignored files) and
// falls back to a directory walk that skips dot-directories when git is not
// available.
func (s *Scanner) listFiles() ([]string, error) {
	cmd := exec.Command("git", "-C", s.root, "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = nil
	if err := cmd.Run(); err == nil {
		var files []string
		for _, p := range strings.Split(buf.String(), "\x00") {
			if p != "" {
				files = append(files, p)
			}
		}
		return files, nil
	}
	// Fallback walk.
	var files []string
	walkErr := filepath.WalkDir(s.root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable entries are skipped, not fatal
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") && path != s.root {
				return filepath.SkipDir
			}
			return nil
		}
		rel, rerr := filepath.Rel(s.root, path)
		if rerr != nil {
			return nil
		}
		files = append(files, rel)
		return nil
	})
	return files, walkErr
}

func isBinary(data []byte) bool {
	n := len(data)
	if n > binaryProbeLen {
		n = binaryProbeLen
	}
	return bytes.IndexByte(data[:n], 0) >= 0
}
