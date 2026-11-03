package knight

import (
	"fmt"
	"regexp"
	"strings"
)

type skillSafetyFinding struct {
	Rule string
	Line int
}

func (f skillSafetyFinding) String() string {
	return fmt.Sprintf("%s (line %d)", f.Rule, f.Line)
}

func summarizeSkillSafetyFindings(findings []skillSafetyFinding) string {
	parts := make([]string, len(findings))
	for i, f := range findings {
		parts[i] = f.String()
	}
	return strings.Join(parts, ", ")
}

var dangerousSkillPatterns = []struct {
	rule string
	re   *regexp.Regexp

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
