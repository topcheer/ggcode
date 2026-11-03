package agent

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	sgMaxSubgoals       = 8
	sgMinSubgoals       = 3
	sgMinIterGap        = 3
	sgUnaddressedThresh = 0.4
	sgMaxWarns          = 1
)

type subgoalEntry struct {
	number    int
	text      string
	keywords  []string
	addressed bool
}

type subgoalState struct {
	mu       sync.Mutex
	subgoals []subgoalEntry
	planIter int
	fired    bool
}

func newSubgoalState() *subgoalState {
	return &subgoalState{}
}

func (s *subgoalState) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.subgoals = nil
	s.planIter = 0
	s.fired = false
}

var numberedItemRe = regexp.MustCompile(`(?m)^\s*(\d+)[.)]\s+(.{3,80})$`)

func extractSubgoals(text string) []subgoalEntry {
	matches := numberedItemRe.FindAllStringSubmatch(text, sgMaxSubgoals+2)
	if len(matches) < sgMinSubgoals {
		return nil
	}
	var result []subgoalEntry
	for _, m := range matches {
		num := 0
		fmt.Sscanf(m[1], "%d", &num)
		desc := strings.TrimSpace(m[2])

		if len(desc) < 5 {
			continue
		}
		result = append(result, subgoalEntry{
			number:   num,
			text:     desc,
			keywords: extractSubgoalKeywords(desc),
		})
	}
	if len(result) < sgMinSubgoals {
		return nil
	}
	if len(result) > sgMaxSubgoals {
		result = result[:sgMaxSubgoals]
	}
	return result
}

var sgStopWords = map[string]bool{
	"the": true, "a": true, "an": true, "to": true, "of": true, "in": true,
	"for": true, "and": true, "or": true, "on": true, "at": true, "by": true,
	"is": true, "it": true, "this": true, "that": true, "with": true,
	"from": true, "be": true, "will": true, "was": true, "are": true,
	"as": true, "if": true, "we": true, "i": true, "you": true,
	"fix": true, "update": true, "add": true, "create": true, "check": true,
	"make": true, "set": true, "ensure": true, "do": true,
}

func extractSubgoalKeywords(desc string) []string {

	tokens := regexp.MustCompile(`[a-zA-Z][a-zA-Z0-9_-]{2,}`).FindAllString(desc, -1)
	var kws []string
	seen := make(map[string]bool)
	for _, t := range tokens {
		lower := strings.ToLower(t)
		if sgStopWords[lower] || seen[lower] {
			continue
		}
		seen[lower] = true
		kws = append(kws, lower)
	}
	return kws
}

func (s *subgoalState) recordAssistantText(text string, iter int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.subgoals) > 0 {
		return
	}
	subs := extractSubgoals(text)

	if len(subs) >= sgMinSubgoals && hasPlanIntent(text) {
		s.subgoals = subs
		s.planIter = iter
		debug.Log("agent", "subgoal_track: plan detected at iter %d with %d subgoals", iter, len(subs))
	}
}

var sgPlanIntentPhrases = []string{
	"plan", "steps", "going to", "will do", "todo", "to-do", "to do",
	"approach", "roadmap", "checklist", "road map", "next up", "my tasks",
	"任务", "计划", "步骤",
}

func hasPlanIntent(text string) bool {
	lower := strings.ToLower(text)
	for _, p := range sgPlanIntentPhrases {
		if sgKeywordPresent(lower, p) {
			return true
		}
	}
	return false
}

func (s *subgoalState) recordToolCall(toolName, args string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.subgoals) == 0 {
		return
	}
	argLower := strings.ToLower(args)
	for i := range s.subgoals {
		if s.subgoals[i].addressed {
			continue
		}
		for _, kw := range s.subgoals[i].keywords {

			if sgKeywordPresent(argLower, kw) {
				s.subgoals[i].addressed = true
				break
			}
		}
	}
}

func sgKeywordPresent(lower, kw string) bool {
	if kw == "" {
		return false
	}
	for from := 0; ; {
		rel := strings.Index(lower[from:], kw)
		if rel < 0 {
			return false
		}
		i := from + rel
		end := i + len(kw)
		if (i == 0 || !sgKeywordWordByte(lower[i-1])) && (end == len(lower) || !sgKeywordWordByte(lower[end])) {
			return true
		}
		from = end
	}
}

func sgKeywordWordByte(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= '0' && b <= '9')
}

func (s *subgoalState) maybeWarn(currentIter int) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fired || len(s.subgoals) < sgMinSubgoals {
		return ""
	}

	if currentIter-s.planIter < sgMinIterGap {
		return ""
	}
	unaddressed := 0
	trackable := 0
	var missing []string
	for _, sg := range s.subgoals {

		if len(sg.keywords) == 0 {
			continue
		}
		trackable++
		if !sg.addressed {
			unaddressed++
			missing = append(missing, fmt.Sprintf("  %d. %s", sg.number, truncate(sg.text, 60)))
		}
	}
	if trackable == 0 {
		return ""
	}
	ratio := float64(unaddressed) / float64(trackable)
	if ratio < sgUnaddressedThresh {
		return ""
	}
	s.fired = true
	debug.Log("agent", "subgoal_track: %d/%d subgoals unaddressed at iter %d", unaddressed, len(s.subgoals), currentIter)
	return fmt.Sprintf(
		"[Subgoal Completion] %d of %d planned subgoals have received no tool activity:\n%s\n"+
			"Research shows missing-step planning errors are a top agent failure cause (arXiv:2508.13143). "+
			"Review whether these subgoals are still needed. If so, address them before declaring completion. "+
			"If requirements changed, explicitly acknowledge which subgoals were intentionally deferred.",
		unaddressed, trackable, strings.Join(missing, "\n"))
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	if max < 3 {

		cut := max
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
		return s[:cut]
	}
	cut := max - 3
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "..."
}

func (s *subgoalState) markUndelivered() { s.fired = false }
