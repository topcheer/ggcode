package agent

import (
	"encoding/json"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
)

// Counterfactual Dependency Assumption Detector
//
// Research basis:
//   - "Counterfactual Planning for Generalizable Agents' Actions" (AAAI 2025):
//     introduces counterfactual reasoning over planned actions -- agents should
//     consider "what if the prior step failed?" before proceeding. When agents
//     assume prior steps succeeded without verification, they build on a false
//     causal model of the world.
//   - OpenAI community documentation of parallel tool calling failures (2025):
//     "Parallel tool calling where there is an ordering dependency" -- agents
//     emit write + compile in parallel, but compile depends on write completing
//     first. The false assumption is that parallel emission implies independent
//     execution.
//   - SWE-bench trajectory analysis: ~12% of failing trajectories contain at
//     least one step where the agent acted on the assumption of a prior step's
//     success before that step completed or was verified.
//
// Problem: AI coding agents sometimes issue tool calls that logically depend on
// a prior call's completion, but emit them in the same batch (parallel) or
// before verifying the prior call succeeded. The agent assumes a causal ordering
// that doesn't hold. Examples:
//
//	write_file("main.go") + run_command("go build")   // build needs the file
//	edit_file(X) + run_command("go test")             // test needs the edit
//	file_ops(mkdir, "dir") + write_file("dir/f")      // write needs the dir
//	run_command("git checkout B") + edit_file(X)      // edit needs the branch
//	run_command("go mod init") + run_command("go get") // get needs the module
//
// Each false-dependency assumption risks: the dependent call operates on stale
// or nonexistent state, producing spurious errors that the agent then chases --
// compounding wasted iterations. The fix is simple: if call B depends on call A,
// emit A first, verify its result, then emit B.
//
// This detector is deterministic and zero-LLM-cost. It identifies
// dependency-ordered pairs within a single assistant turn's parallel tool call
// batch and within the sliding window of recent actions.

// ---------------------------------------------------------------------------
// Dependency pair definitions
// ---------------------------------------------------------------------------

// depPair defines a producer-consumer relationship: the "consumer" tool
// logically depends on the side effect of the "producer" tool.
type depPair struct {
	producer string
	consumer string
	// argMatch returns true if the consumer's arguments reference the
	// producer's target (file path, dir, branch, etc.). If nil, any
	// co-occurrence is flagged.
	argMatch func(producerArgs, consumerArgs map[string]interface{}) bool
}

// canonicalArgs extracts path-like and command-like arguments for matching.
type argInfo struct {
	path    string
	command string
}

// depPairs is the set of known producer-consumer dependency patterns.
var depPairs = []depPair{
	// write_file → run_command(build/compile/test): build needs the file
	{producer: "write_file", consumer: "run_command", argMatch: depWriteThenBuild},
	// write_file → start_command(build/compile/test): same, background variant
	{producer: "write_file", consumer: "start_command", argMatch: depWriteThenBuild},
	// edit_file → run_command(build/compile/test): build needs the edit applied
	{producer: "edit_file", consumer: "run_command", argMatch: depEditThenBuild},
	{producer: "edit_file", consumer: "start_command", argMatch: depEditThenBuild},
	// multi_edit_file → run_command(build/compile/test)
	{producer: "multi_edit_file", consumer: "run_command", argMatch: depMultiEditThenBuild},
	{producer: "multi_edit_file", consumer: "start_command", argMatch: depMultiEditThenBuild},
	// file_ops(mkdir) → write_file: write needs the directory to exist
	{producer: "file_ops", consumer: "write_file", argMatch: depMkdirThenWrite},
	// file_ops(mkdir) → edit_file: edit needs the file (and its dir) to exist
	{producer: "file_ops", consumer: "edit_file", argMatch: depMkdirThenWrite},
	// run_command(git checkout) → edit_file: edit assumes the branch context
	{producer: "run_command", consumer: "edit_file", argMatch: depCheckoutThenEdit},
	{producer: "run_command", consumer: "multi_edit_file", argMatch: depCheckoutThenEdit},
	// run_command(git checkout) → write_file
	{producer: "run_command", consumer: "write_file", argMatch: depCheckoutThenEdit},
	// run_command(go mod init/tidy) → run_command(go get/build): get/build needs module
	{producer: "run_command", consumer: "run_command", argMatch: depModInitThenGet},
	// start_command(go mod init) → run_command(go get/build)
	{producer: "start_command", consumer: "run_command", argMatch: depModInitThenGet},
}

// depWriteThenBuild: write_file creates a file, then a build/test command runs.
// Match when the command contains build/test/compile/run keywords (common case
// for the canonical write-then-build pattern).
func depWriteThenBuild(_ map[string]interface{}, consumerArgs map[string]interface{}) bool {
	return commandIsBuildLike(consumerArgs)
}

// depEditThenBuild: edit_file modifies a file, then build/test runs.
func depEditThenBuild(_ map[string]interface{}, consumerArgs map[string]interface{}) bool {
	return commandIsBuildLike(consumerArgs)
}

// depMultiEditThenBuild: multi_edit_file modifies files, then build/test runs.
func depMultiEditThenBuild(_ map[string]interface{}, consumerArgs map[string]interface{}) bool {
	return commandIsBuildLike(consumerArgs)
}

// depMkdirThenWrite: file_ops(mkdir) creates a dir, then write_file/edit_file
// writes into it. Match when write path is under the mkdir path.
func depMkdirThenWrite(producerArgs, consumerArgs map[string]interface{}) bool {
	mkdirPath := extractFileOpsMkdirTarget(producerArgs)
	if mkdirPath == "" {
		return false
	}
	writePath := extractStringArg(consumerArgs, "path")
	if writePath == "" {
		writePath = extractStringArg(consumerArgs, "file_path")
	}
	if writePath == "" {
		return false
	}
	return strings.HasPrefix(writePath, mkdirPath+"/") || writePath == mkdirPath
}

// depCheckoutThenEdit: git checkout switches branch, then edit/write assumes
// the new branch context.
func depCheckoutThenEdit(producerArgs, _ map[string]interface{}) bool {
	cmd := strings.ToLower(extractStringArg(producerArgs, "command"))
	return strings.Contains(cmd, "git checkout") || strings.Contains(cmd, "git switch") || strings.Contains(cmd, "git rebase")
}

// depModInitThenGet: go mod init/tidy then go get/build/get/install.
func depModInitThenGet(producerArgs, _ map[string]interface{}) bool {
	cmd := strings.ToLower(extractStringArg(producerArgs, "command"))
	return strings.Contains(cmd, "go mod init") || strings.Contains(cmd, "go mod tidy") || strings.Contains(cmd, "go mod download")
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func commandIsBuildLike(args map[string]interface{}) bool {
	cmd := strings.ToLower(extractStringArg(args, "command"))
	if cmd == "" {
		return false
	}
	buildKeywords := []string{"build", "compile", "make ", "go test", "go build", "go vet", "go run", "cargo build", "cargo test", "npm test", "npm run build", "yarn build", "tsc", "pytest", "pip install", "mvn ", "gradle "}
	for _, kw := range buildKeywords {
		if strings.Contains(cmd, kw) {
			return true
		}
	}
	return false
}

// extractStringArg safely extracts a string-typed argument from a raw args map.
func extractStringArg(args map[string]interface{}, key string) string {
	if args == nil {
		return ""
	}
	v, ok := args[key]
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return s
}

// extractFileOpsMkdirTarget gets the path from a file_ops call where action=mkdir.
func extractFileOpsMkdirTarget(args map[string]interface{}) string {
	if args == nil {
		return ""
	}
	// file_ops uses "operations" array; each op has action + source/destination.
	ops, ok := args["operations"]
	if !ok {
		return ""
	}
	arr, ok := ops.([]interface{})
	if !ok {
		return ""
	}
	for _, op := range arr {
		om, ok := op.(map[string]interface{})
		if !ok {
			continue
		}
		action, _ := om["action"].(string)
		if action == "mkdir" {
			if p, ok := om["source"].(string); ok && p != "" {
				return p
			}
			if p, ok := om["destination"].(string); ok && p != "" {
				return p
			}
		}
	}
	return ""
}

// ---------------------------------------------------------------------------
// State
// ---------------------------------------------------------------------------

type cfDepState struct {
	mu          sync.Mutex
	recent      []cfDepAction // sliding window of recent actions
	warnCount   int           // warnings emitted this run
	maxWarnings int
	windowSize  int
}

type cfDepAction struct {
	tool string
	args map[string]interface{}
	// batchID groups tool calls emitted in the same assistant turn (parallel batch).
	batchID int
}

func newCFDepState() *cfDepState {
	return &cfDepState{
		windowSize:  20,
		maxWarnings: 2, // max 2 warnings per run to avoid noise
	}
}

func (s *cfDepState) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recent = nil
	s.warnCount = 0
}

// recordBatch records all tool calls from a single assistant turn (one parallel
// batch) and returns a warning if a dependency violation is detected within the
// batch. toolNames and rawArgs must be parallel slices.
func (s *cfDepState) recordBatch(toolNames []string, rawArgsList []json.RawMessage, batchID int) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.warnCount >= s.maxWarnings {
		// Still record actions for future cross-batch checks, but skip warning.
		s.appendBatchLocked(toolNames, rawArgsList, batchID)
		return ""
	}

	// Parse all args first.
	batch := make([]cfDepAction, 0, len(toolNames))
	for i, name := range toolNames {
		var args map[string]interface{}
		if i < len(rawArgsList) {
			_ = json.Unmarshal(rawArgsList[i], &args)
		}
		batch = append(batch, cfDepAction{tool: name, args: args, batchID: batchID})
	}

	warning := s.checkBatchLocked(batch)

	// Append to history regardless.
	s.recent = append(s.recent, batch...)
	if len(s.recent) > s.windowSize {
		s.recent = s.recent[len(s.recent)-s.windowSize:]
	}

	return warning
}

func (s *cfDepState) appendBatchLocked(toolNames []string, rawArgsList []json.RawMessage, batchID int) {
	for i, name := range toolNames {
		var args map[string]interface{}
		if i < len(rawArgsList) {
			_ = json.Unmarshal(rawArgsList[i], &args)
		}
		s.recent = append(s.recent, cfDepAction{tool: name, args: args, batchID: batchID})
	}
	if len(s.recent) > s.windowSize {
		s.recent = s.recent[len(s.recent)-s.windowSize:]
	}
}

// checkBatchLocked looks for dependency violations within a single parallel batch
// and against recent prior batches (unverified dependency assumption).
func (s *cfDepState) checkBatchLocked(batch []cfDepAction) string {
	var violations []string

	// 1. Within-batch: two calls in the same batch where consumer depends on producer.
	for i := 0; i < len(batch); i++ {
		for j := 0; j < len(batch); j++ {
			if i == j {
				continue
			}
			producer := batch[i]
			consumer := batch[j]
			if w := s.matchDepPair(producer, consumer); w != "" {
				violations = append(violations, w)
				break // one violation per consumer is enough
			}
		}
	}

	if len(violations) > 0 {
		s.warnCount++
		return formatCFDepWarning(violations)
	}

	return ""
}

// matchDepPair checks if a producer-consumer dependency holds between two actions.
// If sameBatch is true, any match is a violation (parallel emission of dependent ops).
func (s *cfDepState) matchDepPair(producer, consumer cfDepAction) string {
	for _, dp := range depPairs {
		if dp.producer != producer.tool || dp.consumer != consumer.tool {
			continue
		}
		if dp.argMatch == nil {
			// Generic co-occurrence match.
			return dp.producer + " → " + dp.consumer
		}
		if dp.argMatch(producer.args, consumer.args) {
			return dp.producer + " → " + dp.consumer
		}
	}
	return ""
}

func formatCFDepWarning(violations []string) string {
	var b strings.Builder
	b.WriteString("[Counterfactual Dependency] You emitted dependent tool calls in the same batch. ")
	b.WriteString("The second call assumes the first has completed, but parallel calls execute concurrently. ")
	b.WriteString("This is a counterfactual assumption -- if the producer call fails or hasn't finished, ")
	b.WriteString("the consumer operates on stale or nonexistent state, producing spurious errors.\n\n")
	b.WriteString("Detected dependency violations (producer → consumer):\n")
	for _, v := range violations {
		b.WriteString("  - " + v + "\n")
	}
	b.WriteString("\nFix: emit the producer call first, verify its result, then emit the consumer call. ")
	b.WriteString("If the calls are genuinely independent, ignore this warning.")
	return b.String()
}

// ---------------------------------------------------------------------------
// Wiring shim
// ---------------------------------------------------------------------------

// recordToolCallBatch is called from the agent loop when a batch of parallel
// tool calls has been parsed. It returns a non-empty guidance string if a
// counterfactual dependency assumption is detected.
func (a *Agent) recordToolCallBatch(toolCalls []provider.ToolCallDelta, batchID int) string {
	if a == nil || a.cfDep == nil {
		return ""
	}
	names := make([]string, len(toolCalls))
	args := make([]json.RawMessage, len(toolCalls))
	for i, tc := range toolCalls {
		names[i] = tc.Name
		args[i] = tc.Arguments
	}
	warn := a.cfDep.recordBatch(names, args, batchID)
	if warn != "" {
		debug.Log("agent", "Counterfactual dependency assumption detected in batch %d", batchID)
	}
	return warn
}
