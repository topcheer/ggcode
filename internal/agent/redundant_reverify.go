package agent

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	redundantReverifyMaxWarnings = 2
)

type redundantReverifyState struct {
	warnings int
	lastRun  map[string]*reverifyRun
}

type reverifyRun struct {
	toolName    string
	iteration   int
	editsSince  int
	resultError bool
	signature   string
}

var reverifyCmdPatterns = map[string]*regexp.Regexp{
	"build":     regexp.MustCompile(`(?i)\b(go\s+build|make\s+build|cargo\s+build|npm\s+run\s+build|yarn\s+build|pnpm\s+build|cmake\b|meson\b)\b`),
	"test":      regexp.MustCompile(`(?i)\b(go\s+test|make\s+test|cargo\s+test|npm\s+test|npm\s+run\s+test|yarn\s+test|pnpm\s+test|pytest\b|jest\b|vitest\b|mocha\b)\b`),
	"lint":      regexp.MustCompile(`(?i)\b(go\s+vet|golangci-lint|eslint|prettier|clang-tidy|flake8|pylint|rubocop|make\s+lint)\b`),
	"typecheck": regexp.MustCompile(`(?i)\b(tsc\s+--noEmit|tsc\s+-p|mypy\b|pyright\b|pyre\b)\b`),
	"fmtcheck":  regexp.MustCompile(`(?i)\b(gofmt\s+-l|goimports\s+-l|rustfmt\s+--check)\b`),
}

var reverifyEditTools = sourceMutatingTools

func newRedundantReverifyState() *redundantReverifyState {
	return &redundantReverifyState{
		lastRun: make(map[string]*reverifyRun),
	}
}

func (s *redundantReverifyState) reset() {
	s.warnings = 0
	s.lastRun = make(map[string]*reverifyRun)
}

var reverifyTextToolFirstWords = map[string]bool{
	"grep": true, "rg": true, "sed": true, "awk": true, "echo": true, "cat": true,
	"printf": true, "tail": true, "head": true, "less": true, "sort": true,
	"uniq": true, "wc": true, "tr": true, "cut": true, "tee": true, "xargs": true,
	"man": true, "which": true, "type": true, "find": true, "ls": true,

	"git": true, "docker": true, "kubectl": true, "podman": true,
}

func firstPipelineSegment(args string) string {
	seg := args
	for _, sep := range []string{"|", ";", "&&", "||"} {
		if i := strings.Index(seg, sep); i >= 0 {
			seg = seg[:i]
		}
	}
	return seg
}

var reverifySegmentSeps = regexp.MustCompile(`\|\||&&|[|;]`)

func commandSegments(args string) []string {
	var segs []string
	for _, seg := range reverifySegmentSeps.Split(args, -1) {
		if s := strings.TrimSpace(seg); s != "" {
			segs = append(segs, s)
		}
	}
	return segs
}

var reverifyEnvAssignRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)

func stripLeadingPrefixes(fields []string) []string {
	for len(fields) > 0 {
		if strings.HasPrefix(fields[0], "$(") {

			for len(fields) > 0 {
				first := fields[0]
				fields = fields[1:]
				if strings.Contains(first, ")") {
					break
				}
			}
			continue
		}
		if fields[0] == "cd" && len(fields) >= 2 {
			fields = fields[2:]
			continue
		}
		if reverifyEnvAssignRe.MatchString(fields[0]) {
			fields = fields[1:]
			continue
		}
		break
	}
	return fields
}

func extractShellCommand(args string) string {
	var wrapper struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal([]byte(args), &wrapper); err == nil && wrapper.Command != "" {
		return wrapper.Command
	}
	return args
}

func verificationSignature(args string) string {
	args = extractShellCommand(args)
	chosen := ""
	for _, seg := range commandSegments(args) {
		for _, re := range reverifyCmdPatterns {
			if re.MatchString(seg) {
				chosen = seg
				break
			}
		}
		if chosen != "" {
			break
		}
	}
	if chosen == "" {
		chosen = args
	}
	fields := stripLeadingPrefixes(strings.Fields(chosen))
	return strings.ToLower(strings.Join(fields, " "))
}

func (s *redundantReverifyState) classifyVerificationCommand(toolName, args string) string {

	if toolName != "run_command" && toolName != "start_command" {
		return ""
	}

	args = extractShellCommand(args)

	fields := strings.Fields(firstPipelineSegment(args))
	for len(fields) > 0 && strings.HasPrefix(fields[0], "$(") {
		fields = fields[1:]
	}
	if len(fields) > 0 {
		bin := filepath.Base(fields[0])
		if reverifyTextToolFirstWords[bin] {
			return ""
		}
	}

	combined := toolName + " " + stripQuotedSpans(args)
	for cat, re := range reverifyCmdPatterns {
		if re.MatchString(combined) {
			return cat
		}
	}
	return ""
}

func stripQuotedSpans(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			b.WriteByte(' ')
			continue
		}
		if (c == '"' || c == '\'') && i > 0 && s[i-1] != '\\' {
			quote = c
			b.WriteByte(' ')
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

func (s *redundantReverifyState) recordToolCall(toolName, args string, iteration int, resultError bool) string {
	cat := s.classifyVerificationCommand(toolName, args)
	if cat == "" {
		return ""
	}

	sig := verificationSignature(args)

	prev := s.lastRun[cat]

	s.lastRun[cat] = &reverifyRun{
		toolName:    toolName,
		iteration:   iteration,
		editsSince:  0,
		resultError: resultError,
		signature:   sig,
	}

	if prev != nil && prev.editsSince == 0 && !prev.resultError && prev.signature == sig {
		if s.warnings >= redundantReverifyMaxWarnings {
			return ""
		}
		s.warnings++
		hint := fmt.Sprintf(`[Redundant Re-verification] You ran a "%s" verification at iteration %d and it passed. No files have been modified since. Running it again cannot produce new information -- verification is idempotent when no source files change (SICA, NeurIPS 2025). Either make the code change first, or if verification is already done, proceed to the next task step.

Previous run: %s (iteration %d, passed)
This run: %s (iteration %d)`,
			cat, prev.iteration, prev.toolName, prev.iteration, toolName, iteration)
		debug.Log("redundant-reverify", "category=%s prev_iter=%d curr_iter=%d edits_since=%d",
			cat, prev.iteration, iteration, prev.editsSince)
		return hint
	}

	return ""
}

func (s *redundantReverifyState) recordEdit(toolName string) {

	if !mutatesSourceTree(toolName) {
		return
	}
	for _, run := range s.lastRun {
		run.editsSince++
	}
}

func (s *redundantReverifyState) recordShellSourceMutation() {
	for _, run := range s.lastRun {
		run.editsSince++
	}
}

func (a *Agent) maybeWarnRedundantReverify(_ string) string {
	return ""
}
