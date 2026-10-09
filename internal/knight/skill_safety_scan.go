package knight

// r23: deterministic content-level safety scan for staged skills (the
// deterministic counterpart of the LLM "low-risk" gate clause).
//
// Why: a skill file is persisted then inlined into every future session
// prompt - the same injection surface r409 closed for save_memory (write
// and read paths). The knight skill path had NO deterministic content
// check: skill_promoter.go only validated structure, and the auto-promote
// gate's "no destructive actions / no credentials" was an LLM prompt
// constraint (fail-open). overlap.go already carries the deterministic
// check for redundancy; this adds the missing one for safety. The gap is
// named in docs/design/knight-design.md P2 (skills_guard, unimplemented).
//
// Design constraints:
//   - High precision over recall: a false positive only forces manual
//     review (the notify path), a false negative ships a poisoned skill.
//   - Findings never echo matched content (a hit may BE a credential) -
//     they report the rule name and line number only.
//   - Credential detection reuses the scenario_sanitize.go regex set
//     (single source of truth within the package); already-redacted
//     placeholders do not re-match those patterns.

import (
	"fmt"
	"regexp"
	"strings"
)

// skillSafetyFinding is one deterministic safety violation. Detail is a
// short human label, never the matched substring.
type skillSafetyFinding struct {
	Rule string
	Line int
}

func (f skillSafetyFinding) String() string {
	return fmt.Sprintf("%s (line %d)", f.Rule, f.Line)
}

// summarizeSkillSafetyFindings renders findings for logs/eval entries.
func summarizeSkillSafetyFindings(findings []skillSafetyFinding) string {
	parts := make([]string, len(findings))
	for i, f := range findings {
		parts[i] = f.String()
	}
	return strings.Join(parts, ", ")
}

// dangerousSkillPatterns are conservative, high-precision regexes for
// skill bodies. Each fires on an obvious attack or accident primitive;
// anything debatable is left to the LLM gate and human review.
var dangerousSkillPatterns = []struct {
	rule string
	re   *regexp.Regexp
	// exclude suppresses a match when the whole matching LINE also matches
	// this regex (e.g. --force-with-lease is the safe force form).
	exclude *regexp.Regexp
}{
	{rule: "rm-rf-root", re: regexp.MustCompile(`rm\s+(-[a-zA-Z]*r[a-zA-Z]*f|-[a-zA-Z]*f[a-zA-Z]*r)\s+(/|~)`), exclude: nil},
	{rule: "curl-pipe-shell", re: regexp.MustCompile(`(curl|wget)[^|\n]*\|\s*(ba|z|da)?sh\b`), exclude: nil},
	{rule: "base64-pipe-shell", re: regexp.MustCompile(`base64\s+(-d|--decode)[^|\n]*\|\s*(ba|z|da)?sh\b`), exclude: nil},
	{rule: "eval-base64", re: regexp.MustCompile(`eval\s+[^;\n]*base64`), exclude: nil},
	{rule: "chmod-777-root", re: regexp.MustCompile(`chmod\s+-R\s+777\s+/`), exclude: nil},
	{rule: "git-push-force", re: regexp.MustCompile(`git\s+push[^;\n]*--force\b`), exclude: regexp.MustCompile(`--force-with-lease`)},
	{rule: "sensitive-file-exfil", re: regexp.MustCompile(`(curl|wget|nc|ncat|ssh)[^;\n]*@(~/\.ssh|/etc/shadow|/etc/passwd|\.env\b|~/.aws|~/.gnupg)`), exclude: nil},
}

// scanSkillContentSafety returns the deterministic safety findings in a
// skill body, or nil when the content is clean. It checks, in order:
// bare credential-looking strings (scenario_sanitize regex set) and the
// dangerousSkillPatterns above. Both are line-anchored so a hit can be
// reported with a line number.
func scanSkillContentSafety(content string) []skillSafetyFinding {
	if strings.TrimSpace(content) == "" {
		return nil
	}
	lines := strings.Split(content, "\n")
	var findings []skillSafetyFinding
	seen := map[string]bool{}
	add := func(rule string, line int) {
		key := fmt.Sprintf("%s:%d", rule, line)
		if !seen[key] {
			seen[key] = true
			findings = append(findings, skillSafetyFinding{Rule: rule, Line: line})
		}
	}
	for i, line := range lines {
		lineNo := i + 1
		// Credential-shaped strings: reuse the sanitization regex set.
		// Placeholders like [REDACTED_TOKEN] do not match these patterns,
		// so an already-sanitized body passes cleanly.
		for _, rule := range sanitizationRules {
			if rule.pattern.MatchString(line) {
				add("credential-like:"+rule.name, lineNo)
			}
		}
		for _, p := range dangerousSkillPatterns {
			if p.re.MatchString(line) {
				if p.exclude != nil && p.exclude.MatchString(line) {
					continue
				}
				add(p.rule, lineNo)
			}
		}
	}
	return findings
}
