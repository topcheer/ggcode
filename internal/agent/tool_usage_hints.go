package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	usageHintsFileName   = "tool-usage-hints.json"
	usageHintsPerTool    = 5
	usageHintsMaxBytes   = 400
	usageHintsTTL        = 7 * 24 * time.Hour
	usageHintsMaxMsgLen  = 200
	usageHintsInjectMark = "\n[usage hints from past failures]"
)

type usageHint struct {
	Tool      string    `json:"tool"`
	Message   string    `json:"message"`
	LastSeen  time.Time `json:"last_seen"`
	FailCount int       `json:"fail_count"`
}

type toolUsageHintStore struct {
	mu   sync.Mutex
	path string

	loaded bool
	hints  map[string][]usageHint
}

func newToolUsageHintStore() *toolUsageHintStore {
	return &toolUsageHintStore{hints: map[string][]usageHint{}}
}

func (s *toolUsageHintStore) attach(dir string) {
	if s == nil || dir == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p := filepath.Join(dir, usageHintsFileName)
	if s.path == p && s.loaded {
		return
	}
	s.path = p
	s.loaded = true
	s.hints = map[string][]usageHint{}
	data, err := os.ReadFile(p)
	if err != nil {
		return
	}
	var all []usageHint
	if err := json.Unmarshal(data, &all); err != nil {
		debug.Log("agent", "[usage-hints] corrupt %s ignored: %v", usageHintsFileName, err)
		return
	}
	cutoff := time.Now().Add(-usageHintsTTL)
	for _, h := range all {
		if h.Tool == "" || h.Message == "" || h.LastSeen.Before(cutoff) {
			continue
		}
		s.hints[h.Tool] = append(s.hints[h.Tool], h)
	}
}

var usageHintParamSemanticsRe = regexp.MustCompile(`(?i)missing required|required parameter|must be|must not|expects?|invalid \w+|unknown parameter|unexpected parameter|type mismatch|expects? .+ (got|have)`)

var (
	usageHintPathRe    = regexp.MustCompile(`(?:/[^\s"']*)+`)
	usageHintNumRe     = regexp.MustCompile(`\b\d+(?:\.\d+)?\b`)
	usageHintQuotedRe  = regexp.MustCompile(`"[^"\n]*"|'[^'\n]*'|` + "`[^`\n]*`")
	usageHintParamName = regexp.MustCompile(`(?i)\b(?:parameter|param|field|argument|arg)\s+"?([a-z_][a-z0-9_.-]{1,40})"?`)
)

func distillUsageHint(errText string) string {
	if len(errText) == 0 || len(errText) > 4*1024 {
		return ""
	}
	if !usageHintParamSemanticsRe.MatchString(errText) {
		return ""
	}
	msg := strings.TrimSpace(errText)

	if i := strings.IndexAny(msg, "\n"); i > 0 {
		msg = msg[:i]
	}
	msg = usageHintQuotedRe.ReplaceAllString(msg, `"X"`)
	msg = usageHintPathRe.ReplaceAllString(msg, "<path>")
	msg = usageHintNumRe.ReplaceAllString(msg, "N")
	msg = strings.Join(strings.Fields(msg), " ")
	if len(msg) > usageHintsMaxMsgLen {
		msg = msg[:usageHintsMaxMsgLen]
	}
	if msg == "" {
		return ""
	}
	return msg
}

func (s *toolUsageHintStore) recordFailure(tool, errText string) {
	if s == nil || tool == "" {
		return
	}
	msg := distillUsageHint(errText)
	if msg == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	list := s.hints[tool]
	for i := range list {
		if list[i].Message == msg {
			list[i].FailCount++
			list[i].LastSeen = time.Now()
			s.hints[tool] = list
			s.persistLocked()
			return
		}
	}
	list = append(list, usageHint{Tool: tool, Message: msg, LastSeen: time.Now(), FailCount: 1})
	if len(list) > usageHintsPerTool {
		list = list[1:]
	}
	s.hints[tool] = list
	s.persistLocked()
}

func (s *toolUsageHintStore) recordSuccess(tool string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	list := s.hints[tool]
	if len(list) == 0 {
		return
	}
	kept := make([]usageHint, 0, len(list))
	for _, h := range list {
		h.FailCount--
		if h.FailCount > 0 {
			kept = append(kept, h)
		}
	}
	if len(kept) == 0 {
		delete(s.hints, tool)
	} else {
		s.hints[tool] = kept
	}
	s.persistLocked()
}

func (s *toolUsageHintStore) OverlayFor(tool string) string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	list := s.hints[tool]
	s.mu.Unlock()
	if len(list) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(usageHintsInjectMark)
	total := 0
	for _, h := range list {
		line := fmt.Sprintf("\n- %s", h.Message)
		if total+len(line) > usageHintsMaxBytes {
			break
		}
		b.WriteString(line)
		total += len(line)
	}
	if total == 0 {
		return ""
	}
	return b.String()
}

func (s *toolUsageHintStore) persistLocked() {
	if s.path == "" {
		return
	}
	all := make([]usageHint, 0, len(s.hints))
	for _, list := range s.hints {
		all = append(all, list...)
	}
	data, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		return
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		debug.Log("agent", "[usage-hints] persist failed: %v", err)
		return
	}
	if err := os.Rename(tmp, s.path); err != nil {
		debug.Log("agent", "[usage-hints] rename failed: %v", err)
	}
}
