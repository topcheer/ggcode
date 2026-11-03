package agent

import (
	"encoding/json"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
)

type buildIdempotencyState struct {
	mu sync.Mutex

	lastBuildIter       int
	lastBuildCmd        string
	editsSinceLastBuild int
	totalRedundant      int
	warnsIssued         int
	maxWarns            int
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

var buildTestToolPrefixes = []string{
	"go ", "make ", "npm ", "yarn ", "pnpm ", "cargo ", "python ",
	"pytest", "mvn ", "gradle ", "rake ", "dotnet ",
}

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

func hasBuildTestPrefix(s string) bool {
	for _, p := range buildTestToolPrefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

var buildAffectingEnv = map[string]bool{
	"goos":        true,
	"goarch":      true,
	"cgo_enabled": true,
	"goflags":     true,
}

func stripEnvVarsKeepBuildEnv(s string, envKey *[]string) string {
	for strings.Contains(s, "=") && !hasBuildTestPrefix(s) {
		idx := strings.Index(s, " ")
		if idx == -1 {
			return s
		}
		assignment := s[:idx]
		if eq := strings.Index(assignment, "="); eq > 0 {
			name := strings.ToLower(strings.TrimSpace(assignment[:eq]))
			if buildAffectingEnv[name] && envKey != nil {
				*envKey = append(*envKey, assignment)
			}
		}
		s = strings.TrimSpace(s[idx:])
	}
	return s
}

func detectBuildTestCommand(cmd string) (bool, string, string) {
	c := strings.ToLower(strings.TrimSpace(cmd))
	if c == "" {
		return false, "", ""
	}

	var envKey []string
	for _, rawLine := range strings.Split(c, "\n") {
		trimmed := strings.TrimSpace(rawLine)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		c = stripEnvVarsKeepBuildEnv(trimmed, &envKey)
		break
	}

	for _, pat := range buildTestPatterns {
		if strings.HasPrefix(c, pat.prefix) {
			return true, pat.label, strings.Join(envKey, " ") + "|" + c
		}
	}
	return false, "", ""
}

func (s *buildIdempotencyState) recordToolCall(toolName string, args json.RawMessage, iteration int) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	if sourceMutatingTools[toolName] {
		s.editsSinceLastBuild++
		return ""
	}

	if toolName != "run_command" && toolName != "start_command" {
		return ""
	}

	cmd := extractStringField(args, "command")
	if cmd == "" {
		return ""
	}

	if shellMutatesSources(cmd) {
		s.editsSinceLastBuild++
	}

	isBuild, label, cmdKey := detectBuildTestCommand(cmd)
	if !isBuild {
		return ""
	}

	warning := ""

	if s.lastBuildIter > 0 && s.editsSinceLastBuild == 0 && cmdKey == s.lastBuildCmd {
		s.totalRedundant++
		if s.warnsIssued < s.maxWarns {
			s.warnsIssued++
			debug.Log("agent", "Iteration %d: build idempotency violation: %s re-run with 0 edits since iter %d",
				iteration, label, s.lastBuildIter)
			warning = formatIdempotencyWarning(label, s.lastBuildIter, iteration, s.totalRedundant)
		}
	}

	s.lastBuildIter = iteration
	s.lastBuildCmd = cmdKey
	s.editsSinceLastBuild = 0

	return warning
}

func (s *buildIdempotencyState) markBuildFailed(cmd string) {
	if s == nil || cmd == "" {
		return
	}
	if isBuild, _, cmdKey := detectBuildTestCommand(cmd); isBuild && cmdKey == s.lastBuildCmd {
		s.lastBuildCmd = ""
	}
}

func shellMutatesSources(cmd string) bool {
	lower := strings.ToLower(cmd)
	for _, pat := range []string{
		"gofmt -w", "gofmt -l -w", "goimports -w",
		"go fmt ",
		"sed -i", "git apply", "patch -p",
		"go mod tidy", "go mod get", "go get ",
		"gofumpt -w", "prettier --write", "eslint --fix",
		"rustfmt", "black ", "isort ", "autopep8",
	} {
		if strings.Contains(lower, pat) {
			return true
		}
	}

	if lower == "go fmt" {
		return true
	}

	toks := strings.Fields(lower)
	for i, tok := range toks {
		redirect := ""
		switch {
		case tok == ">" || tok == ">>":
			if i+1 < len(toks) {
				redirect = toks[i+1]
			}

		case tok == "&>" || tok == ">&":

			if i+1 < len(toks) {
				redirect = toks[i+1]
			}
		case (tok == "2>" || tok == "1>") && i+1 < len(toks):
			redirect = toks[i+1]
		case tok == "2>>" && i+1 < len(toks):
			redirect = toks[i+1]
		case strings.HasPrefix(tok, ">"):
			redirect = tok[1:]
		case strings.HasPrefix(tok, "2>") && len(tok) > 2:
			redirect = tok[2:]
		case strings.HasPrefix(tok, "1>") && len(tok) > 2:
			redirect = tok[2:]
		case strings.HasPrefix(tok, "&>") && len(tok) > 2:
			redirect = tok[2:]
		}
		if redirect == "" {
			continue
		}
		if strings.HasPrefix(redirect, "/dev/") {
			continue
		}
		if strings.ContainsAny(redirect, "\"'") {
			continue
		}
		if strings.Contains(redirect, ".") || strings.Contains(redirect, "/") {
			return true
		}
	}
	for _, pat := range []string{
		"tee ",
		"cp ", "mv ",
		"git restore",

		"git checkout",
		"git switch",
		"git pull", "git merge",
		"git stash",

		"curl ", "wget ", "rsync ", "scp ",
		"tar ", "unzip ",

		"go generate",
		"clang-format -i", "perl -pi", "perl -i",
		" of=",
		"install ",
		"patch <",
	} {
		if strings.Contains(lower, pat) {
			return true
		}
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
