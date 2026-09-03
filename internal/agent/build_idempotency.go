package agent

import (
	"encoding/json"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
)

// Build Idempotency Violation Detector
//
// Research basis:
//   - "Where LLM Agents Fail and How They can Learn From Failures" (AgentDebug,
//     arXiv:2509.25370, 2025): identifies action-level failures where agents
//     repeat operations without new information. Categorises these as "redundant
//     action" failures in the AgentErrorTaxonomy.
//   - "A Self-Improving Coding Agent" (SICA, arXiv:2504.15228, NeurIPS 2025):
//     trajectory waste is the primary bottleneck — 17-53% of iterations produce
//     no forward progress.
//   - GAP: Graph-based Agent Planning (NeurIPS 2025 NORA): models
//     inter-task dependencies to avoid redundant computation. The key insight:
//     build/test commands are DETERMINISTIC — re-running them without source
//     changes is guaranteed to produce identical results.
//
// Problem: AI coding agents waste iterations re-running deterministic build or
// test commands (`go build`, `go test`, `npm test`, `make test`, etc.) when NO
// source files were edited since the last build/test run. The output is
// guaranteed identical, so the iteration is pure waste — consuming tokens,
// time, and context budget for zero new information.
//
// Example waste trajectory:
//
//	iter 1: go test ./...          → PASS
//	iter 2: read_file(result)      → understand failure
//	iter 3: edit_file(fix.go)      → fix bug
//	iter 4: go test ./...          → PASS (legitimate, code changed)
//	iter 5: read_file(other.go)    → investigate
//	iter 6: go test ./...          → WASTE: no edits since iter 4
//
// What it detects: When a build/test command is issued and no source-mutating
// tool calls (edit_file, write_file, multi_edit_file, file_ops) have occurred
// since the last build/test command in the same run.
//
// Distinct from existing detectors:
//   - repetition_tracker.go: detects repeated identical tool calls. This
//     detector catches SEMANTIC redundancy (same build command, different
//     args/flags, same zero-edit precondition).
//   - tool_redundancy.go: analyses argument-level overlap. This detector
//     tracks the CAUSAL precondition (source edits) that justifies a rebuild.
//   - futile_cycle.go: tracks read revisits. This detector tracks build/test
//     re-execution without intervening edits.
//   - tool_sequence.go: tracks suboptimal ordering. This detector tracks
//     idempotent re-execution of deterministic commands.

// buildIdempotencyState tracks build/test commands and source edits to detect
// idempotent re-execution without intervening code changes.
type buildIdempotencyState struct {
	mu sync.Mutex

	// lastBuildIter is the iteration of the most recent build/test command (0 = none).
	lastBuildIter int
	// lastBuildCmd is a normalised description of the last build/test command.
	lastBuildCmd string
	// editsSinceLastBuild counts source-mutating tool calls since the last build.
	editsSinceLastBuild int
	// totalRedundant counts redundant builds this run.
	totalRedundant int
	// warnsIssued so far.
	warnsIssued int
	// maxWarns per run.
	maxWarns int
}

func newBuildIdempotencyState() *buildIdempotencyState {
	return &buildIdempotencyState{
		maxWarns: 2,
	}
}

func (s *buildIdempotencyState) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastBuildIter = 0
	s.lastBuildCmd = ""
	s.editsSinceLastBuild = 0
	s.totalRedundant = 0
	s.warnsIssued = 0
}

// sourceMutatingTools lists tools that change source code, justifying a
// rebuild. Canonical definition lives in verify_hint.go (#154) — this alias
// keeps the historical name working and guaranteed in sync.
var sourceMutatingToolsAlias = sourceMutatingTools

// buildTestPrefixes lists tool prefixes that skip env-var stripping.
var buildTestToolPrefixes = []string{
	"go ", "make ", "npm ", "yarn ", "pnpm ", "cargo ", "python ",
	"pytest", "mvn ", "gradle ", "rake ", "dotnet ",
}

// buildTestPatterns maps command prefixes to canonical labels.
var buildTestPatterns = []struct {
	prefix string
	label  string
}{
	{"go build", "go build"},
	{"go test", "go test"},
	{"go vet", "go vet"},
	{"go check", "go check"},
	{"make build", "make build"},
	{"make test", "make test"},
	{"make verify", "make verify"},
	{"make check", "make check"},
	{"make lint", "make lint"},
	{"make ci", "make ci"},
	{"npm test", "npm test"},
	{"npm run test", "npm test"},
	{"npm run build", "npm build"},
	{"npm run lint", "npm lint"},
	{"npx tsc", "tsc"},
	{"yarn test", "yarn test"},
	{"yarn build", "yarn build"},
	{"pnpm test", "pnpm test"},
	{"pnpm build", "pnpm build"},
	{"cargo build", "cargo build"},
	{"cargo test", "cargo test"},
	{"cargo check", "cargo check"},
	{"cargo clippy", "cargo clippy"},
	{"pytest", "pytest"},
	{"python -m pytest", "pytest"},
	{"mvn test", "mvn test"},
	{"mvn compile", "mvn compile"},
	{"gradle test", "gradle test"},
	{"gradle build", "gradle build"},
	{"rake test", "rake test"},
	{"dotnet build", "dotnet build"},
	{"dotnet test", "dotnet test"},
	{"tsc --noemit", "tsc"},
	{"eslint", "eslint"},
	{"golangci-lint", "golangci-lint"},
}

// hasBuildTestPrefix checks if a trimmed command starts with any known tool prefix.
func hasBuildTestPrefix(s string) bool {
	for _, p := range buildTestToolPrefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

// stripEnvVars removes leading FOO=bar assignments from a command line.
func stripEnvVars(s string) string {
	for strings.Contains(s, "=") && !hasBuildTestPrefix(s) {
		idx := strings.Index(s, " ")
		if idx == -1 {
			return s
		}
		s = strings.TrimSpace(s[idx:])
	}
	return s
}

// detectBuildTestCommand checks whether a command string represents a deterministic
// build or test operation whose output depends only on source files.
func detectBuildTestCommand(cmd string) (bool, string) {
	c := strings.ToLower(strings.TrimSpace(cmd))
	if c == "" {
		return false, ""
	}

	// Strip leading shell comments and env vars to find the actual command.
	// e.g. "# build\nGOOS=linux go build ./..." -> "go build ./..."
	for _, rawLine := range strings.Split(c, "\n") {
		trimmed := strings.TrimSpace(rawLine)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		c = stripEnvVars(trimmed)
		break
	}

	for _, pat := range buildTestPatterns {
		if strings.HasPrefix(c, pat.prefix) {
			return true, pat.label
		}
	}
	return false, ""
}

// recordToolCall processes a tool call. If it's a build/test command, it checks
// whether source edits occurred since the last build. Returns a warning string
// if an idempotency violation is detected (empty otherwise).
func (s *buildIdempotencyState) recordToolCall(toolName string, args json.RawMessage, iteration int) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Track source-mutating tools.
	if sourceMutatingTools[toolName] {
		s.editsSinceLastBuild++
		return ""
	}

	// Only run_command and start_command can be build/test invocations.
	if toolName != "run_command" && toolName != "start_command" {
		return ""
	}

	cmd := extractStringField(args, "command")
	if cmd == "" {
		return ""
	}

	// #749: shell commands can mutate sources too (gofmt -w, go mod tidy,
	// sed -i, git apply, generators). They are invisible to sourceMutatingTools,
	// so without this the next build is misreported as redundant with a false
	// "guaranteed identical" claim. Count as an edit; asymmetric cost says
	// prefer missing a redundant-build warning over suppressing a needed one.
	if shellMutatesSources(cmd) {
		s.editsSinceLastBuild++
	}

	isBuild, label := detectBuildTestCommand(cmd)
	if !isBuild {
		return ""
	}

	warning := ""

	// Check idempotency: if we had a prior build and no edits since, it's redundant.
	// #1441-A: the check never compared WHICH command ran - the standard
	// build->test->lint chain has 0 edits between steps, so test and lint
	// each fired "re-running go test... guaranteed identical... move on if
	// verification passed" (go test never ran this run; build passing does
	// NOT guarantee test passing - and the wording nudged skipping tests
	// after a green build). The guarantee only holds for the SAME command;
	// lastBuildCmd was written but never read (the third same-command
	// evidence alongside the docblock's rationale and examples).
	if s.lastBuildIter > 0 && s.editsSinceLastBuild == 0 && label == s.lastBuildCmd {
		s.totalRedundant++
		if s.warnsIssued < s.maxWarns {
			s.warnsIssued++
			debug.Log("agent", "Iteration %d: build idempotency violation: %s re-run with 0 edits since iter %d",
				iteration, label, s.lastBuildIter)
			warning = formatIdempotencyWarning(label, s.lastBuildIter, iteration, s.totalRedundant)
		}
	}

	// Update state: this is now the most recent build.
	s.lastBuildIter = iteration
	s.lastBuildCmd = label
	s.editsSinceLastBuild = 0

	return warning
}

// shellMutatesSources reports whether a shell command plausibly rewrites
// source files (or module files) in place (#749). Substring match on the
// lowercased command is deliberately broad: the cost of a false positive is
// one missed redundancy warning, while the cost of a false negative is
// actively discouraging a necessary rebuild.
func shellMutatesSources(cmd string) bool {
	lower := strings.ToLower(cmd)
	for _, pat := range []string{
		"gofmt -w", "gofmt -l -w", "goimports -w",
		"go fmt ", // #1028: `go fmt` rewrites files in place (wraps gofmt -l -w); missing here made the cache-hit note "no source files have changed" literally false
		"sed -i", "git apply", "patch -p",
		"go mod tidy", "go mod get", "go get ",
		"gofumpt -w", "prettier --write", "eslint --fix",
		"rustfmt", "black ", "isort ", "autopep8",
	} {
		if strings.Contains(lower, pat) {
			return true
		}
	}
	// #1028 follow-up: bare `go fmt` (no args) also rewrites the current
	// directory's packages in place; the "go fmt " pattern above requires a
	// trailing space. It is cacheable (HasPrefix "go fmt"), so without this
	// the cache-hit note would be false for it too.
	if lower == "go fmt" {
		return true
	}
	return false
}

func formatIdempotencyWarning(label string, lastBuildIter, curIter int, totalRedundant int) string {
	var sb strings.Builder
	sb.WriteString("[Build Idempotency] Iterations ")
	sb.WriteString(itoaIdempot(lastBuildIter))
	sb.WriteString("-")
	sb.WriteString(itoaIdempot(curIter))
	sb.WriteString(": re-running \"")
	sb.WriteString(label)
	sb.WriteString("\" with 0 source edits since the last build/test run.\n")
	sb.WriteString("Build and test commands are deterministic -- without code changes the result is ")
	sb.WriteString("guaranteed identical to the previous run. This iteration produced zero new information.\n")
	if totalRedundant > 1 {
		sb.WriteString("This is redundant build #")
		sb.WriteString(itoaIdempot(totalRedundant))
		sb.WriteString(" this run. Repeated idempotent re-builds signal stuck iteration.\n")
	}
	sb.WriteString("Action: either make the code change first, or move on to the next task if verification already passed.")
	return sb.String()
}

// itoa is a lightweight int-to-string without importing strconv (keeps deps minimal).
func itoaIdempot(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// extractStringField extracts a string field from JSON args. Returns "" if not found.
func extractBuildCmdField(args json.RawMessage, field string) string {
	if len(args) == 0 {
		return ""
	}
	var m map[string]interface{}
	if err := json.Unmarshal(args, &m); err != nil {
		return ""
	}
	v, ok := m[field]
	if !ok {
		return ""
	}
	switch val := v.(type) {
	case string:
		return val
	default:
		return ""
	}
}
