package agent

import (
	"encoding/json"
	"strings"
	"sync"
	"unicode"

	"github.com/topcheer/ggcode/internal/debug"
)

type reversibilityState struct {
	mu          sync.Mutex
	warnCount   int
	maxWarnings int

	testsRan    bool
	buildRan    bool
	stagingSeen bool
}

func newReversibilityState() *reversibilityState {
	return &reversibilityState{
		maxWarnings: 2,
	}
}

func (r *reversibilityState) reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.warnCount = 0
	r.testsRan = false
	r.buildRan = false
	r.stagingSeen = false
}

func (r *reversibilityState) recordSafetySignal(toolName, args string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	switch toolName {
	case "run_command":
		tokens := commandTokens(args)

		cmd0 := ""
		if len(tokens) > 0 {
			cmd0 = tokens[0]
			if cmd0 == "command" && len(tokens) > 1 {
				cmd0 = tokens[1]
			}
		}
		switch cmd0 {
		case "go", "npm", "yarn", "pnpm", "cargo", "python", "python3":
			if hasCommandToken(tokens[1:], "test", "pytest", "vitest", "jest") {
				r.testsRan = true
			}
			if hasCommandToken(tokens[1:], "build") {
				r.buildRan = true
			}
		case "make", "build":

			bareMake := len(tokens) <= 1 || (tokens[0] == "command" && len(tokens) == 2)
			if cmd0 == "build" || bareMake || hasCommandToken(tokens[1:], "build", "all", "verify", "verify-ci") {
				r.buildRan = true
			}
			if hasCommandToken(tokens[1:], "test", "check") {
				r.testsRan = true
			}
		case "pytest", "py.test", "vitest", "jest":

			r.testsRan = true
		}
	case "git_add", "git_commit":
		r.stagingSeen = true
	}
}

func commandTokens(args string) []string {
	cmd := args
	if strings.HasPrefix(cmd, "#") {
		if idx := strings.IndexByte(cmd, '\n'); idx >= 0 {
			cmd = cmd[idx+1:]
		} else {
			cmd = ""
		}
	}
	trimmed := strings.TrimSpace(cmd)
	isEnvelope := strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[")
	if isEnvelope {
		var env struct {
			Command string `json:"command"`
		}
		if err := json.Unmarshal([]byte(trimmed), &env); err == nil && env.Command != "" {
			return commandTokens(env.Command)
		}
	}
	cut := func(r rune) bool {
		switch r {
		case '{', '}', '[', ']', '"', ',', ';':
			return true
		case ':':
			return isEnvelope
		}
		return unicode.IsSpace(r)
	}
	return strings.FieldsFunc(strings.ToLower(cmd), cut)
}

func hasCommandToken(tokens []string, words ...string) bool {
	for _, tok := range tokens {
		for _, w := range words {
			if tok == w {
				return true
			}
		}
	}
	return false
}

func (r *reversibilityState) checkPreAction(toolName, args string) string {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.warnCount >= r.maxWarnings {
		return ""
	}

	lowerArgs := strings.ToLower(args)

	switch toolName {
	case "git_commit":

		if !r.testsRan && !r.buildRan && !r.stagingSeen {
			r.warnCount++
			debug.Log("agent", "reversibility: git_commit without prior verification (run #%d)", r.warnCount)
			return "[reversibility] Committing without tests/build. Run verification first."
		}

	case "run_command":

		if isGitPush(lowerArgs) && !r.testsRan && !r.buildRan {
			r.warnCount++
			debug.Log("agent", "reversibility: git push without prior verification (run #%d)", r.warnCount)
			return "[reversibility] Pushing without local tests/build. Verify first."
		}

		if isDestructiveGit(lowerArgs) {
			r.warnCount++
			debug.Log("agent", "reversibility: destructive git command detected (run #%d)", r.warnCount)
			return "[reversibility] Destructive git command. Consider `git stash` first."
		}

	case "file_ops":

		if strings.Contains(lowerArgs, `"action":"delete"`) || strings.Contains(lowerArgs, `"action": "delete"`) {
			r.warnCount++
			debug.Log("agent", "reversibility: file_ops delete detected (run #%d)", r.warnCount)
			return "[reversibility] Deleting file. Verify no references first."
		}
	}

	return ""
}

func isGitPush(s string) bool {
	tokens := commandTokens(s)

	tokens = stripGitGlobalFlagTokens(tokens)
	for i := 0; i+1 < len(tokens); i++ {

		if strings.Trim(tokens[i], "\"'") == "git" && strings.HasPrefix(strings.Trim(tokens[i+1], "\"'"), "push") {
			return true
		}
	}
	return false
}

func isDestructiveGit(s string) bool {

	for _, sub := range strings.FieldsFunc(s, func(r rune) bool { return r == ';' || r == '\n' }) {
		if isDestructiveGitSub(sub) {
			return true
		}
	}
	return false
}

func isDestructiveGitSub(s string) bool {
	tokens := commandTokens(s)

	tokens = stripGitGlobalFlagTokens(tokens)

	for i := 0; i+1 < len(tokens); i++ {
		if strings.Trim(tokens[i], "\"'") != "git" {
			continue
		}
		sub := tokens[i+1]
		for _, u := range tokens[i+1:] {
			switch u {
			case "&&", "||", "|", "&":
				goto nextGit
			}
			if u == ";" || strings.HasPrefix(u, ";") {
				goto nextGit
			}

			u = strings.TrimRight(u, "\"'\\")
			switch sub {
			case "reset":
				if u == "--hard" {
					return true
				}
			case "clean":

				if u == "--force" || (len(u) > 1 && u[0] == '-' && u[1] == 'f') {
					return true
				}
			case "checkout":
				if u == "--" {
					return true
				}
			}
		}
	nextGit:
	}
	return false
}

func stripGitGlobalFlagTokens(tokens []string) []string {

	for i := 0; i < len(tokens); i++ {
		if strings.Trim(tokens[i], "\"'") != "git" {
			continue
		}
		j := i + 1
		for j < len(tokens) {
			ft := strings.Trim(tokens[j], "\"'")
			if !isGitGlobalFlag(ft) {
				break
			}
			j++
			if !strings.Contains(ft, "=") && j < len(tokens) {
				j++
			}
		}
		if j > i+1 && j <= len(tokens) {

			tokens = append(append([]string{}, tokens[:i+1]...), tokens[j:]...)
		}
	}
	return tokens
}
