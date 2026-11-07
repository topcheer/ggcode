package agent

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	propagationWarn1 = 4

	propagationWarn2 = 7

	propagationMaxWarnings = 2

	propagationMaxTracked = 60
)

type editPropagationState struct {
	mu             sync.Mutex
	distinctFiles  map[string]bool
	totalDistinct  int
	warningsIssued int
}

func newEditPropagationState() *editPropagationState {
	return &editPropagationState{
		distinctFiles: make(map[string]bool),
	}
}

func (s *editPropagationState) recordEdit(toolName, args string) bool {
	// #5254: apply_patch (Responses-native) edits propagate like the rest.
	if !mutatingSourceEdit(toolName) {
		return false
	}
	paths := propagationExtractPaths(args)
	if len(paths) == 0 {
		return false
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	added := false
	for _, fp := range paths {
		if fp == "" {
			continue
		}
		if !s.distinctFiles[fp] && len(s.distinctFiles) < propagationMaxTracked {
			s.distinctFiles[fp] = true
			added = true
		}
	}
	return added
}

func (s *editPropagationState) recordGreenBuild() {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := len(s.distinctFiles)
	if n > 0 {
		debug.Log("agent", "edit propagation reset: %d distinct files cleared on green build", n)
	}
	s.distinctFiles = make(map[string]bool)
}

func (s *editPropagationState) maybeWarn(iteration int) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.warningsIssued >= propagationMaxWarnings {
		return ""
	}

	count := len(s.distinctFiles)
	if count < propagationWarn1 {
		return ""
	}

	var msg string

	if count >= propagationWarn2 && s.warningsIssued <= propagationMaxWarnings-1 {
		s.warningsIssued = propagationMaxWarnings
		msg = fmt.Sprintf(
			"%s%d%s",
			"[Cross-file propagation risk: ", count,
			" distinct files edited since the last "+
				"successful build. Cross-file dependency chains create error propagation "+
				"paths -- a latent defect in any one file can cascade through imports to "+
				"all others. Research shows accuracy drops non-linearly with unverified "+
				"cross-file edits (MAST taxonomy, Cemri et al. 2025). Run a build+test NOW "+
				"to establish a verified baseline before the error surface grows further.]",
		)
	} else if s.warningsIssued == 0 {
		s.warningsIssued = 1
		msg = fmt.Sprintf(
			"%s%d%s",
			"[Cross-file propagation risk: ", count,
			" distinct files edited since the last "+
				"successful build. Edits spread across many files create cross-file "+
				"dependency propagation paths. Run a build+test to verify accumulated "+
				"changes before adding more files to the unverified set.]",
		)
	} else {
		return ""
	}

	debug.Log("agent", "edit propagation warning #%d: %d distinct files since green build (iter=%d)",
		s.warningsIssued, count, iteration)

	return msg
}

func (s *editPropagationState) reset() {
	s.mu.Lock()
	s.distinctFiles = make(map[string]bool)
	s.totalDistinct = 0
	s.warningsIssued = 0
	s.mu.Unlock()
}

func propagationExtractPaths(args string) []string {
	var paths []string

	paths = append(paths, propagationExtractField(args, "file_path")...)

	paths = append(paths, propagationExtractField(args, "path")...)

	paths = append(paths, propagationExtractFilesArray(args)...)

	return paths
}

func propagationExtractFilesArray(args string) []string {
	var parsed struct {
		Files []string `json:"files"`
	}
	if err := json.Unmarshal([]byte(args), &parsed); err != nil {
		return nil
	}
	return parsed.Files
}

func propagationExtractField(s, key string) []string {
	var values []string

	search := "\"" + key + "\":"
	idx := 0
	for {
		pos := strings.Index(s[idx:], search)
		if pos < 0 {
			break
		}
		pos += idx

		valStart := pos + len(search)

		for valStart < len(s) && (s[valStart] == ' ' || s[valStart] == '\t' || s[valStart] == '\n') {
			valStart++
		}

		if valStart >= len(s) || s[valStart] != '"' {
			idx = pos + len(search)
			continue
		}
		valStart++

		valEnd := valStart
		for valEnd < len(s) {
			if s[valEnd] == '\\' && valEnd+1 < len(s) {
				valEnd += 2
				continue
			}
			if s[valEnd] == '"' {
				break
			}
			valEnd++
		}
		if valEnd < len(s) {
			val := s[valStart:valEnd]
			val = strings.ReplaceAll(val, "\\/", "/")
			if val != "" {
				values = append(values, val)
			}
			idx = valEnd + 1
		} else {
			break
		}
	}
	return values
}
