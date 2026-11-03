package agent

import (
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

const editFailThreshold = 2

type editFailState struct {
	consecutiveFailures map[string]int

	guidanceFired map[string]bool
}

func newEditFailState() *editFailState {
	return &editFailState{
		consecutiveFailures: make(map[string]int),
		guidanceFired:       make(map[string]bool),
	}
}

func (s *editFailState) reset() {
	s.consecutiveFailures = make(map[string]int)
	s.guidanceFired = make(map[string]bool)
}

func (s *editFailState) recordEditSuccess(path string) {
	if path == "" {
		return
	}
	n := normalizePath(path)
	delete(s.consecutiveFailures, n)
}

func (s *editFailState) recordRead(path string) {
	if path == "" {
		return
	}
	n := normalizePath(path)
	delete(s.consecutiveFailures, n)
}

func (s *editFailState) recordEditFailure(path string) string {
	if path == "" {
		return ""
	}
	n := normalizePath(path)
	s.consecutiveFailures[n]++

	count := s.consecutiveFailures[n]
	if count < editFailThreshold {
		return ""
	}
	if s.guidanceFired[n] {
		return ""
	}
	s.guidanceFired[n] = true
	debug.Log("agent", "edit-fail-recovery: %d consecutive failures on %s, injecting guidance", count, n)
	return fmt.Sprintf(
		"This is your %dth consecutive failed edit to this file. "+
			"The file content likely differs from what you expect. "+
			"Before retrying: (1) re-read the file with read_file to see its current content, "+
			"(2) verify the text you are matching with old_text still exists, "+
			"(3) if the section was moved or renamed, use grep to locate it.",
		count,
	)
}

func extractFileForEdit(toolName string, args []byte) string {
	paths := extractEditFilePaths(toolName, args)
	if len(paths) == 0 {
		return ""
	}
	return paths[0]
}

func (s *editFailState) summarizeEditFailures() string {
	if len(s.consecutiveFailures) == 0 {
		return ""
	}
	var parts []string
	for path, count := range s.consecutiveFailures {
		if count > 0 {
			short := path
			if idx := strings.LastIndex(short, "/"); idx >= 0 {
				short = short[idx+1:]
			}
			parts = append(parts, fmt.Sprintf("%s(%d)", short, count))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, ", ")
}
