package agent

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	scopeDirThresholdSimple = 5

	scopeDirThresholdComplex = 10

	scopeFileThreshold = 25

	scopeWarnAfter = 6
)

type scopeDriftState struct {
	editedDirs map[string]bool

	editFiles map[string]bool

	productiveCount int

	fired bool
}

func newScopeDriftState() *scopeDriftState {
	return &scopeDriftState{
		editedDirs: make(map[string]bool),
		editFiles:  make(map[string]bool),
	}
}

func (s *scopeDriftState) reset() {
	s.editedDirs = make(map[string]bool)
	s.editFiles = make(map[string]bool)
	s.productiveCount = 0
	s.fired = false
}

func (s *scopeDriftState) recordEdit(filePath string) {
	if filePath == "" {
		return
	}

	fp := filepath.Clean(filePath)
	s.editFiles[fp] = true

	dir := filepath.Dir(fp)
	sig := dirSignature(dir)
	if sig != "" {
		s.editedDirs[sig] = true
	}
	s.productiveCount++
}

func dirSignature(dir string) string {

	wasAbs := strings.HasPrefix(dir, "/") || strings.HasPrefix(dir, "\\") || (len(dir) >= 2 && dir[1] == ':')
	dir = strings.TrimLeft(dir, "/\\")
	if len(dir) >= 2 && dir[1] == ':' {
		dir = dir[2:]
	}
	parts := strings.Split(dir, "/")

	clean := parts[:0]
	for _, p := range parts {
		if p != "" {
			clean = append(clean, p)
		}
	}
	if len(clean) <= 1 {
		return dir
	}
	if wasAbs && len(clean) > 2 {
		return clean[len(clean)-2] + "/" + clean[len(clean)-1]
	}
	return clean[0] + "/" + clean[1]
}

func (s *scopeDriftState) check(isComplex bool) string {
	if s.fired || s.productiveCount < scopeWarnAfter {
		return ""
	}

	dirThreshold := scopeDirThresholdSimple
	if isComplex {
		dirThreshold = scopeDirThresholdComplex
	}

	dirCount := len(s.editedDirs)
	fileCount := len(s.editFiles)

	if dirCount <= dirThreshold && fileCount <= scopeFileThreshold {
		return ""
	}

	s.fired = true

	dirs := make([]string, 0, len(s.editedDirs))
	for d := range s.editedDirs {
		dirs = append(dirs, d)
	}
	dirList := strings.Join(dirs, ", ")
	if len([]rune(dirList)) > 200 {
		dirList = string([]rune(dirList)[:197]) + "..."
	}

	debug.Log("scope_drift", "scope creep: %d dirs, %d files (threshold dirs=%d files=%d, complex=%v)",
		dirCount, fileCount, dirThreshold, scopeFileThreshold, isComplex)

	primaryMetric := "directories"
	primaryCount := dirCount
	primaryThreshold := dirThreshold
	if fileCount > scopeFileThreshold && fileCount >= dirCount {
		primaryMetric = "files"
		primaryCount = fileCount
		primaryThreshold = scopeFileThreshold
	}

	return fmt.Sprintf(
		"Scope check: You have modified %d %s across %d unique directories "+
			"(threshold: %d %s). This exceeds what the original task likely requires. "+
			"If you are doing legitimate multi-file work (e.g., a planned refactor), continue. "+
			"Otherwise, re-read the original request and focus on completing ONLY what was asked.\n"+
			"Directories touched: %s",
		primaryCount, primaryMetric, dirCount,
		primaryThreshold, primaryMetric,
		dirList,
	)
}

func (a *Agent) scopeDriftRecord(toolName string, fileHint string) {
	if a.scopeDrift == nil {
		return
	}
	if !productiveEditTools[toolName] {
		return
	}
	a.scopeDrift.recordEdit(fileHint)
}

func (a *Agent) scopeDriftCheck() string {
	if a.scopeDrift == nil {
		return ""
	}
	isComplex := false
	if a.planner != nil {
		a.planner.mu.Lock()
		isComplex = a.planner.isComplex
		a.planner.mu.Unlock()
	}
	return a.scopeDrift.check(isComplex)
}

func (a *Agent) resetScopeDrift() {
	if a.scopeDrift != nil {
		a.scopeDrift.reset()
	}
}

var productiveEditTools = sourceMutatingTools
