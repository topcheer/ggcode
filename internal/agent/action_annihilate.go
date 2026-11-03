package agent

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
)

type annihilationPair struct {
	priorTool   string
	cancelTool  string
	matchFn     func(priorArgs, cancelArgs json.RawMessage) bool
	qualifyFn   func(priorArgs, cancelArgs json.RawMessage) string
	description string
}

type trackedAction struct {
	tool      string
	args      json.RawMessage
	iteration int
}

type actionAnnihilateState struct {
	mu sync.Mutex

	actions []trackedAction

	commitHashes map[int]string

	cancelCount int
	maxWarns    int
	warnsIssued int
	lookback    int
}

func newActionAnnihilateState() *actionAnnihilateState {
	return &actionAnnihilateState{
		maxWarns:     2,
		lookback:     20,
		commitHashes: make(map[int]string),
	}
}

func (s *actionAnnihilateState) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.actions = nil
	s.cancelCount = 0
	s.warnsIssued = 0
	s.commitHashes = make(map[int]string)
}

func (s *actionAnnihilateState) recordCommitHash(iteration int, hash string) {
	if hash == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.commitHashes[iteration] = hash
}

var annihilationPairs = []annihilationPair{
	{
		priorTool:   "git_add",
		cancelTool:  "git_reset",
		description: "git_add then git_reset (staged then unstaged the same files)",
		matchFn:     matchGitAddReset,
		qualifyFn:   qualifyGitAddResetPartial,
	},
	{
		priorTool:   "git_commit",
		cancelTool:  "git_revert",
		description: "git_commit then git_revert (committed then reverted)",

		matchFn: nil,
	},
	{
		priorTool:   "edit_file",
		cancelTool:  "undo_edit",
		description: "edit_file then undo_edit (edited then undid your own change)",
		matchFn:     nil,
	},
	{
		priorTool:   "multi_edit_file",
		cancelTool:  "undo_edit",
		description: "multi_edit_file then undo_edit (edited then undid your own change)",
		matchFn:     nil,
	},
	{
		priorTool:   "write_file",
		cancelTool:  "undo_edit",
		description: "write_file then undo_edit (wrote then undid your own change)",
		matchFn:     nil,
	},
	{
		priorTool:   "file_ops",
		cancelTool:  "file_ops",
		description: "file_ops mkdir then file_ops delete on the same path",
		matchFn:     matchMkdirDelete,
	},
	{
		priorTool:   "git_checkout",
		cancelTool:  "git_checkout",
		description: "git_checkout switch then switch back (branch thrashing)",
		matchFn:     matchCheckoutRoundtrip,
	},
}

func (s *actionAnnihilateState) recordToolCall(toolName string, args json.RawMessage, iteration int) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	warning := s.checkAnnihilation(toolName, args, iteration)

	s.actions = append(s.actions, trackedAction{
		tool:      toolName,
		args:      args,
		iteration: iteration,
	})

	if len(s.actions) > s.lookback {
		dropped := s.actions[:len(s.actions)-s.lookback]
		s.actions = s.actions[len(s.actions)-s.lookback:]

		for _, d := range dropped {
			delete(s.commitHashes, d.iteration)
		}
	}

	return warning
}

func (s *actionAnnihilateState) checkAnnihilation(currentTool string, currentArgs json.RawMessage, iteration int) string {

	if currentTool == "undo_edit" {
		return s.checkUndoEditAnnihilation(currentArgs, iteration)
	}

	if currentTool == "git_checkout" {
		return s.checkCheckoutRoundtripAnnihilation(currentArgs, iteration)
	}

	for _, pair := range annihilationPairs {
		if currentTool != pair.cancelTool {
			continue
		}

		for i := len(s.actions) - 1; i >= 0; i-- {
			prior := s.actions[i]

			if pair.priorTool == "git_add" && prior.tool == "git_commit" {
				break
			}
			if prior.tool != pair.priorTool {
				continue
			}

			if pair.priorTool == "git_commit" && !s.commitHashMatches(prior.iteration, currentArgs) {
				continue
			}

			if pair.matchFn != nil && !pair.matchFn(prior.args, currentArgs) {
				continue
			}

			s.cancelCount++
			if s.warnsIssued >= s.maxWarns {
				return ""
			}
			s.warnsIssued++
			debug.Log("agent", "Iteration %d: action annihilation detected: %s (pair #%d)",
				iteration, pair.description, s.cancelCount)

			qualifier := ""
			if pair.qualifyFn != nil {
				qualifier = pair.qualifyFn(prior.args, currentArgs)
			}
			return formatAnnihilationWarning(pair.description, prior.iteration, iteration, s.cancelCount, qualifier)
		}
	}

	return ""
}

func (s *actionAnnihilateState) checkUndoEditAnnihilation(currentArgs json.RawMessage, iteration int) string {
	if undoEditAction(currentArgs) == "list" {
		return ""
	}
	for i := len(s.actions) - 1; i >= 0; i-- {
		prior := s.actions[i]
		desc, ok := undoEditPriorDescription(prior.tool)
		if !ok {
			continue
		}
		s.cancelCount++
		if s.warnsIssued >= s.maxWarns {
			return ""
		}
		s.warnsIssued++
		debug.Log("agent", "Iteration %d: action annihilation detected: %s (pair #%d)",
			iteration, desc, s.cancelCount)
		return formatAnnihilationWarning(desc, prior.iteration, iteration, s.cancelCount, "")
	}
	return ""
}

func (s *actionAnnihilateState) checkCheckoutRoundtripAnnihilation(currentArgs json.RawMessage, iteration int) string {
	currentBranch := extractStringField(currentArgs, "branch")
	if currentBranch == "" {
		return ""
	}
	sawDifferentHop := false
	for i := len(s.actions) - 1; i >= 0; i-- {
		prior := s.actions[i]
		if prior.tool != "git_checkout" {

			return ""
		}
		if extractStringField(prior.args, "branch") != currentBranch {
			sawDifferentHop = true
			continue
		}
		if !sawDifferentHop {

			continue
		}
		s.cancelCount++
		if s.warnsIssued >= s.maxWarns {
			return ""
		}
		s.warnsIssued++
		desc, _ := checkoutRoundtripDescription()
		debug.Log("agent", "Iteration %d: action annihilation detected: %s (pair #%d)",
			iteration, desc, s.cancelCount)
		return formatAnnihilationWarning(desc, prior.iteration, iteration, s.cancelCount, "")
	}
	return ""
}

func checkoutRoundtripDescription() (string, bool) {
	for _, p := range annihilationPairs {
		if p.priorTool == "git_checkout" && p.cancelTool == "git_checkout" {
			return p.description, true
		}
	}
	return "git_checkout switch then switch back (branch thrashing)", false
}

func undoEditAction(args json.RawMessage) string {
	var parsed struct {
		Action string `json:"action"`
	}
	if err := json.Unmarshal(args, &parsed); err != nil || parsed.Action == "" {
		return "undo"
	}
	return parsed.Action
}

func undoEditPriorDescription(priorTool string) (string, bool) {
	for _, p := range annihilationPairs {
		if p.cancelTool == "undo_edit" && p.priorTool == priorTool {
			return p.description, true
		}
	}
	return "", false
}

func formatAnnihilationWarning(desc string, priorIter, curIter int, totalCancels int, qualifier string) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("[Action Annihilation] Iterations %d-%d: %s.\n", priorIter, curIter, desc))
	if qualifier != "" {

		sb.WriteString(qualifier)
		return sb.String()
	}
	sb.WriteString("These two actions produced net-zero state change -- the work between them was wasted.\n")
	if totalCancels > 1 {
		sb.WriteString(fmt.Sprintf("This is cancellation pair #%d this run. Repeated annihilation signals an undecided or oscillating approach.\n", totalCancels))
	}
	sb.WriteString("Reflect: what are you actually trying to accomplish? Commit to a single approach and execute it.")
	return sb.String()
}

func matchGitAddReset(priorArgs, cancelArgs json.RawMessage) bool {
	priorFiles := extractStringSlice(priorArgs, "files")
	if len(priorFiles) == 0 || (len(priorFiles) == 1 && (priorFiles[0] == "." || priorFiles[0] == "*")) {

		return true
	}
	resetFiles := extractStringSlice(cancelArgs, "files")
	if len(resetFiles) == 0 {

		return true
	}

	priorSet := make(map[string]bool, len(priorFiles))
	for _, f := range priorFiles {
		priorSet[f] = true
	}
	for _, f := range resetFiles {
		if priorSet[f] {
			return true
		}
	}
	return false
}

const partialResetQualifier = "This reset cancelled only PART of the staged set -- most staged files remain staged, so this is NOT net-zero. Partial unstaging after a stage-all (e.g. excluding swept-in WIP files before a release commit) is a standard curation step; if that is what you did, proceed. Repeated partial resets, however, may signal an unsettled staging plan."

func qualifyGitAddResetPartial(priorArgs, cancelArgs json.RawMessage) string {
	resetFiles := extractStringSlice(cancelArgs, "files")
	if len(resetFiles) == 0 {
		return ""
	}
	priorFiles := extractStringSlice(priorArgs, "files")
	if len(priorFiles) == 0 || (len(priorFiles) == 1 && (priorFiles[0] == "." || priorFiles[0] == "*")) {
		return partialResetQualifier
	}
	resetSet := make(map[string]bool, len(resetFiles))
	for _, f := range resetFiles {
		resetSet[f] = true
	}
	for _, f := range priorFiles {
		if !resetSet[f] {
			return partialResetQualifier
		}
	}
	return ""
}

func matchMkdirDelete(priorArgs, cancelArgs json.RawMessage) bool {
	priorOps := extractOps(priorArgs)
	cancelOps := extractOps(cancelArgs)
	for _, po := range priorOps {
		if po.action != "mkdir" {
			continue
		}
		for _, co := range cancelOps {
			if co.action == "delete" && co.source == po.source {
				return true
			}
		}
	}
	return false
}

func matchCheckoutRoundtrip(priorArgs, cancelArgs json.RawMessage) bool {
	priorBranch := extractStringField(priorArgs, "branch")
	cancelBranch := extractStringField(cancelArgs, "branch")
	return priorBranch != "" && priorBranch == cancelBranch
}

func extractStringField(args json.RawMessage, field string) string {
	var m map[string]interface{}
	if err := json.Unmarshal(args, &m); err != nil {
		return ""
	}
	v, ok := m[field]
	if !ok {
		return ""
	}
	str, ok := v.(string)
	if !ok {
		return ""
	}
	return str
}

func extractStringSlice(args json.RawMessage, field string) []string {
	var m map[string]interface{}
	if err := json.Unmarshal(args, &m); err != nil {
		return nil
	}
	v, ok := m[field]
	if !ok {
		return nil
	}
	arr, ok := v.([]interface{})
	if !ok {
		return nil
	}
	result := make([]string, 0, len(arr))
	for _, item := range arr {
		if s, ok := item.(string); ok {
			result = append(result, s)
		}
	}
	return result
}

type fileOpEntry struct {
	action string
	source string
}

func extractOps(args json.RawMessage) []fileOpEntry {
	var m map[string]interface{}
	if err := json.Unmarshal(args, &m); err != nil {
		return nil
	}
	v, ok := m["operations"]
	if !ok {
		return nil
	}
	arr, ok := v.([]interface{})
	if !ok {
		return nil
	}
	result := make([]fileOpEntry, 0, len(arr))
	for _, item := range arr {
		om, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		action, _ := om["action"].(string)
		source, _ := om["source"].(string)
		if action != "" {
			result = append(result, fileOpEntry{action: action, source: source})
		}
	}
	return result
}

func (s *actionAnnihilateState) commitHashMatches(iter int, revertArgs json.RawMessage) bool {
	var parsed struct {
		Commit string `json:"commit"`
	}
	if err := json.Unmarshal(revertArgs, &parsed); err != nil || parsed.Commit == "" {
		return false
	}
	hash, ok := s.commitHashes[iter]
	if !ok || hash == "" {
		return false
	}
	target := strings.ToLower(parsed.Commit)
	recorded := strings.ToLower(hash)
	shorter := len(target)
	if len(recorded) < shorter {
		shorter = len(recorded)
	}
	return target[:shorter] == recorded[:shorter]
}

var newCommitHashRegex = regexp.MustCompile(`\[[^\]]*?([0-9a-f]{7,40})\]`)

func extractNewCommitHash(content string) string {
	m := newCommitHashRegex.FindStringSubmatch(content)
	if m == nil {
		return ""
	}
	return m[1]
}
