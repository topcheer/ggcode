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
// pattern and, if so, the PR number it references. A command matches a
// pattern when the command's leading tokens equal the pattern tokens and
// the token right after the pattern is a positive integer (the PR number).
// Matching is exact-token (case-sensitive), so "gh pr merge 5356" matches
// "gh pr merge" but "gh pr merged 5356" does not.
func MatchMergeGateCommand(command string, patterns []string) (prNumber int, matched bool) {
	fields := strings.Fields(strings.TrimSpace(command))
	if len(fields) < 2 {
		return 0, false
	}
	for _, pat := range patterns {
		pf := strings.Fields(strings.TrimSpace(pat))
		if len(pf) == 0 || len(fields) < len(pf)+1 {
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
		n, err := strconv.Atoi(fields[len(pf)])
		if err != nil || n <= 0 {
			continue
		}
		return n, true
	}
	return 0, false
}
