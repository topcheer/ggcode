package agent

import (
	"encoding/json"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
)

type depPair struct {
	producer string
	consumer string
	argMatch func(producerArgs, consumerArgs map[string]interface{}) bool
}

var depPairs = []depPair{

	{producer: "write_file", consumer: "run_command", argMatch: depWriteThenBuild},

	{producer: "write_file", consumer: "start_command", argMatch: depWriteThenBuild},

	{producer: "edit_file", consumer: "run_command", argMatch: depEditThenBuild},
	{producer: "edit_file", consumer: "start_command", argMatch: depEditThenBuild},

	{producer: "multi_edit_file", consumer: "run_command", argMatch: depMultiEditThenBuild},
	{producer: "multi_edit_file", consumer: "start_command", argMatch: depMultiEditThenBuild},

	{producer: "file_ops", consumer: "write_file", argMatch: depMkdirThenWrite},

	{producer: "file_ops", consumer: "edit_file", argMatch: depMkdirThenWrite},

	{producer: "run_command", consumer: "edit_file", argMatch: depCheckoutThenEdit},
	{producer: "run_command", consumer: "multi_edit_file", argMatch: depCheckoutThenEdit},

	{producer: "run_command", consumer: "write_file", argMatch: depCheckoutThenEdit},

	{producer: "run_command", consumer: "run_command", argMatch: depModInitThenGet},

	{producer: "start_command", consumer: "run_command", argMatch: depModInitThenGet},
}

func depWriteThenBuild(_ map[string]interface{}, consumerArgs map[string]interface{}) bool {
	return commandIsBuildLike(consumerArgs)
}

func depEditThenBuild(_ map[string]interface{}, consumerArgs map[string]interface{}) bool {
	return commandIsBuildLike(consumerArgs)
}

func depMultiEditThenBuild(_ map[string]interface{}, consumerArgs map[string]interface{}) bool {
	return commandIsBuildLike(consumerArgs)
}

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

func depCheckoutThenEdit(producerArgs, _ map[string]interface{}) bool {
	cmd := strings.ToLower(extractStringArg(producerArgs, "command"))
	return strings.Contains(cmd, "git checkout") || strings.Contains(cmd, "git switch") || strings.Contains(cmd, "git rebase")
}

func depModInitThenGet(producerArgs, consumerArgs map[string]interface{}) bool {
	cmd := strings.ToLower(extractStringArg(producerArgs, "command"))
	if !(strings.Contains(cmd, "go mod init") || strings.Contains(cmd, "go mod tidy") || strings.Contains(cmd, "go mod download")) {
		return false
	}
	consumer := strings.ToLower(extractStringArg(consumerArgs, "command"))
	for _, kw := range []string{"go get", "go build", "go install", "go run", "go test", "go vet", "go mod verify"} {
		if strings.Contains(consumer, kw) {
			return true
		}
	}
	return false
}

var buildCommandPrefixes = []string{
	"go build", "go test", "go vet", "go run", "go install", "go generate",
	"cargo build", "cargo test", "cargo check", "cargo run",
	"npm test", "npm run build", "npm run test",
	"yarn build", "yarn test", "pnpm build", "pnpm test",
	"tsc", "make", "mvn", "gradle", "pytest",
	"python -m build", "pip install", "cmake --build", "bazel build",
}

func commandIsBuildLike(args map[string]interface{}) bool {
	cmd := strings.ToLower(extractStringArg(args, "command"))
	if cmd == "" {
		return false
	}
	for _, seg := range strings.FieldsFunc(cmd, func(r rune) bool {
		return r == '\n' || r == ';' || r == '|' || r == '&'
	}) {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			continue
		}
		for _, p := range buildCommandPrefixes {
			if seg == p || strings.HasPrefix(seg, p+" ") {
				return true
			}
		}
	}
	return false
}

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

func extractFileOpsMkdirTarget(args map[string]interface{}) string {
	if args == nil {
		return ""
	}

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

type cfDepState struct {
	mu          sync.Mutex
	warnCount   int
	maxWarnings int
}

func newCFDepState() *cfDepState {
	return &cfDepState{
		maxWarnings: 2,
	}
}

type cfDepAction struct {
	tool    string
	args    map[string]interface{}
	batchID int
}

func (s *cfDepState) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.warnCount = 0
}

func (s *cfDepState) recordBatch(toolNames []string, rawArgsList []json.RawMessage, batchID int) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.warnCount >= s.maxWarnings {
		return ""
	}

	batch := make([]cfDepAction, 0, len(toolNames))
	for i, name := range toolNames {
		var args map[string]interface{}
		if i < len(rawArgsList) {
			_ = json.Unmarshal(rawArgsList[i], &args)
		}
		batch = append(batch, cfDepAction{tool: name, args: args, batchID: batchID})
	}

	return s.checkBatchLocked(batch)
}

func (s *cfDepState) checkBatchLocked(batch []cfDepAction) string {
	var violations []string

	for i := 0; i < len(batch); i++ {
		for j := 0; j < len(batch); j++ {
			if i == j {
				continue
			}
			producer := batch[i]
			consumer := batch[j]
			if w := s.matchDepPair(producer, consumer); w != "" {
				violations = append(violations, w)
				break
			}
		}
	}

	if len(violations) > 0 {
		s.warnCount++
		return formatCFDepWarning(violations)
	}

	return ""
}

func (s *cfDepState) matchDepPair(producer, consumer cfDepAction) string {
	for _, dp := range depPairs {
		if dp.producer != producer.tool || dp.consumer != consumer.tool {
			continue
		}
		if dp.argMatch == nil {

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
