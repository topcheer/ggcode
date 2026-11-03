package agent

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	overseerInterval      = 12
	spamThreshold         = 6
	stallThreshold        = 15
	fileStuckThreshold    = 4
	driftThreshold        = 20
	errorEscalationFactor = 2.0

	researchStallThreshold = 40
	researchSpamThreshold  = 15
	researchDriftThreshold = 50

	fileStuckResearchThreshold = 8
)

type overseerState struct {
	mu sync.Mutex

	trajectory []trajectoryEntry

	fileReadsSinceEdit map[string]int

	itersSinceProductive int
	lastUnitIter         int

	fired map[string]bool

	driftLevel int

	lastAnalysisIter int

	researchFindings []ResearchFinding

	researchMode bool

	searchCalls     int
	fetchCalls      int
	reportGateFired bool
}

type trajectoryEntry struct {
	toolName   string
	isError    bool
	fileHint   string
	productive bool
	iter       int
}

var productiveTools = map[string]bool{
	"edit_file":           true,
	"write_file":          true,
	"multi_edit_file":     true,
	"multi_file_edit":     true,
	"run_command":         true,
	"start_command":       true,
	"git_commit":          true,
	"git_add":             true,
	"notebook_edit":       true,
	"enter_worktree":      true,
	"write_command_input": true,
}

var researchProductiveTools = map[string]bool{
	"web_search":            true,
	"web_fetch":             true,
	"code_search":           true,
	"code_execution":        true,
	"lsp_symbols":           true,
	"lsp_workspace_symbols": true,
}

var readOnlyTools = map[string]bool{
	"read_file":               true,
	"multi_file_read":         true,
	"list_directory":          true,
	"search_files":            true,
	"glob":                    true,
	"grep":                    true,
	"web_fetch":               true,
	"web_search":              true,
	"git_status":              true,
	"git_diff":                true,
	"git_log":                 true,
	"git_blame":               true,
	"git_show":                true,
	"git_branch_list":         true,
	"git_remote":              true,
	"lsp_definition":          true,
	"lsp_references":          true,
	"lsp_hover":               true,
	"lsp_symbols":             true,
	"lsp_diagnostics":         true,
	"lsp_document_highlights": true,

	"code_search":                true,
	"git_stash_list":             true,
	"lsp_workspace_symbols":      true,
	"lsp_implementation":         true,
	"lsp_code_actions":           true,
	"lsp_prepare_call_hierarchy": true,
	"lsp_incoming_calls":         true,
	"lsp_outgoing_calls":         true,
	"dep_graph":                  true,
	"scan_todos":                 true,
	"code_health":                true,
}

func newOverseerState() *overseerState {

	return &overseerState{
		fileReadsSinceEdit: make(map[string]int),
		fired:              make(map[string]bool),
		lastUnitIter:       -1,
	}
}

var fixIntentRe = regexp.MustCompile(
	`(?:^|[.!?:\n]\s+)(?:please\s+|now\s+|first\s+|go\s+)?(?:fix|repair|patch|resolve|implement)\b` +
		`|(?:,\s*|\b(?:and|then)\s+)(?:please\s+)?(?:then\s+)?(?:fix|repair|patch|resolve|implement)\b(?:\s+(?:it|them|that|this|the|a|an|our|my|all)\b|$)`)

var fixIntentStripRe = regexp.MustCompile(`\b(?:fix|repair|patch|resolve|implement)[_-][a-z0-9]+(?:-[a-z0-9]+)*`)

func stripFixIntentTokens(lower string) string {
	return fixIntentStripRe.ReplaceAllString(lower, "ref")
}

func detectResearchMode(taskText string) bool {
	p := strings.ToLower(taskText)
	if fixIntentRe.MatchString(stripFixIntentTokens(p)) {
		return false
	}
	keywords := []string{
		"research", "analyze", "analysis", "audit", "investigate",
		"explore", "evaluate", "assess", "examine", "compare",
		"benchmark", "survey", "study", "gap analysis", "trend",
		"review", "inspect", "identify",
	}
	for _, kw := range keywords {
		if strings.Contains(p, kw) {
			return true
		}
	}
	return false
}

func (o *overseerState) effectiveStallThreshold() int {
	if o.researchMode {
		return researchStallThreshold
	}
	return stallThreshold
}

func (o *overseerState) effectiveSpamThreshold() int {
	if o.researchMode {
		return researchSpamThreshold
	}
	return spamThreshold
}

func (o *overseerState) effectiveDriftThreshold() int {
	if o.researchMode {
		return researchDriftThreshold
	}
	return driftThreshold
}

func (o *overseerState) recordToolCall(toolName string, isError bool, fileHint, resultContent string, iteration int) {
	o.mu.Lock()
	defer o.mu.Unlock()

	newIteration := iteration != o.lastUnitIter
	o.lastUnitIter = iteration

	isProductive := productiveTools[toolName]
	if o.researchMode && !isProductive {
		isProductive = researchProductiveTools[toolName]
	}
	isProductive = isProductive && (!isError || len(extractWrittenPaths(resultContent)) > 0)

	o.trajectory = append(o.trajectory, trajectoryEntry{
		toolName:   toolName,
		isError:    isError,
		fileHint:   fileHint,
		productive: isProductive,
		iter:       iteration,
	})

	if !isError {
		switch toolName {
		case "web_search":
			o.searchCalls++
		case "web_fetch":
			o.fetchCalls++
		}
	}

	if isProductive {
		o.itersSinceProductive = 0

		o.fileReadsSinceEdit = make(map[string]int)

		o.driftLevel = 0
	} else if newIteration {
		o.itersSinceProductive++
	}

	if toolName == "read_file" || toolName == "multi_file_read" {
		if fileHint != "" {
			o.fileReadsSinceEdit[fileHint]++
		}
	}
}

func (o *overseerState) reset() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.trajectory = nil
	o.fileReadsSinceEdit = make(map[string]int)
	o.itersSinceProductive = 0
	o.lastUnitIter = -1
	o.driftLevel = 0
	o.fired = make(map[string]bool)
	o.lastAnalysisIter = 0

	o.searchCalls = 0
	o.fetchCalls = 0
	o.reportGateFired = false
}

func (o *overseerState) analyze(iteration int) string {
	o.mu.Lock()
	defer o.mu.Unlock()

	if len(o.trajectory) < overseerInterval {
		return ""
	}
	if iteration-o.lastAnalysisIter < overseerInterval {
		return ""
	}
	o.lastAnalysisIter = iteration

	traj := make([]trajectoryEntry, len(o.trajectory))
	copy(traj, o.trajectory)

	if msg := o.checkReadOnlyStall(traj); msg != "" {
		return msg
	}
	if msg := o.checkToolSpam(traj); msg != "" {
		return msg
	}
	if msg := o.checkFileStuck(traj); msg != "" {
		return msg
	}
	if msg := o.checkErrorEscalation(traj); msg != "" {
		return msg
	}
	if msg := o.checkDrift(traj); msg != "" {
		return msg
	}

	return ""
}

func (o *overseerState) checkToolSpam(traj []trajectoryEntry) string {
	if o.fired["spam"] {
		return ""
	}
	threshold := o.effectiveSpamThreshold()

	hasProductive := false
	for _, e := range traj {
		if e.productive {
			hasProductive = true
			break
		}
	}
	if hasProductive {
		return ""
	}
	counts := make(map[string]int)
	for _, e := range traj {
		counts[e.toolName]++
	}
	for tool, count := range counts {
		if count > threshold {
			o.fired["spam"] = true
			debug.Log("overseer", "tool spam detected: %s called %d times without productive action (research=%t)", tool, count, o.researchMode)
			if o.researchMode {
				return fmt.Sprintf(
					"Overseer: You have called %s %d times. While research involves extensive "+
						"reading, you may be over-searching the same resource. Consider:\n"+
						"1. Summarize your key findings so far\n"+
						"2. Move to a different area of the codebase or a different research angle\n"+
						"3. If you have enough information, start forming your analysis or plan",
					tool, count,
				)
			}
			return fmt.Sprintf(
				"Overseer: You have called %s %d times without making progress. "+
					"You may be stuck exploring without acting. Consider:\n"+
					"1. Summarize what you've learned so far\n"+
					"2. Make a concrete edit or run a command to move forward\n"+
					"3. If you need more context, batch your reads instead of one-by-one",
				tool, count,
			)
		}
	}
	return ""
}

func (o *overseerState) checkReadOnlyStall(traj []trajectoryEntry) string {
	if o.fired["stall"] {
		return ""
	}
	threshold := o.effectiveStallThreshold()

	distinct := 0
	last := -1
	start := 0
	for i := len(traj) - 1; i >= 0; i-- {
		if traj[i].iter != last {
			distinct++
			last = traj[i].iter
		}
		if distinct > threshold {
			start = i + 1
			break
		}
	}
	if distinct < threshold {
		return ""
	}
	recent := traj[start:]
	allReadOnly := true
	for _, e := range recent {
		if !readOnlyTools[e.toolName] {
			allReadOnly = false
			break
		}
	}
	if allReadOnly {
		o.fired["stall"] = true
		debug.Log("overseer", "read-only stall: %d consecutive read-only iterations (research=%t)", threshold, o.researchMode)
		if o.researchMode {
			return fmt.Sprintf(
				"Overseer: You have spent %d iterations reading and searching. You have done "+
					"thorough research. Consider transitioning to the next phase:\n"+
					"1. Summarize your key findings and insights\n"+
					"2. Formulate concrete recommendations or a plan\n"+
					"3. If implementation is needed, start with the highest-priority item",
				threshold,
			)
		}
		return fmt.Sprintf(
			"Overseer: You have spent %d iterations only reading and searching without "+
				"making any changes. You have enough context to act. Start implementing your "+
				"solution — make edits, run builds, or execute commands to move the task forward.",
			threshold,
		)
	}
	return ""
}

func (o *overseerState) checkFileStuck(traj []trajectoryEntry) string {
	if o.fired["file_stuck"] {
		return ""
	}
	threshold := fileStuckThreshold
	if o.researchMode {
		threshold = fileStuckResearchThreshold
	}
	for file, count := range o.fileReadsSinceEdit {
		if count >= threshold {
			o.fired["file_stuck"] = true
			debug.Log("overseer", "file stuck: %s read %d times without edit", file, count)
			return fmt.Sprintf(
				"Overseer: You have read %s %d times without editing it. "+
					"If you're unsure what to change, try:\n"+
					"1. Search for the specific function or symbol you need to modify\n"+
					"2. Look at how similar patterns are handled elsewhere in the codebase\n"+
					"3. Make a small experimental edit and build to test your understanding",
				file, count,
			)
		}
	}
	return ""
}

func (o *overseerState) checkErrorEscalation(traj []trajectoryEntry) string {
	if o.fired["error_escalation"] {
		return ""
	}
	mid := len(traj) / 2
	if mid < 5 {
		return ""
	}
	firstHalf := traj[:mid]
	recentHalf := traj[mid:]

	earlyErrors := float64(countErrors(firstHalf)) / float64(len(firstHalf))
	recentErrors := float64(countErrors(recentHalf)) / float64(len(recentHalf))

	if earlyErrors == 0 {
		earlyErrors = 0.01
	}

	if recentErrors > earlyErrors*errorEscalationFactor && recentErrors > 0.3 {
		o.fired["error_escalation"] = true
		debug.Log("overseer", "error escalation: early=%.1f%% recent=%.1f%%", earlyErrors*100, recentErrors*100)
		return fmt.Sprintf(
			"Overseer: Your error rate is increasing (early: %.0f%%, recent: %.0f%%). "+
				"Your approach may be fundamentally wrong. Consider:\n"+
				"1. Re-read the original task requirements carefully\n"+
				"2. Check if you're modifying the right files\n"+
				"3. Consider a completely different strategy\n"+
				"4. If stuck, use ask_user to clarify requirements",
			earlyErrors*100, recentErrors*100,
		)
	}
	return ""
}

func (o *overseerState) checkDrift(traj []trajectoryEntry) string {
	base := o.effectiveDriftThreshold()
	var targetLevel int
	switch {
	case o.itersSinceProductive >= base*3:
		targetLevel = 3
	case o.itersSinceProductive >= base*2:
		targetLevel = 2
	case o.itersSinceProductive >= base:
		targetLevel = 1
	default:
		return ""
	}

	if o.driftLevel >= targetLevel {
		return ""
	}
	o.driftLevel = targetLevel
	debug.Log("overseer", "drift level %d: %d iterations since last productive action",
		targetLevel, o.itersSinceProductive)

	switch targetLevel {
	case 1:
		if o.researchMode {
			return fmt.Sprintf(
				"Overseer: %d iterations since your last concrete output. While research "+
					"involves extensive reading, consider producing an intermediate artifact:\n"+
					"1. Summarize your key findings so far\n"+
					"2. Identify the most important gap or insight\n"+
					"3. Decide whether to continue researching or move to implementation",
				o.itersSinceProductive,
			)
		}
		return fmt.Sprintf(
			"Overseer: %d iterations since you last made a productive change (edit, command, commit). "+
				"You may be drifting from the original task. Re-anchor:\n"+
				"1. State your current goal in one sentence\n"+
				"2. Identify the single next concrete action\n"+
				"3. Execute it immediately — stop researching and start doing",
			o.itersSinceProductive,
		)
	case 2:
		return fmt.Sprintf(
			"Overseer: %d iterations without productive changes. This is a significant stall.\n"+
				"1. Summarize what you've accomplished so far in 2-3 sentences (this helps if context compaction occurs)\n"+
				"2. Identify the specific blocker preventing progress\n"+
				"3. If stuck on the same problem, try a completely different approach\n"+
				"4. Continue working — try the next subtask if the current one is blocked",
			o.itersSinceProductive,
		)
	default:
		return fmt.Sprintf(
			"Overseer: %d iterations without productive changes — critical stall detected.\n"+
				"Recommended actions:\n"+
				"- Try a fundamentally different approach to the current subtask\n"+
				"- Skip the current subtask and move to the next one in your plan\n"+
				"- Use ask_user only if you are truly blocked by missing information\n"+
				"Do NOT stop or wrap up — continue making progress on your overall task.",
			o.itersSinceProductive,
		)
	}
}

func (a *Agent) overseerCheck(toolName string, isError bool, fileHint, resultContent string, iteration int) string {
	if a.overseer == nil {
		return ""
	}
	a.overseer.recordToolCall(toolName, isError, fileHint, resultContent, iteration)
	return a.overseer.analyze(iteration)
}

func (a *Agent) resetOverseer(runTask string) {
	if a.overseer == nil {
		return
	}

	taskText := a.getAutopilotGoal()
	if taskText == "" {
		taskText = runTask
	}
	if taskText == "" {

		msgs := a.Messages()
		for i := len(msgs) - 1; i >= 0 && taskText == ""; i-- {
			if msgs[i].Role != "user" {
				continue
			}
			for _, block := range msgs[i].Content {
				if block.Type == "text" {
					taskText = block.Text
					break
				}
			}
		}
	}
	a.overseer.researchMode = detectResearchMode(taskText)
	if a.overseer.researchMode {
		debug.Log("overseer", "research mode detected — thresholds raised")
	}
	a.overseer.reset()
}

func countErrors(traj []trajectoryEntry) int {
	count := 0
	for _, e := range traj {
		if e.isError {
			count++
		}
	}
	return count
}

func extractWrittenPaths(resultContent string) []string {
	if resultContent == "" || !strings.Contains(resultContent, "written_paths") {
		return nil
	}
	var payload struct {
		WrittenPaths []string `json:"written_paths"`
	}

	dec := json.NewDecoder(strings.NewReader(resultContent))
	for {
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			break
		}
		if json.Unmarshal(raw, &payload) == nil && len(payload.WrittenPaths) > 0 {
			return payload.WrittenPaths
		}
	}
	return nil
}

func extractFileHint(toolName string, args []byte) string {
	hints := extractFileHints(toolName, args)
	if len(hints) > 0 {
		return hints[0]
	}
	return ""
}

func extractFileHints(toolName string, args []byte) []string {
	if toolName == "multi_file_read" {
		var parsed struct {
			Files []struct {
				Path string `json:"path"`
			} `json:"files"`
		}
		if err := json.Unmarshal(args, &parsed); err == nil && len(parsed.Files) > 0 {
			out := make([]string, 0, len(parsed.Files))
			for _, f := range parsed.Files {
				if p := strings.TrimSpace(f.Path); p != "" {
					out = append(out, p)
				}
			}
			return out
		}
		return nil
	}

	s := string(args)
	for _, key := range []string{`"path"`, `"file_path"`, `"filePath"`} {
		idx := strings.Index(s, key)
		if idx < 0 {
			continue
		}

		rest := s[idx+len(key):]

		rest = strings.TrimLeft(rest, " :\"")

		end := strings.IndexByte(rest, '"')
		if end > 0 {
			hint := rest[:end]
			if len([]rune(hint)) > 80 {
				hint = string([]rune(hint)[:77]) + "..."
			}
			return []string{hint}
		}
	}
	return nil
}

var _ = time.Second
