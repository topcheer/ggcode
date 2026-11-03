package agent

import (
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
)

type goalDriftCtxState struct {
	mu sync.Mutex

	originKeywords map[string]bool
	initialized    bool

	recentTargets []string
	windowSize    int

	lastCheckIter int

	warned bool
}

const (
	goalDriftMinIter        = 12
	goalDriftWindowSize     = 8
	goalDriftMinKeywords    = 3
	goalDriftMaxOverlapWarn = 1
	goalDriftCheckInterval  = 4
	goalDriftMaxWarnsPerRun = 1
	goalDriftMinTargetLen   = 4
	goalDriftKeywordMinLen  = 4
	goalDriftStopWords      = true
)

func (s *goalDriftCtxState) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.initialized = false
	s.originKeywords = make(map[string]bool)
	s.recentTargets = nil
	s.warned = false
	s.lastCheckIter = 0
}

func newGoalDriftCtxState() *goalDriftCtxState {
	return &goalDriftCtxState{
		originKeywords: make(map[string]bool),
		windowSize:     goalDriftWindowSize,
	}
}

func (s *goalDriftCtxState) initFromUserMessage(msg string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.initialized || strings.TrimSpace(msg) == "" {
		return
	}
	s.initialized = true

	words := strings.Fields(msg)
	for _, word := range words {
		word = cleanToken(word)
		if len(word) < goalDriftKeywordMinLen {
			continue
		}
		if isStopWord(word) || goalDriftIsStopWord(word) {
			continue
		}
		word = strings.ToLower(word)
		s.originKeywords[word] = true
	}

	for _, pathField := range strings.Fields(msg) {
		pathField = strings.Trim(pathField, "\"'`,;()[]{}")
		if strings.Contains(pathField, "/") || strings.Contains(pathField, ".") {
			parts := strings.FieldsFunc(pathField, func(r rune) bool {
				return r == '/' || r == '.' || r == '_' || r == '-'
			})
			for _, part := range parts {
				part = strings.ToLower(part)
				if len(part) >= goalDriftKeywordMinLen && !goalDriftIsStopWord(part) {
					s.originKeywords[part] = true
				}
			}
		}
	}

	debug.Log("agent", "goal drift ctx: extracted %d keywords from user message", len(s.originKeywords))
}

func (s *goalDriftCtxState) recordToolCall(toolName, rawArgs string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	target := extractTarget(toolName, []byte(rawArgs))
	if len(target) < goalDriftMinTargetLen {
		return
	}

	s.recentTargets = append(s.recentTargets, strings.ToLower(target))
	if len(s.recentTargets) > s.windowSize {
		s.recentTargets = s.recentTargets[len(s.recentTargets)-s.windowSize:]
	}
}

func (s *goalDriftCtxState) checkDrift(iter int) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	if iter < goalDriftMinIter {
		return ""
	}

	if iter-s.lastCheckIter < goalDriftCheckInterval && s.lastCheckIter > 0 {
		return ""
	}
	s.lastCheckIter = iter

	if s.warned {
		return ""
	}

	if len(s.originKeywords) < goalDriftMinKeywords {
		return ""
	}

	if len(s.recentTargets) < goalDriftWindowSize/2 {
		return ""
	}

	matches := 0
	for _, recentTarget := range s.recentTargets {
		for kw := range s.originKeywords {
			if strings.Contains(recentTarget, kw) {
				matches++
				break
			}
		}
	}

	if matches <= goalDriftMaxOverlapWarn {
		s.warned = true
		debug.Log("agent", "goal drift ctx: drift detected at iter %d (%d/%d recent targets match origin keywords)", iter, matches, len(s.recentTargets))
		return "[goal-drift] Recent tool calls target files unrelated to original request. Re-read original request and verify alignment."
	}

	return ""
}

func extractTarget(toolName string, rawArgs []byte) string {

	switch toolName {
	case "read_file", "edit_file", "write_file", "multi_edit_file",
		"lsp_definition", "lsp_references", "lsp_hover", "lsp_symbols",
		"lsp_diagnostics", "lsp_rename", "lsp_implementation":
		return extractJSONStringField(rawArgs, "path") +
			extractJSONStringField(rawArgs, "file_path")
	case "grep", "search_files":
		return extractJSONStringField(rawArgs, "pattern") +
			extractJSONStringField(rawArgs, "query")
	case "glob":
		return extractJSONStringField(rawArgs, "pattern")
	case "run_command", "start_command":
		return extractJSONStringField(rawArgs, "command")
	case "code_search":
		return extractJSONStringField(rawArgs, "query")
	case "lsp_workspace_symbols":
		return extractJSONStringField(rawArgs, "query")
	default:
		return string(rawArgs)
	}
}

func cleanToken(s string) string {
	s = strings.TrimFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && !(r >= 'A' && r <= 'Z') && !(r >= '0' && r <= '9') && r != '_' && r != '-' && r != '.' && r != '/'
	})
	return s
}

var goalDriftStopWordSet = map[string]bool{
	"that": true, "this": true, "with": true, "from": true, "have": true,
	"your": true, "you": true, "the": true, "for": true, "are": true,
	"was": true, "but": true, "not": true, "and": true, "can": true,
	"all": true, "will": true, "they": true, "them": true, "then": true,
	"than": true, "been": true, "were": true, "into": true, "over": true,
	"when": true, "what": true, "which": true, "their": true, "there": true,
	"about": true, "would": true, "could": true, "should": true, "these": true,
	"those": true, "need": true, "make": true, "more": true, "such": true,
	"only": true, "some": true, "very": true, "just": true, "also": true,
	"like": true, "want": true, "does": true, "done": true, "task": true,
	"work": true, "help": true, "using": true, "based": true, "please": true,
	"following": true, "before": true, "after": true,
}

func goalDriftIsStopWord(w string) bool {
	if !goalDriftStopWords {
		return false
	}
	return goalDriftStopWordSet[strings.ToLower(w)]
}
