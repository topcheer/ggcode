package agent

import (
	"encoding/json"
	"regexp"
	"strings"
	"sync"
)

// editAbandonState detects when the agent has edited files but then shifted
// its attention to entirely different files without verifying the edits.
//
// This is inspired by the PASTE (Pattern-Aware Speculative Tool Execution)
// framework (Sui et al., March 2026) and LLMCompiler (ICML 2024), which show
// that coding agent tool sequences follow predictable patterns:
// read -> edit -> build -> test. When the agent breaks this pattern by
// editing files then pivoting to unrelated exploration, the unverified edits
// are likely to be forgotten -- a qualitative attention-shift failure that
// simple count-based verification-debt detectors cannot capture.
//
// Key distinction from verification_debt:
//   - verification_debt: quantitative (warns at 5+ total unverified edits)
//   - edit_abandon: qualitative (warns when ATTENTION shifts away from
//     edited files to unrelated files, regardless of edit count)
//
// Zero LLM cost. Non-blocking.

const (
	eaMinEditedFiles    = 2 // need at least 2 edited files to be "abandonable"
	eaMinAttentionShift = 3 // 3+ consecutive tool calls on non-edited files
	eaMaxWarnings       = 2
	eaMinTotalCalls     = 6
)

type editAbandonState struct {
	mu sync.Mutex

	// files that have been modified but not yet verified
	editedFiles map[string]bool

	// count of consecutive tool calls targeting files NOT in editedFiles
	consecutiveNonEdit int

	totalCalls int
	warnings   int
	fired      bool
}

func newEditAbandonState() *editAbandonState {
	return &editAbandonState{
		editedFiles: make(map[string]bool),
	}
}

func (s *editAbandonState) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.editedFiles = make(map[string]bool)
	s.consecutiveNonEdit = 0
	s.totalCalls = 0
	s.warnings = 0
	s.fired = false
}

// eaPathRe extracts the "path" or "file" field from a JSON tool argument string.
var eaPathRe = regexp.MustCompile(`"(?:path|file|file_path)"\s*:\s*"([^"]+)"`)

// extractEAPaths pulls file paths from tool call arguments via regex.
func extractEAPaths(args string) []string {
	if args == "" {
		return nil
	}
	matches := eaPathRe.FindAllStringSubmatch(args, -1)
	paths := make([]string, 0, len(matches))
	for _, m := range matches {
		paths = append(paths, m[1])
	}
	return paths
}

// eaIsEditTool is derived from the canonical sourceMutatingTools superset (#738).
func eaIsEditTool(tool string) bool {
	return sourceMutatingTools[tool]
}

func eaIsReadTool(tool string) bool {
	switch tool {
	case "read_file", "multi_file_read", "grep", "search_files",
		"lsp_symbols", "lsp_definition", "lsp_references", "lsp_hover",
		"lsp_diagnostics", "lsp_document_highlights", "lsp_code_actions":
		return true
	}
	return false
}

func eaIsVerifyTool(tool, args string) bool {
	switch tool {
	case "lsp_diagnostics", "lsp_references", "code_health":
		return true
	case "run_command":
		return isVerificationCommand(eaExtractCommand(args))
	}
	return false
}

func eaExtractCommand(args string) string {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(args), &raw); err != nil {
		return ""
	}
	if cmd, ok := raw["command"]; ok {
		var s string
		if json.Unmarshal(cmd, &s) == nil {
			return s
		}
	}
	return ""
}

// recordToolCall updates the abandonment state based on the tool called.
func (s *editAbandonState) recordToolCall(tool, args string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.totalCalls++

	if eaIsVerifyTool(tool, args) {
		s.editedFiles = make(map[string]bool)
		s.consecutiveNonEdit = 0
		s.fired = false
		return
	}

	if eaIsEditTool(tool) {
		for _, p := range extractEAPaths(args) {
			if p != "" {
				s.editedFiles[p] = true
			}
		}
		s.consecutiveNonEdit = 0
		s.fired = false
		return
	}

	if eaIsReadTool(tool) {
		paths := extractEAPaths(args)
		if len(paths) > 0 {
			hitEdited := false
			for _, p := range paths {
				if s.editedFiles[p] {
					hitEdited = true
					break
				}
			}
			if hitEdited {
				s.consecutiveNonEdit = 0
			} else {
				s.consecutiveNonEdit++
			}
		} else {
			s.consecutiveNonEdit++
		}
		return
	}

	s.consecutiveNonEdit++
}

// maybeWarn returns a guidance message if edit abandonment is detected.
func (s *editAbandonState) maybeWarn() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.warnings >= eaMaxWarnings || s.fired {
		return ""
	}
	if len(s.editedFiles) < eaMinEditedFiles {
		return ""
	}
	if s.consecutiveNonEdit < eaMinAttentionShift {
		return ""
	}
	if s.totalCalls < eaMinTotalCalls {
		return ""
	}

	s.warnings++
	s.fired = true

	files := make([]string, 0, len(s.editedFiles))
	for f := range s.editedFiles {
		files = append(files, f)
	}

	var b strings.Builder
	b.WriteString("[edit-abandonment] You have ")
	b.WriteString(eaItoa(len(s.editedFiles)))
	b.WriteString(" unverified edit(s) (")
	if len(files) > 3 {
		b.WriteString(strings.Join(files[:3], ", "))
		b.WriteString(", ...")
	} else {
		b.WriteString(strings.Join(files, ", "))
	}
	b.WriteString(") but your last ")
	b.WriteString(eaItoa(s.consecutiveNonEdit))
	b.WriteString(" tool calls targeted unrelated files. Unverified edits risk being forgotten or left inconsistent. ")
	b.WriteString("Run a build/test or read the edited files to verify your changes before moving on.")

	return b.String()
}

func eaItoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	neg := n < 0
	if neg {
		n = -n
	}
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
