package agent

import (
	"fmt"
	"os"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	redundantReadMinSize = 2048
)

type redundantReadState struct {
	warnedFiles map[string]bool

	lastReadMtime map[string]int64

	lastReadHash map[string]uint64
}

func newRedundantReadState() *redundantReadState {
	return &redundantReadState{
		warnedFiles:   make(map[string]bool),
		lastReadMtime: make(map[string]int64),
		lastReadHash:  make(map[string]uint64),
	}
}

func (r *redundantReadState) reset() {
	r.warnedFiles = make(map[string]bool)
	r.lastReadMtime = make(map[string]int64)
	r.lastReadHash = make(map[string]uint64)
}

func (r *redundantReadState) checkRedundantRead(path string, partial bool) string {
	if path == "" {
		return ""
	}

	if partial {
		return ""
	}
	n := normalizePath(path)

	if r.warnedFiles[n] {
		return ""
	}

	prevMtime, hadPrior := r.lastReadMtime[n]
	if !hadPrior {

		r.recordReadMtime(path)
		return ""
	}

	info, err := os.Stat(path)
	if err != nil {

		return ""
	}

	if info.Size() < redundantReadMinSize {
		return ""
	}

	if info.ModTime().UnixNano() != prevMtime {
		r.recordReadMtime(path)
		return ""
	}

	if prevHash, ok := r.lastReadHash[n]; ok {
		if cur := hashFilePrefix(path); cur != 0 && cur != prevHash {

			r.recordReadMtime(path)
			return ""
		}
	}

	r.warnedFiles[n] = true
	debug.Log("agent", "redundant-read guard: %s re-read without changes (%d bytes already in context)", n, info.Size())

	sizeKB := info.Size() / 1024
	return fmt.Sprintf(
		"[context-hint] %s already read (%dKB in context). Use earlier read or grep for specific lines.",
		path, sizeKB,
	)
}

func (r *redundantReadState) recordReadMtime(path string) {
	if path == "" {
		return
	}
	info, err := os.Stat(path)
	if err != nil {
		return
	}
	n := normalizePath(path)
	r.lastReadMtime[n] = info.ModTime().UnixNano()

	if h := hashFilePrefix(path); h != 0 {
		r.lastReadHash[n] = h
	} else {
		delete(r.lastReadHash, n)
	}
}

func (r *redundantReadState) recordWrite(path string) {
	if path == "" {
		return
	}
	n := normalizePath(path)

	delete(r.lastReadMtime, n)

	delete(r.warnedFiles, n)
}
