package agent

// Orphaned Background Command Detection
//
// Research basis: Claude Code, Cursor, and Aider all track background processes
// (long-running dev servers, file watchers, test runners) and surface them to
// the user. However, none detect a common agent failure mode: the agent starts
// a background command (start_command) and then FORGETS to check its output in
// subsequent iterations. This leads to:
//   - Silent failures (dev server crashed, tests hung, build errored)
//   - Wasted iterations working on assumptions without verifying background output
//   - Orphaned processes consuming resources after the agent moves on
//   - Missed signals from streaming output (warnings, deprecations, errors)
//
// Competitor analysis:
//   - Claude Code: shows running processes in a side panel but doesn't nudge
//     the agent to check them
//   - Cursor: terminal output is visible but the AI agent has no background
//     process tracking
//   - OpenHands: tracks process lifecycle but doesn't detect "forgotten" ones
//   - Devin: has explicit process management UI but relies on user oversight
//   - Aider: single-process model, no background commands
//
// Gap: No deterministic system detects when the agent has started a background
// command but hasn't read its output for several iterations. This is especially
// critical in autopilot/long-running tasks where the agent starts a dev server
// or test watcher and then makes code changes without checking if the server
// is still running or tests are passing.
//
// Our approach: track start_command calls and their job IDs, then detect when
// read_command_output/wait_command hasn't been called for several iterations
// after the background command was started. Inject a targeted nudge reminding
// the agent to check the output. Zero LLM cost.
//
// Interaction with existing systems:
//   - run_command: synchronous, completes immediately -- NOT tracked here
//   - start_command: asynchronous, returns a job_id -- tracked as a bg command
//   - read_command_output/wait_command: checks background output -- clears the
//     "unchecked" state for that job
//   - stop_command: terminates a background job -- removes it from tracking

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
)

// bgOrphanState tracks background commands started via start_command that
// haven't had their output checked in subsequent iterations.
type bgOrphanState struct {
	mu sync.Mutex

	// activeJobs maps job_id → metadata about the background command
	activeJobs map[string]*bgJobInfo

	// currentIteration tracks the current iteration number for staleness calculation
	currentIteration int

	// warned tracks job IDs we've already warned about (avoid repeat warnings)
	warned map[string]bool

	// injectionCount tracks how many orphan warnings we've injected this run
	injectionCount int
}

// bgJobInfo holds metadata about a background command started by the agent.
type bgJobInfo struct {
	JobID           string // the job_id returned by start_command
	Description     string // the description from the command args
	Command         string // the actual command (truncated for display)
	StartIter       int    // iteration when the command was started
	LastCheckedIter int    // last iteration where output was read
}

const (
	// bgOrphanThreshold: minimum iterations between start_command and
	// read_command_output before we warn. Set to 2 so the agent has one
	// iteration to naturally check output before we intervene.
	bgOrphanThreshold = 2

	// bgOrphanMaxInjections: cap on how many orphan warnings per run to
	// avoid context flooding in autopilot sessions with many bg commands.
	bgOrphanMaxInjections = 3

	// bgOrphanMaxJobs: cap on tracked background jobs to prevent unbounded
	// memory growth in long-running sessions.
	bgOrphanMaxJobs = 20
)

// newBgOrphanState creates a fresh background orphan detector.
func newBgOrphanState() *bgOrphanState {
	return &bgOrphanState{
		activeJobs: make(map[string]*bgJobInfo),
		warned:     make(map[string]bool),
	}
}

// reset clears all state for a new run.
func (s *bgOrphanState) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.activeJobs = make(map[string]*bgJobInfo)
	s.warned = make(map[string]bool)
	s.currentIteration = 0
	s.injectionCount = 0
}

// recordStartCommand is called when the agent uses start_command. It parses
// the result to extract the job_id and registers the background command.
func (s *bgOrphanState) recordStartCommand(args json.RawMessage, result string, iteration int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	jobID := extractJobID(result)
	if jobID == "" {
		return
	}

	if len(s.activeJobs) >= bgOrphanMaxJobs {
		// Evict oldest entry if at capacity
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

// recordOutputCheck is called when the agent reads output from a background
// command (read_command_output, wait_command, write_command_input). It marks
// the job as checked.
func (s *bgOrphanState) recordOutputCheck(args json.RawMessage, iteration int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	jobID := extractJobIDFromArgs(args)
	if jobID == "" {
		return
	}
	if info, ok := s.activeJobs[jobID]; ok {
		info.LastCheckedIter = iteration
		// Clear any prior warning so we can re-warn if it goes stale again
		delete(s.warned, jobID)
	}
}

// recordStopCommand removes a job from tracking when stop_command is called.
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

// checkOrphanedCommands returns a guidance message if any background commands
// have been unchecked for too long, or empty string if all clear.
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

// extractJobID parses a start_command result to find the returned job_id.
// The result text typically contains "job_id": "<id>" or "Job ID: <id>".
func extractJobID(result string) string {
	// Try JSON-style "job_id": "xxx"
	if id := extractJSONBgField(result, "job_id"); id != "" {
		return id
	}
	// Try "Job ID: xxx" format
	if idx := strings.Index(strings.ToLower(result), "job id:"); idx >= 0 {
		rest := result[idx+7:]
		rest = strings.TrimSpace(rest)
		// Take first token/quoted string
		if strings.HasPrefix(rest, "\"") || strings.HasPrefix(rest, "'") {
			q := rest[0]
			end := strings.IndexByte(rest[1:], q)
			if end > 0 {
				return rest[1 : 1+end]
			}
		}
		// Unquoted: take up to whitespace or newline
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

// extractJobIDFromArgs parses tool arguments to find a job_id parameter.
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

// extractJSONStringField extracts a string field from a JSON-like result text.
func extractJSONBgField(text, field string) string {
	// Try full JSON parse first
	var m map[string]any
	if err := json.Unmarshal([]byte(text), &m); err == nil {
		if v, ok := m[field].(string); ok {
			return v
		}
	}
	// Fallback: regex-free string search for "field": "value"
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

// parseStartCommandArgs extracts the description and command from start_command args.
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

// truncateCmd shortens a command string for display.
func truncateBgCmd(s string, max int) string {
	s = strings.TrimSpace(s)
	// Strip leading comment line
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

// --- Agent integration methods ---

// recordBgToolCall processes a tool call/result pair for background command tracking.
// Called for every tool call in the agent loop.
func (a *Agent) recordBgToolCall(toolName string, args json.RawMessage, result string, iteration int) {
	if a.bgOrphan == nil {
		return
	}
	switch toolName {
	case "start_command":
		a.bgOrphan.recordStartCommand(args, result, iteration)
	case "read_command_output", "wait_command", "write_command_input":
		a.bgOrphan.recordOutputCheck(args, iteration)
	case "stop_command":
		a.bgOrphan.recordStopCommand(args)
	}
}

// maybeWarnBgOrphan returns a guidance message if any background commands
// need checking. Called once per iteration in the agent loop.
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

// resetBgOrphan clears state for a new run.
func (a *Agent) resetBgOrphan() {
	if a.bgOrphan != nil {
		a.bgOrphan.reset()
	}
}
