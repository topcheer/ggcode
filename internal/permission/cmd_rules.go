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
	// #596-P3: two wildcard shapes, deliberately different:
	//
	//  1. Prefix-glob ("make*", "git diff*") — trailing wildcard is the ONLY
	//     wildcard. "make*" matches "make" or "make build", NOT "makeevil"
	//     (word boundary), so a typo'd binary can't ride an allow rule.
	//
	//  2. Substring-glob ("curl*evil.example.com*") — contains an earlier
	//     wildcard; the trailing one is generic. The word boundary would
	//     break legitimate suffixes ("/payload" after the host), and the
	//     pattern's intent is substring containment, not command prefix.
	//
	// Both exclude shell control characters (;|&` $() < > newline CR \) so a
	// wildcard can never swallow command chaining. Hyphens ARE excluded in
	// prefix-glob patterns to enforce word boundaries (#596-P3): "make*"
	// matches "make" or "make build", NOT "makeevil". Flag chars like
	// "go build -tags" still match because they appear AFTER a space.
	// (#713: the separate controlPrefix class was removed — the only branch
	// that ever selected it was provably unreachable.)
	control := `[^;|&` + "`" + `$()<>\n\r\\]*`
	trailingOnly := strings.HasSuffix(pattern, "*") &&
		!strings.Contains(pattern[:len(pattern)-1], "*")
	var sb strings.Builder
	sb.WriteString("(?i)^") // anchor at start
	for _, ch := range pattern {
		if ch == '*' {
			// Substring-glob class. Prefix-glob wildcards never take this
			// path to production: the trailingOnly branch below re-emits the
			// wildcard as the optionalArgs word-boundary group. The old
			// HasSuffix(controlPrefix) probe was unreachable in every
			// construction path — escaped pattern literals can never
			// reproduce the class text, and no earlier write emits it — so
			// it is removed rather than kept as dead code under a comment
			// that implies prefix-globs get the hyphen-excluding class here
			// (#713). Word boundaries come from optionalArgs' mandatory
			// space, not from this class.
			sb.WriteString(control)
			continue
		}
		// Escape regex metacharacters
		if strings.ContainsRune(`\.+?()|[]{}^$`, ch) {
			sb.WriteByte('\\')
		}
		sb.WriteRune(ch)
	}
	// Optional-argument suffix shared by prefix-globs and no-wildcard
	// patterns: bare command ("make", "go build") or with space-prefixed
	// args ("make build", "go build ./..."), never "makeevil"/"go builds".
	optionalArgs := `(?:[ \t]` + control[:len(control)-1] + `*)?`
	if trailingOnly {
		// Drop the generic wildcard the loop just emitted and re-add it as
		// the optional argument group (word boundary).
		s := sb.String()
		sb.Reset()
		sb.WriteString(strings.TrimSuffix(s, control))
		sb.WriteString(optionalArgs)
	} else if !strings.Contains(pattern, "*") {
		// #573-A: no wildcard — "go build" matches "go build ./...": a
		// prefix of further ARGUMENTS, never a prefix of a chained command.
		sb.WriteString(optionalArgs)
	}
	sb.WriteString(`$`) // anchor at end — 'git status' must not match
	// 'git status; rm -rf /' (command chaining injection)
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
			rest, ok := stripEnvVarRef(command)
			if !ok {
				return command
			}
			command = rest
			continue
		}
		rest, ok := stripEnvAssignment(command)
		if !ok {
			return command
		}
		command = rest
	}
}

// stripEnvVarRef strips one leading $VAR or ${VAR} reference (#596-P1: only
// VALID references — $0, $@, $(), ${IFS} are left intact so they correctly
// fail to match any rule). ok is true only when a valid reference was
// consumed AND a non-space remainder follows: a bare "$FOO" is not a command
// and is reported unstripped.
func stripEnvVarRef(command string) (rest string, ok bool) {
	if len(command) > 1 && command[1] == '{' {
		// ${VAR} form — find closing brace
		if end := strings.IndexByte(command, '}'); end > 2 {
			if isValidEnvName(command[2:end]) {
				rest = command[end+1:]
			}
		}
	} else {
		// $VAR form — find end of variable name
		end := 1
		for end < len(command) {
			c := command[end]
			if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_' {
				end++
			} else {
				break
			}
		}
		if end > 1 && isValidEnvName(command[1:end]) {
			rest = command[end:]
		}
	}
	if rest == "" || rest == command {
		return "", false
	}
	rest = strings.TrimLeft(rest, " \t")
	if rest == "" {
		return "", false
	}
	return rest, true
}

// stripEnvAssignment strips one leading NAME=VALUE assignment. VALUE may be
// quoted with embedded whitespace (#573-D). ok is false when the token is
// not a parseable assignment (no '=', invalid name, unterminated quote, or
// value glued to the next token like FOO="a"bar) — the caller must then
// return the command unchanged. An empty rest means the assignment consumed
// the whole string.
func stripEnvAssignment(command string) (rest string, ok bool) {
	eq := strings.IndexByte(command, '=')
	if eq <= 0 || !isValidEnvName(command[:eq]) {
		return "", false
	}
	rest = command[eq+1:]
	if rest == "" {
		return "", true
	}
	if q := rest[0]; q == '"' || q == '\'' {
		end := strings.IndexByte(rest[1:], q)
		if end < 0 {
			// Unterminated quote — not ours to parse; treat as command.
			return "", false
		}
		after := rest[end+2:]
		if after == "" {
			return "", true
		}
		if after[0] != ' ' && after[0] != '\t' {
			// Value glued to the next token — keep the assignment visible.
			return "", false
		}
		return after, true
	}
	idx := strings.IndexAny(rest, " \t")
	if idx < 0 {
		return "", true
	}
	return rest[idx+1:], true
}

// isValidEnvName reports whether s is a valid shell env var name
// ([A-Za-z_][A-Za-z0-9_]*). Shell-SPECIAL variables whose expansion alters
// parsing are rejected even though they are valid names: ${IFS} expands to
// whitespace, so stripping "${IFS} " from "${IFS} make build" would turn a
// shell-injection payload into a command that rides a "make*" allow rule.
func isValidEnvName(s string) bool {
	if s == "" || s == "IFS" {
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
