package agent

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
)

type bgOrphanState struct {
	mu sync.Mutex

	activeJobs map[string]*bgJobInfo

	currentIteration int

	warned map[string]bool

	injectionCount int
}

type bgJobInfo struct {
	JobID           string
	Description     string
	Command         string
	StartIter       int
	LastCheckedIter int
}

const (
	bgOrphanThreshold = 2

	bgOrphanMaxInjections = 3

	bgOrphanMaxJobs = 20
)

func newBgOrphanState() *bgOrphanState {
	return &bgOrphanState{
		activeJobs: make(map[string]*bgJobInfo),
		warned:     make(map[string]bool),
	}
}

func (s *bgOrphanState) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.activeJobs = make(map[string]*bgJobInfo)
	s.warned = make(map[string]bool)
	s.currentIteration = 0
	s.injectionCount = 0
}

func (s *bgOrphanState) recordStartCommand(args json.RawMessage, result string, iteration int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	jobID := extractJobID(result)
	if jobID == "" {
		return
	}

	if len(s.activeJobs) >= bgOrphanMaxJobs {
		var oldestID string
		var oldestIter int
		for id, info := range s.activeJobs {
			if oldestID == "" || info.StartIter < oldestIter {
				oldestID = id
				oldestIter = info.StartIter
			}
		}
		delete(s.activeJobs, oldestID)
	}

	desc, cmd := parseStartCommandArgs(args)
	s.activeJobs[jobID] = &bgJobInfo{
		JobID:           jobID,
		Description:     desc,
		Command:         cmd,
		StartIter:       iteration,
		LastCheckedIter: iteration,
	}
	debug.Log("bg-orphan", "tracking background command job=%s desc=%s at iteration %d", jobID, desc, iteration)
}

func (s *bgOrphanState) recordOutputCheck(args json.RawMessage, result string, iteration int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	jobID := extractJobIDFromArgs(args)
	if jobID == "" {
		return
	}

	if isCommandJobNotFoundResult(result) {
		delete(s.activeJobs, jobID)
		delete(s.warned, jobID)
		return
	}
	info, ok := s.activeJobs[jobID]
	if !ok {
		return
	}

	jobIDSeen := false
	for _, rawLine := range strings.Split(result, "\n") {
		line := strings.TrimSpace(rawLine)
		if strings.HasPrefix(line, "Job ID:") {
			jobIDSeen = true
			continue
		}
		if !strings.HasPrefix(line, "Status:") {
			continue
		}
		if !jobIDSeen {

			continue
		}
		status := strings.TrimSpace(strings.TrimPrefix(line, "Status:"))

		switch strings.ToLower(status) {
		case "completed", "failed", "cancelled", "timed_out":
			delete(s.activeJobs, jobID)
			delete(s.warned, jobID)
			return
		}
	}
	info.LastCheckedIter = iteration

	delete(s.warned, jobID)
}

func (s *bgOrphanState) recordStopCommand(args json.RawMessage) {
	s.mu.Lock()
	defer s.mu.Unlock()

	jobID := extractJobIDFromArgs(args)
	if jobID == "" {
		return
	}
	delete(s.activeJobs, jobID)
	delete(s.warned, jobID)
}

func (s *bgOrphanState) checkOrphanedCommands(iteration int) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.currentIteration = iteration

	if len(s.activeJobs) == 0 || s.injectionCount >= bgOrphanMaxInjections {
		return ""
	}

	var orphans []*bgJobInfo
	for _, info := range s.activeJobs {
		if s.warned[info.JobID] {
			continue
		}
		gap := iteration - info.LastCheckedIter
		if gap >= bgOrphanThreshold {
			orphans = append(orphans, info)
		}
	}

	if len(orphans) == 0 {
		return ""
	}

	s.injectionCount++
	for _, o := range orphans {
		s.warned[o.JobID] = true
	}

	var sb strings.Builder
	if len(orphans) == 1 {
		o := orphans[0]
		sb.WriteString(fmt.Sprintf(
			"[Background command check] You started a background command %d iterations ago "+
				"but haven't checked its output since. The process may have errored, hung, or produced "+
				"important output you're missing.\n"+
				"  Command: %s\n"+
				"  Use read_command_output (job_id: %s) or wait_command to check its status.",
			iteration-o.LastCheckedIter,
			truncateBgCmd(o.Command, 120),
			o.JobID,
		))
	} else {
		sb.WriteString(fmt.Sprintf(
			"[Background command check] You have %d background commands whose output hasn't been "+
				"checked recently. Processes may have errored or produced important output.\n",
			len(orphans),
		))
		for _, o := range orphans {
			sb.WriteString(fmt.Sprintf(
				"  - %s (job_id: %s, %d iterations unchecked)\n",
				truncateBgCmd(o.Command, 80),
				o.JobID,
				iteration-o.LastCheckedIter,
			))
		}
		sb.WriteString("Use read_command_output or wait_command to check each one.")
	}

	return sb.String()
}

func indexFoldASCII(s, needle string) int {
	if needle == "" {
		return 0
	}
	first := needle[0]
	lo, hi := lowerASCII(first), upperASCII(first)
	for i := 0; i+len(needle) <= len(s); i++ {
		c := s[i]
		if c != lo && c != hi {
			continue
		}
		if equalFoldASCII(s[i:i+len(needle)], needle) {
			return i
		}
	}
	return -1
}

func lowerASCII(c byte) byte {
	if c >= 'A' && c <= 'Z' {
		return c + 'a' - 'A'
	}
	return c
}

func upperASCII(c byte) byte {
	if c >= 'a' && c <= 'z' {
		return c - 'a' + 'A'
	}
	return c
}

func equalFoldASCII(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := lowerASCII(a[i]), lowerASCII(b[i])
		if ca != cb {
			return false
		}
	}
	return true
}

func extractJobID(result string) string {

	if id := extractJSONBgField(result, "job_id"); id != "" {
		return id
	}

	if idx := indexFoldASCII(result, "job id:"); idx >= 0 {
		rest := result[idx+7:]
		rest = strings.TrimSpace(rest)

		if strings.HasPrefix(rest, "\"") || strings.HasPrefix(rest, "'") {
			q := rest[0]
			end := strings.IndexByte(rest[1:], q)
			if end > 0 {
				return rest[1 : 1+end]
			}
		}

		for i, c := range rest {
			if c == ' ' || c == '\n' || c == '\t' || c == ',' {
				return rest[:i]
			}
		}
		if rest != "" {
			return rest
		}
	}
	return ""
}

func extractJobIDFromArgs(args json.RawMessage) string {
	if len(args) == 0 {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal(args, &m); err != nil {
		return ""
	}
	if id, ok := m["job_id"].(string); ok {
		return id
	}
	return ""
}

func extractJSONBgField(text, field string) string {
	var m map[string]any
	if err := json.Unmarshal([]byte(text), &m); err == nil {
		if v, ok := m[field].(string); ok {
			return v
		}
	}

	key := fmt.Sprintf(`"%s"`, field)
	idx := strings.Index(text, key)
	if idx < 0 {
		return ""
	}
	rest := text[idx+len(key):]
	rest = strings.TrimSpace(rest)
	if !strings.HasPrefix(rest, ":") {
		return ""
	}
	rest = strings.TrimSpace(rest[1:])
	if strings.HasPrefix(rest, "\"") {
		end := strings.IndexByte(rest[1:], '"')
		if end > 0 {
			return rest[1 : 1+end]
		}
	}
	return ""
}

func parseStartCommandArgs(args json.RawMessage) (desc, cmd string) {
	if len(args) == 0 {
		return "", ""
	}
	var m map[string]any
	if err := json.Unmarshal(args, &m); err != nil {
		return "", ""
	}
	if d, ok := m["description"].(string); ok {
		desc = d
	}
	if c, ok := m["command"].(string); ok {
		cmd = truncateBgCmd(c, 200)
	}
	return desc, cmd
}

func truncateBgCmd(s string, max int) string {
	s = strings.TrimSpace(s)

	if strings.HasPrefix(s, "# ") {
		if idx := strings.IndexByte(s, '\n'); idx >= 0 {
			s = strings.TrimSpace(s[idx+1:])
		}
	}
	if len(s) > max {
		return s[:max-3] + "..."
	}
	return s
}

func (a *Agent) recordBgToolCall(toolName string, args json.RawMessage, result string, iteration int) {
	if a.bgOrphan == nil {
		return
	}
	switch toolName {
	case "start_command":
		a.bgOrphan.recordStartCommand(args, result, iteration)
	case "read_command_output", "wait_command":

		a.bgOrphan.recordOutputCheck(args, result, iteration)
	case "stop_command":
		a.bgOrphan.recordStopCommand(args)
	}
}

func (a *Agent) maybeWarnBgOrphan(iteration int) string {
	if a.bgOrphan == nil {
		return ""
	}
	msg := a.bgOrphan.checkOrphanedCommands(iteration)
	if msg != "" {
		debug.Log("bg-orphan", "orphaned background command warning at iteration %d", iteration)
	}
	return msg
}

func (a *Agent) resetBgOrphan() {
	if a.bgOrphan != nil {
		a.bgOrphan.reset()
	}
}

func isCommandJobNotFoundResult(result string) bool {
	for _, line := range strings.Split(result, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Error: command job") && strings.Contains(line, "not found") {
			return true
		}
	}
	return false
}
