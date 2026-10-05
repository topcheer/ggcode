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

// tool_usage_hints.go (r35): JTPRO-style runtime tool-prompt co-optimization,
// online and deterministic. JTPRO (arXiv:2604.19821, ACL 2026 Findings)
// shows per-tool schema/argument description refinement from failure
// rollouts lifts overall success rate 5-20% relative - and that optimizing
// tool docs beats optimizing instructions alone. ggcode's near neighbours
// all act on different objects (r118 repairs JSON syntax once per call;
// tool_effectiveness keeps run-local boolean stats; run_command's
// diagnose hints are a static table for one tool). What is missing - and
// what this file adds - is the closed loop: distill parameter-usage
// failures into per-tool hints, persist them across sessions, and inject
// them into the tool description the LLM sees, so the model stops
// repeating the same argument mistakes.
//
// Design constraints (deterministic, no LLM rewrites - a self-modifying
// tool schema is a prompt-injection surface):
//   - only errors with parameter/schema semantics are distilled
//   - messages are normalized to templates (paths, numbers, quoted
//     values stripped) so hints generalize instead of memorizing
//   - success decays: a hint dies after its tool succeeds, so stale
//     hints never pollute a fixed workflow
//   - capped: <=5 hints/tool, <=400 bytes/tool, 7-day TTL
//   - stored at <project>/.ggcode/tool-usage-hints.json; corrupt file
//     degrades to empty (invariants.go precedent), never bricks a run

const (
	usageHintsFileName   = "tool-usage-hints.json"
	usageHintsPerTool    = 5
	usageHintsMaxBytes   = 400
	usageHintsTTL        = 7 * 24 * time.Hour
	usageHintsMaxMsgLen  = 200 // per-hint message cap after normalization
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
	path string // "" = inert (no working dir yet)

	loaded bool
	hints  map[string][]usageHint // tool -> newest-last
}

func newToolUsageHintStore() *toolUsageHintStore {
	return &toolUsageHintStore{hints: map[string][]usageHint{}}
}

// attach binds the store to <dir>/tool-usage-hints.json and lazily loads it.
// Called on first use from the agent loop (workingDir is not known at
// construction time). Safe to call repeatedly.
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
		return // absent = start clean
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

// usageHintParamSemanticsRe identifies parameter-usage failures worth
// distilling. Deliberately narrow: infrastructure errors (network, OOM,
// timeouts) say nothing about HOW to call the tool and would only bloat
// descriptions.
var usageHintParamSemanticsRe = regexp.MustCompile(`(?i)missing required|required parameter|must be|must not|expects?|invalid \w+|unknown parameter|unexpected parameter|type mismatch|expects? .+ (got|have)`)

var (
	usageHintPathRe    = regexp.MustCompile(`(?:/[^\s"']*)+`)
	usageHintNumRe     = regexp.MustCompile(`\b\d+(?:\.\d+)?\b`)
	usageHintQuotedRe  = regexp.MustCompile(`"[^"\n]*"|'[^'\n]*'|` + "`[^`\n]*`")
	usageHintParamName = regexp.MustCompile(`(?i)\b(?:parameter|param|field|argument|arg)\s+"?([a-z_][a-z0-9_.-]{1,40})"?`)
)

// distillUsageHint normalizes a parameter-semantics error into a reusable
// template. Returns "" when the error carries no usage signal.
func distillUsageHint(errText string) string {
	if len(errText) == 0 || len(errText) > 4*1024 {
		return ""
	}
	if !usageHintParamSemanticsRe.MatchString(errText) {
		return ""
	}
	msg := strings.TrimSpace(errText)
	// Keep the FIRST line - later lines are often stack traces or hints
	// from other subsystems that do not generalize.
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

// recordFailure distills a failed tool result. Errors that fail the
// semantics filter are ignored; a repeated identical hint bumps its
// fail count and timestamp instead of duplicating.
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
		list = list[1:] // ring: drop the oldest
	}
	s.hints[tool] = list
	s.persistLocked()
}

// recordSuccess decays: once the tool works, its hints are stale and
// would only bloat future descriptions (description-budget lesson).
func (s *toolUsageHintStore) recordSuccess(tool string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.hints[tool]; !ok {
		return
	}
	delete(s.hints, tool)
	s.persistLocked()
}

// OverlayFor returns the hint block appended to a tool description, or ""
// when the tool has no live hints. Byte-capped.
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

// persistLocked writes the store atomically; failure is logged and
// non-fatal (hints are advisory).
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
