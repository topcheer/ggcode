package config

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// DefaultMergeGateCommands lists the lifecycle commands the merge gate
// protects out of the box. Entries are command prefixes; a concrete
// invocation matches when its leading tokens equal the pattern and the
// next token is the PR number (e.g. "gh pr merge 5356").
var DefaultMergeGateCommands = []string{"gh pr merge", "gh pr close"}

// DefaultMergeGatePreflightTTL is how long a PREFLIGHT: PASS snapshot
// stays fresh for the merge gate before a new preflight is required.
const DefaultMergeGatePreflightTTL = 60 * time.Second

// MergeGatePreflightTTLRange bounds gates.merge.preflight_ttl.
const (
	MinMergeGatePreflightTTL = time.Second
	MaxMergeGatePreflightTTL = 10 * time.Minute
)

// GatesConfig groups completion-gate (lifecycle gate) settings. Gates are
// generic product capability: advisory REQUIREMENT prompts injected before
// lifecycle commands (currently PR merge/close) unless a fresh preflight
// PASS snapshot exists for that PR's head.
type GatesConfig struct {
	Merge MergeGateConfig `yaml:"merge,omitempty" json:"merge,omitempty"`
}

// MergeGateConfig configures the merge/close lifecycle gate. Zero values
// mean "use the default" so a minimal config file needs no gates section.
type MergeGateConfig struct {
	// Enabled toggles the gate; nil means enabled (default true).
	Enabled *bool `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	// Commands lists protected command prefixes. nil means the default
	// ["gh pr merge", "gh pr close"].
	Commands []string `yaml:"commands,omitempty" json:"commands,omitempty"`
	// HoldPatterns are regexes; a PR comment or label matching any of
	// them makes merge_preflight report HOLD. nil means no holds.
	HoldPatterns []string `yaml:"hold_patterns,omitempty" json:"hold_patterns,omitempty"`
	// PreflightTTL is how long a PASS snapshot stays fresh. Zero means
	// the 60s default.
	PreflightTTL time.Duration `yaml:"preflight_ttl,omitempty" json:"preflight_ttl,omitempty"`
}

// EffectiveEnabled reports whether the merge gate is on.
func (m MergeGateConfig) EffectiveEnabled() bool {
	if m.Enabled == nil {
		return true
	}
	return *m.Enabled
}

// EffectiveCommands returns the protected command prefixes in force.
func (m MergeGateConfig) EffectiveCommands() []string {
	if len(m.Commands) == 0 {
		out := make([]string, len(DefaultMergeGateCommands))
		copy(out, DefaultMergeGateCommands)
		return out
	}
	out := make([]string, len(m.Commands))
	copy(out, m.Commands)
	return out
}

// EffectivePreflightTTL returns the PASS snapshot freshness window.
func (m MergeGateConfig) EffectivePreflightTTL() time.Duration {
	if m.PreflightTTL <= 0 {
		return DefaultMergeGatePreflightTTL
	}
	return m.PreflightTTL
}

// EffectiveHoldPatterns returns the configured hold regexes as-is.
func (m MergeGateConfig) EffectiveHoldPatterns() []string {
	if len(m.HoldPatterns) == 0 {
		return nil
	}
	out := make([]string, len(m.HoldPatterns))
	copy(out, m.HoldPatterns)
	return out
}

// CompiledHoldPatterns compiles HoldPatterns, reporting the offending
// entry in the error so bad config lines are actionable.
func (m MergeGateConfig) CompiledHoldPatterns() ([]*regexp.Regexp, error) {
	if len(m.HoldPatterns) == 0 {
		return nil, nil
	}
	out := make([]*regexp.Regexp, 0, len(m.HoldPatterns))
	for i, p := range m.HoldPatterns {
		re, err := regexp.Compile(p)
		if err != nil {
			return nil, fmt.Errorf("gates.merge.hold_patterns[%d] %q: %w", i, p, err)
		}
		out = append(out, re)
	}
	return out, nil
}

// ValidateMergeGateConfig checks merge gate settings in force. It is
// called from Config.Validate on every Load so a broken hold regex or an
// out-of-range TTL fails config load with the offending entry named.
func (c *Config) ValidateMergeGateConfig() error {
	if c == nil {
		return nil
	}
	m := c.Gates.Merge
	if _, err := m.CompiledHoldPatterns(); err != nil {
		return err
	}
	for i, cmd := range m.Commands {
		if strings.TrimSpace(cmd) == "" {
			return fmt.Errorf("gates.merge.commands[%d] must not be empty", i)
		}
	}
	if m.PreflightTTL != 0 {
		if m.PreflightTTL < MinMergeGatePreflightTTL || m.PreflightTTL > MaxMergeGatePreflightTTL {
			return fmt.Errorf("gates.merge.preflight_ttl %s must be between %s and %s",
				m.PreflightTTL, MinMergeGatePreflightTTL, MaxMergeGatePreflightTTL)
		}
	}
	return nil
}

// MatchMergeGateCommand reports whether command matches any protected
// pattern and, if so, the PR number it references (#5401).
//
// A command matches when its leading tokens equal the pattern tokens
// (exact-token, case-sensitive), regardless of how the target PR is
// expressed afterwards - this mirrors the real gh CLI grammar
// `gh pr merge [<number> | <url> | <branch>] [flags]` where flags and
// positional arguments interleave freely:
//
//	gh pr merge 5356                 -> PR 5356
//	gh pr merge --auto --squash 5356  -> PR 5356 (flags skipped)
//	gh pr merge https://o/r/pull/5356-> PR 5356 (URL tail)
//	gh pr merge feature-branch        -> PR 0 (branch selector: gated,
//	                                    preflight must identify the PR)
//	gh pr merge                       -> PR 0 (no selector: gh acts on
//	                                    the current branch's PR)
//
// PR 0 means "gated but no explicit PR number" - the caller injects the
// requirement and asks the agent to identify the PR (e.g. via
// merge_preflight on the current branch). "gh pr merged 5356" still does
// not match (exact-token), and a command merely ECHOING the pattern
// ("echo gh pr merge 5356") does not match because matching is
// prefix-anchored within each command unit.
//
// #5407: compound commands are segmented first (&&, ||, ;, |, and
// newlines separate command units, quote- and escape-aware so separators
// inside string arguments do not split), and each unit is matched
// independently - "cd /dir && gh pr merge 5356" gates on its second unit
// while "echo x && echo gh pr merge 5356" still does not match (each
// echo unit starts with "echo", not the pattern).
func MatchMergeGateCommand(command string, patterns []string) (prNumber int, matched bool) {
	for _, unit := range splitCommandUnits(command) {
		fields := strings.Fields(unit)
		fields = stripEnvAssignments(fields)
		if len(fields) == 0 {
			continue
		}
		for _, pat := range patterns {
			pf := strings.Fields(strings.TrimSpace(pat))
			if len(pf) == 0 || len(fields) < len(pf) {
				continue
			}
			ok := true
			for i, tok := range pf {
				if fields[i] != tok {
					ok = false
					break
				}
			}
			if !ok {
				continue
			}
			// #5401: the pattern heads the unit; whatever follows selects
			// the target. Extract a PR number when one is expressible, else
			// still report a hit with PR 0.
			return extractPRNumber(fields[len(pf):]), true
		}
	}
	return 0, false
}

// stripEnvAssignments drops leading VAR=value tokens from a command
// unit (#5411): in `GH_TOKEN=x gh pr merge 5356` the assignment prefixes a
// REAL gh invocation, so the gated action must still match. An assignment
// token contains '=' and does not start with '-' (which keeps flag forms
// like --match-head-commit=sha untouched); stripping stops at the first
// non-assignment token, so `echo FOO=1` keeps its echo head and still does
// not match.
func stripEnvAssignments(fields []string) []string {
	i := 0
	for i < len(fields) && strings.Contains(fields[i], "=") && !strings.HasPrefix(fields[i], "-") {
		i++
	}
	return fields[i:]
}

// splitCommandUnits splits a (possibly compound) shell command into its
// command units: substrings that would each be executed as a command.
// Separators are &&, ||, ;, | (pipes) and newlines. Single- and
// double-quoted spans and backslash escapes are honored, so a separator
// inside a string argument does NOT split (echo "a && b" is one unit).
// Subshells/grouping are not parsed - a unit starting with "(" or "$("
// simply fails prefix matching, which is the safe (non-matching) side.
func splitCommandUnits(command string) []string {
	var (
		units []string
		cur   []byte
		flush = func() {
			if u := strings.TrimSpace(string(cur)); u != "" {
				units = append(units, u)
			}
			cur = cur[:0]
		}
	)
	for i := 0; i < len(command); i++ {
		c := command[i]
		switch {
		case c == '\\' && i+1 < len(command):
			// escaped char: keep both bytes verbatim (including escaped separators)
			cur = append(cur, c, command[i+1])
			i++
		case c == '\'' || c == '"':
			// quoted span: consume through the matching close quote, keep verbatim
			cur = append(cur, c)
			i++
			for i < len(command) && command[i] != c {
				if c == '"' && command[i] == '\\' && i+1 < len(command) {
					cur = append(cur, command[i], command[i+1])
					i++
				} else {
					cur = append(cur, command[i])
				}
				i++
			}
			if i < len(command) {
				cur = append(cur, c) // closing quote
			}
		case c == '&' && i+1 < len(command) && command[i+1] == '&',
			c == '|' && i+1 < len(command) && command[i+1] == '|',
			c == ';',
			c == '|',
			c == '\n':
			flush()
			if c == '&' || c == '|' {
				i++ // consume the second byte of && / ||
			}
		default:
			cur = append(cur, c)
		}
	}
	flush()
	return units
}

// extractPRNumber scans the tokens after a matched pattern for an
// explicit PR selector: the first non-flag token that is either a
// positive integer or a github pull-request URL (whose /pull/<n> tail
// carries the number). Branch names and flag-only forms yield 0.
func extractPRNumber(rest []string) int {
	for _, tok := range rest {
		if strings.HasPrefix(tok, "-") {
			continue // gh flag (--squash, --match-head-commit=..., -d)
		}
		if n, err := strconv.Atoi(tok); err == nil {
			if n > 0 {
				return n
			}
			continue
		}
		if strings.Contains(tok, "/pull/") {
			tail := tok[strings.LastIndex(tok, "/pull/")+len("/pull/"):]
			// tolerate trailing junk (query/hash) by trimming at the next '/'
			if i := strings.IndexAny(tail, "/?#"); i >= 0 {
				tail = tail[:i]
			}
			if n, err := strconv.Atoi(tail); err == nil && n > 0 {
				return n
			}
		}
		// first non-flag, non-numeric token is a branch selector - the
		// action is gated but has no explicit PR number
		return 0
	}
	return 0 // flag-only or no selector at all
}
