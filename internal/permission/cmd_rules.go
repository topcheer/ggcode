package permission

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/rexcache"
)

type CommandRuleSet struct {
	mu        sync.RWMutex
	allow     []*regexp.Regexp
	deny      []*regexp.Regexp
	allowPats []string
	denyPats  []string
}

// maxCommandPatternLen guards compileCommandPattern against runaway
// patterns. Field incident (2026-11-21, fleet-wide 2.5GB startup RSS):
// permission_rules.json accumulated two mojibake blobs (12.5MB and
// 1.2MB, UTF-8 double-encoding cascades) that compiled into ~540MB of
// regexp/syntax instructions PER SEAT - 80% of heap at boot. Real
// command patterns are short (<= a few hundred bytes); anything past
// 8KB is corruption, not intent.
const maxCommandPatternLen = 8 << 10

// compiledPatternCache memoizes pattern -> *regexp.Regexp process-wide.
// The same rules file is compiled once per agent (main + every restored
// teammate); regexp.Regexp is safe for concurrent use, so sharing one
// compiled object per pattern removes the per-agent duplication too.
var compiledPatternCache sync.Map // string -> *regexp.Regexp

func compileCommandPatternCached(pattern string) (*regexp.Regexp, error) {
	if re, ok := compiledPatternCache.Load(pattern); ok {
		return re.(*regexp.Regexp), nil
	}
	re, err := compileCommandPattern(pattern)
	if err != nil {
		return nil, err
	}
	compiledPatternCache.Store(pattern, re)
	return re, nil
}

func NewCommandRuleSet() *CommandRuleSet {
	return &CommandRuleSet{}
}

func NewCommandRuleSetFromLists(allow, deny []string) *CommandRuleSet {
	rs := &CommandRuleSet{}
	for _, p := range allow {
		if re, err := compileCommandPatternCached(p); err == nil {
			rs.allow = append(rs.allow, re)
			rs.allowPats = append(rs.allowPats, p)
		} else {
			debug.Log("permission", "invalid command allow pattern %q: %v", p, err)
		}
	}
	for _, p := range deny {
		if re, err := compileCommandPatternCached(p); err == nil {
			rs.deny = append(rs.deny, re)
			rs.denyPats = append(rs.denyPats, p)
		} else {
			debug.Log("permission", "invalid command deny pattern %q: %v", p, err)
		}
	}
	return rs
}

func (rs *CommandRuleSet) Check(command string) (Decision, bool) {
	if rs == nil || strings.TrimSpace(command) == "" {
		return Ask, false
	}
	rs.mu.RLock()
	defer rs.mu.RUnlock()

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

func (rs *CommandRuleSet) AddAllowPattern(pattern string) {
	if rs == nil || strings.TrimSpace(pattern) == "" {
		return
	}
	re, err := compileCommandPatternCached(pattern)
	if err != nil {
		debug.Log("permission", "invalid command pattern %q: %v", pattern, err)
		return
	}
	rs.mu.Lock()

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

func (rs *CommandRuleSet) AddDenyPattern(pattern string) {
	if rs == nil || strings.TrimSpace(pattern) == "" {
		return
	}
	re, err := compileCommandPatternCached(pattern)
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

func (rs *CommandRuleSet) AllowPatterns() []string {
	if rs == nil {
		return nil
	}
	rs.mu.RLock()
	defer rs.mu.RUnlock()
	return append([]string(nil), rs.allowPats...)
}

func (rs *CommandRuleSet) DenyPatterns() []string {
	if rs == nil {
		return nil
	}
	rs.mu.RLock()
	defer rs.mu.RUnlock()
	return append([]string(nil), rs.denyPats...)
}

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

type commandRuleFile struct {
	Allow []string `json:"allow"`
	Deny  []string `json:"deny"`
}

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

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func compileCommandPattern(pattern string) (*regexp.Regexp, error) {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return nil, ErrEmptyPattern
	}
	if len(pattern) > maxCommandPatternLen {
		debug.Log("permission", "REFUSING to compile command pattern of %d bytes (max %d) - mojibake/corruption guard, fleet 2.5GB-startup incident 2026-11-21", len(pattern), maxCommandPatternLen)
		return nil, ErrPatternTooLong
	}

	control := `[^;|&` + "`" + `$()<>\n\r\\]*`
	trailingOnly := strings.HasSuffix(pattern, "*") &&
		!strings.Contains(pattern[:len(pattern)-1], "*")
	var sb strings.Builder
	sb.WriteString("(?i)^")
	for _, ch := range pattern {
		if ch == '*' {

			sb.WriteString(control)
			continue
		}

		if strings.ContainsRune(`\.+?()|[]{}^$`, ch) {
			sb.WriteByte('\\')
		}
		sb.WriteRune(ch)
	}

	optionalArgs := `(?:[ \t]` + control[:len(control)-1] + `*)?`
	if trailingOnly {

		s := sb.String()
		sb.Reset()
		sb.WriteString(strings.TrimSuffix(s, control))
		sb.WriteString(optionalArgs)
	} else if !strings.Contains(pattern, "*") {

		sb.WriteString(optionalArgs)
	}
	sb.WriteString(`$`)

	return rexcache.Compile(sb.String())
}

var ErrEmptyPattern = &patternError{"command pattern is empty"}

var ErrPatternTooLong = &patternError{"command pattern exceeds " + strconv.Itoa(maxCommandPatternLen) + " bytes (mojibake/corruption guard)"}

type patternError struct{ msg string }

func (e *patternError) Error() string { return e.msg }

func CommandPrefixToPattern(command string) string {
	command = strings.TrimSpace(command)
	if command == "" {
		return ""
	}

	command = stripLeadingEnvAssignments(command)
	tokens := strings.Fields(command)
	if len(tokens) == 0 {
		return ""
	}

	prefix := tokens[0]
	if len(tokens) > 1 && isCommonTwoWordPrefix(tokens[0], tokens[1]) {
		prefix = tokens[0] + " " + tokens[1]
	}
	return prefix + "*"
}

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

func stripEnvVarRef(command string) (rest string, ok bool) {
	if len(command) > 1 && command[1] == '{' {

		if end := strings.IndexByte(command, '}'); end > 2 {
			if isValidEnvName(command[2:end]) {
				rest = command[end+1:]
			}
		}
	} else {

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
