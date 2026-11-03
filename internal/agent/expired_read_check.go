package agent

import (
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	maxExpiredReadWarnings = 3

	postEditRereadGap = 3
)

type expiredReadState struct {
	readBeforeEdit map[string]bool

	expiredWarned map[string]bool

	editedFiles map[string]int

	rereadWarned map[string]bool

	warningCount int

	seq int

	baseDir string
}

func (s *expiredReadState) normalize(path string) string {
	return normalizeCompanionPath(s.baseDir, strings.TrimSpace(path))
}

func newExpiredReadState() *expiredReadState {
	return &expiredReadState{
		readBeforeEdit: make(map[string]bool),
		expiredWarned:  make(map[string]bool),
		editedFiles:    make(map[string]int),
		rereadWarned:   make(map[string]bool),
	}
}

func (e *expiredReadState) reset() {
	e.readBeforeEdit = make(map[string]bool)
	e.expiredWarned = make(map[string]bool)
	e.editedFiles = make(map[string]int)
	e.rereadWarned = make(map[string]bool)
	e.warningCount = 0
	e.seq = 0
}

func (e *expiredReadState) recordRead(path string) {
	if path == "" {
		return
	}
	n := e.normalize(path)

	if _, edited := e.editedFiles[n]; !edited {
		e.readBeforeEdit[n] = true
	}
}

func (e *expiredReadState) recordUndo(path string) {
	if path == "" {
		return
	}
	n := e.normalize(path)
	delete(e.editedFiles, n)
	delete(e.readBeforeEdit, n)
}

func (e *expiredReadState) recordEdit(path string) string {
	if path == "" {
		return ""
	}
	n := e.normalize(path)

	e.seq++
	e.editedFiles[n] = e.seq

	if !e.readBeforeEdit[n] {
		return ""
	}

	if e.expiredWarned[n] {
		return ""
	}

	if e.warningCount >= maxExpiredReadWarnings {
		return ""
	}

	e.expiredWarned[n] = true
	e.warningCount++

	debug.Log("agent", "expired-read: %s was read earlier and is now edited (prior read is expired)", n)

	return fmt.Sprintf(
		"[expired-read] %s: your pre-edit read is stale; the edit result above contains the updated content - reference it instead of re-reading.",
		path,
	)
}

func (e *expiredReadState) checkPostEditReread(path string) string {
	if path == "" {
		return ""
	}
	n := e.normalize(path)

	editSeq, wasEdited := e.editedFiles[n]
	if !wasEdited {
		return ""
	}

	if e.rereadWarned[n] {
		return ""
	}

	e.seq++
	gap := e.seq - editSeq
	if gap > postEditRereadGap {
		return ""
	}

	if e.warningCount >= maxExpiredReadWarnings {
		return ""
	}

	e.rereadWarned[n] = true
	e.warningCount++

	debug.Log("agent", "expired-read: %s re-read %d ticks after edit (edit result has current content)", n, gap)

	return fmt.Sprintf(
		"[Context hint] %s was edited recently -- the edit result already contains the updated content. "+
			"Re-reading wastes context budget. Reference the edit result instead.",
		path,
	)
}
