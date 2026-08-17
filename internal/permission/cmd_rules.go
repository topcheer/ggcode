package permission

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
)

// CommandRuleSet stores user-approved command patterns for fine-grained
// permission control. Instead of allowing ALL commands for run_command,
// users can allow specific patterns like "git diff:*" or "go build:*".
//
// This is inspired by Claude Code's Bash(git diff:*) permission rules
// and Cursor's per-command approval system.
type CommandRuleSet struct {
	mu        sync.RWMutex
	allow     []*regexp.Regexp
	deny      []*regexp.Regexp
	allowPats []string // original patterns for display/persistence
	denyPats  []string // original patterns for display/persistence
}

// NewCommandRuleSet creates an empty rule set.
func NewCommandRuleSet() *CommandRuleSet {
	return &CommandRuleSet{}
}

// NewCommandRuleSetFromLists creates a rule set from allow/deny pattern lists.
func NewCommandRuleSetFromLists(allow, deny []string) *CommandRuleSet {
	rs := &CommandRuleSet{}
	for _, p := range allow {
		if re, err := compileCommandPattern(p); err == nil {
			rs.allow = append(rs.allow, re)
			rs.allowPats = append(rs.allowPats, p)
		} else {
			debug.Log("permission", "invalid command allow pattern %q: %v", p, err)
		}
	}
	for _, p := range deny {
		if re, err := compileCommandPattern(p); err == nil {
			rs.deny = append(rs.deny, re)
			rs.denyPats = append(rs.denyPats, p)
		} else {
			debug.Log("permission", "invalid command deny pattern %q: %v", p, err)
		}
	}
	return rs
}

// Check returns the decision for a given command string.
// Deny patterns take precedence over allow patterns.
// Returns (Allow, true) if an allow rule matched, (Deny, true) if a deny rule
// matched, (Ask, false) if no rule matched.
func (rs *CommandRuleSet) Check(command string) (Decision, bool) {
	if rs == nil || strings.TrimSpace(command) == "" {
		return Ask, false
	}
	rs.mu.RLock()
	defer rs.mu.RUnlock()

	// Deny patterns always take precedence — safety first.
	for _, re := range rs.deny {
		if re.MatchString(command) {
			return Deny, true
		}
	}
	for _, re := range rs.allow {
		if re.MatchString(command) {
			return Allow, true
		}
	}
	// #573-D: env-prefixed commands ("BAR=1 make build") should match rules
	// saved for the bare command ("make*"); without this the always-allow for
	// env-prefixed invocations structurally never fired.
	if stripped := stripLeadingEnvAssignments(command); stripped != "" && stripped != command {
		for _, re := range rs.deny {
			if re.MatchString(stripped) {
				return Deny, true
			}
		}
		for _, re := range rs.allow {
			if re.MatchString(stripped) {
				return Allow, true
			}
		}
	}
	return Ask, false
}

// AddAllowPattern adds an allow pattern at runtime (from TUI "always allow"
// for a specific command). The pattern is compiled and stored.
func (rs *CommandRuleSet) AddAllowPattern(pattern string) {
	if rs == nil || strings.TrimSpace(pattern) == "" {
		return
	}
	re, err := compileCommandPattern(pattern)
	if err != nil {
		debug.Log("permission", "invalid command pattern %q: %v", pattern, err)
		return
	}
	rs.mu.Lock()
	// Avoid duplicates
	for _, existing := range rs.allow {
		if existing.String() == re.String() {
			rs.mu.Unlock()
			return
		}
	}
	rs.allow = append(rs.allow, re)
	rs.allowPats = append(rs.allowPats, pattern)
	rs.mu.Unlock()
}

// AddDenyPattern adds a deny pattern at runtime.
func (rs *CommandRuleSet) AddDenyPattern(pattern string) {
	if rs == nil || strings.TrimSpace(pattern) == "" {
		return
	}
	re, err := compileCommandPattern(pattern)
	if err != nil {
		debug.Log("permission", "invalid command pattern %q: %v", pattern, err)
		return
	}
	rs.mu.Lock()
	for _, existing := range rs.deny {
		if existing.String() == re.String() {
			rs.mu.Unlock()
			return
		}
	}
	rs.deny = append(rs.deny, re)
	rs.denyPats = append(rs.denyPats, pattern)
	rs.mu.Unlock()
}

// AllowPatterns returns the current allow patterns as strings (for display).
func (rs *CommandRuleSet) AllowPatterns() []string {
	if rs == nil {
		return nil
	}
	rs.mu.RLock()
	defer rs.mu.RUnlock()
	return append([]string(nil), rs.allowPats...)
}

// DenyPatterns returns the current deny patterns as strings (for display).
func (rs *CommandRuleSet) DenyPatterns() []string {
	if rs == nil {
		return nil
	}
	rs.mu.RLock()
	defer rs.mu.RUnlock()
	return append([]string(nil), rs.denyPats...)
}

// Clear removes all rules.
func (rs *CommandRuleSet) Clear() {
	if rs == nil {
		return
	}
	rs.mu.Lock()
	rs.allow = nil
	rs.deny = nil
	rs.allowPats = nil
	rs.denyPats = nil
	rs.mu.Unlock()
}

// commandRuleFile is the JSON representation for persistence.
type commandRuleFile struct {
	Allow []string `json:"allow"`
	Deny  []string `json:"deny"`
}

// Save persists the rule set to a JSON file.
func (rs *CommandRuleSet) Save(path string) error {
	if rs == nil {
		return nil
	}
	rs.mu.RLock()
	data := commandRuleFile{
		Allow: append([]string(nil), rs.allowPats...),
		Deny:  append([]string(nil), rs.denyPats...),
	}
	rs.mu.RUnlock()

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0644)
}

// compileCommandPattern converts a user-friendly pattern into a compiled regex.
//
// Pattern syntax:
//
//	"git diff:*"     → matches "git diff" followed by anything
//	"npm test"       → matches "npm test" exactly (or as a prefix)
//	"go build"       → matches "go build" and "go build ./..."
//	"make*"          → matches "make", "make build", "make test", etc.
//
// The pattern is converted to a case-insensitive regex anchored at the start
// of the command. The "*" wildcard matches any characters.
func compileCommandPattern(pattern string) (*regexp.Regexp, error) {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return nil, ErrEmptyPattern
	}
	// Escape regex special chars except our wildcard *
	var sb strings.Builder
	sb.WriteString("(?i)^") // anchor at start
	for _, ch := range pattern {
		if ch == '*' {
			// A trailing wildcard must not swallow command chaining:
			// "git diff*" matching "git diff; curl ..." lets anything ride
			// in past the semicolon. Restrict the wildcard to a single
			// command: no shell control characters (; | & ` $( ) newline) and
			// no redirection characters (< > \) — "curl URL* < ~/.ssh/id_rsa"
			// would otherwise ride an allow-rule and exfiltrate the file's
			// contents as the request body (#373).
			sb.WriteString(`[^;|&` + "`" + `$()<>\n\r\\]*`)
		} else {
			// Escape regex metacharacters
			if strings.ContainsRune(`\.+?()|[]{}^$`, ch) {
				sb.WriteByte('\\')
			}
			sb.WriteRune(ch)
		}
	}
	if strings.Contains(pattern, "*") {
		sb.WriteString("$") // anchor at end — without this, 'git status' would
		// match 'git status; rm -rf /' (command chaining injection)
	} else {
		// #573-A: no wildcard — the documented contract (see examples above)
		// is that "go build" matches "go build ./...": a prefix of further
		// arguments, never a prefix of a chained command. Require
		// end-of-string or an argument boundary (space/tab), and keep the same
		// control-character exclusion as the wildcard so chaining cannot ride
		// the rule ("go build; rm -rf /" must not match).
		sb.WriteString(`(?:[ \t][^;|&` + "`" + `$()<>\n\r\\]*)?$`)
	}
	return regexp.Compile(sb.String())
}

// ErrEmptyPattern is returned when an empty pattern is compiled.
var ErrEmptyPattern = &patternError{"command pattern is empty"}

type patternError struct{ msg string }

func (e *patternError) Error() string { return e.msg }

// CommandPrefixToPattern generates a permission pattern from a command string.
// It extracts the command prefix (first 1-2 words) and appends a wildcard.
//
// Examples:
//
//	"git diff --stat"  → "git diff*"
//	"npm test"         → "npm test*"
//	"go build -tags goolm ./..." → "go build*"
//	"make"             → "make*"
//	"ls -la"           → "ls*"
//	`FOO="a b" make`   → "make*" (quote-aware env stripping, #573-D)
func CommandPrefixToPattern(command string) string {
	command = strings.TrimSpace(command)
	if command == "" {
		return ""
	}
	// Remove leading env var assignments like "FOO=bar" or `FOO="a b"`
	// (#573-D: the old space-split produced garbage patterns for quoted
	// values containing spaces).
	command = stripLeadingEnvAssignments(command)
	tokens := strings.Fields(command)
	if len(tokens) == 0 {
		return ""
	}
	// Two-word commands: "git diff", "go build", "npm test", "go test", etc.
	// One-word commands: "make", "ls", "pwd", etc.
	prefix := tokens[0]
	if len(tokens) > 1 && isCommonTwoWordPrefix(tokens[0], tokens[1]) {
		prefix = tokens[0] + " " + tokens[1]
	}
	return prefix + "*"
}

// stripLeadingEnvAssignments removes leading environment variable
// assignments (NAME=VALUE, $VAR) from a command string. VALUE may be
// single- or double-quoted with embedded whitespace (#573-D).
func stripLeadingEnvAssignments(command string) string {
	for {
		command = strings.TrimLeft(command, " \t")
		if command == "" {
			return ""
		}
		if command[0] == '$' {
			if idx := strings.IndexAny(command, " \t"); idx > 0 {
				command = command[idx+1:]
				continue
			}
			return command
		}
		eq := strings.IndexByte(command, '=')
		if eq <= 0 {
			return command
		}
		if !isValidEnvName(command[:eq]) {
			return command
		}
		rest := command[eq+1:]
		if rest == "" {
			return ""
		}
		switch rest[0] {
		case '"', '\'':
			q := rest[0]
			end := strings.IndexByte(rest[1:], q)
			if end < 0 {
				// Unterminated quote — not ours to parse; treat as command.
				return command
			}
			after := rest[end+2:]
			if after == "" {
				return ""
			}
			if after[0] == ' ' || after[0] == '\t' {
				command = after
				continue
			}
			return command
		default:
			idx := strings.IndexAny(rest, " \t")
			if idx < 0 {
				return ""
			}
			command = rest[idx+1:]
			continue
		}
	}
}

// isValidEnvName reports whether s is a valid shell env var name
// ([A-Za-z_][A-Za-z0-9_]*).
func isValidEnvName(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '_':
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z':
		case c >= '0' && c <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
}

// isCommonTwoWordPrefix returns true if the two-word combination is a common
// command prefix worth treating as a unit (e.g., "git diff" vs "git push").
func isCommonTwoWordPrefix(first, second string) bool {
	commonFirsts := map[string]bool{
		"git": true, "go": true, "npm": true, "yarn": true, "pnpm": true,
		"docker": true, "kubectl": true, "cargo": true, "rustup": true,
		"python": true, "python3": true, "ruby": true, "bundle": true,
		"make": false, // "make" is usually one word
	}
	if isCommon, ok := commonFirsts[first]; ok {
		return isCommon
	}
	// For other commands, only group if second word is not a flag
	return !strings.HasPrefix(second, "-")
}

// ExtractCommandFromInput parses a tool input JSON string and extracts the
// command field. Returns "" if the input is not valid JSON or has no command.
// This is the exported version of the same logic used internally by the TUI.
func ExtractCommandFromInput(input string) string {
	input = strings.TrimSpace(input)
	if input == "" {
		return ""
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(input), &m); err != nil {
		return ""
	}
	for _, key := range []string{"command", "input"} {
		if v, ok := m[key]; ok {
			var s string
			if err := json.Unmarshal(v, &s); err == nil {
				return s
			}
		}
	}
	return ""
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
