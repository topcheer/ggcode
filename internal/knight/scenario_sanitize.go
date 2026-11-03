package knight

import (
	"errors"
	"os"
	"regexp"
	"strings"
)

type sanitizationRule struct {
	name    string
	pattern *regexp.Regexp
	repl    string
}

var sanitizationRules = []sanitizationRule{
	{name: "email", pattern: regexp.MustCompile(`[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}`), repl: "[REDACTED_EMAIL]"},

	{name: "bearer", pattern: regexp.MustCompile(`(?i)bearer\s+[A-Za-z0-9._\-]{16,}`), repl: "Bearer [REDACTED_TOKEN]"},
	{name: "openai-key", pattern: regexp.MustCompile(`sk-[A-Za-z0-9]{16,}`), repl: "[REDACTED_TOKEN]"},
	{name: "github-pat", pattern: regexp.MustCompile(`gh[pousr]_[A-Za-z0-9]{20,}`), repl: "[REDACTED_TOKEN]"},
	{name: "aws-akid", pattern: regexp.MustCompile(`AKIA[0-9A-Z]{16}`), repl: "[REDACTED_TOKEN]"},
	{name: "jwt", pattern: regexp.MustCompile(`eyJ[A-Za-z0-9_\-]{10,}\.[A-Za-z0-9_\-]{10,}\.[A-Za-z0-9_\-]{10,}`), repl: "[REDACTED_TOKEN]"},

	{name: "ipv4", pattern: regexp.MustCompile(`\b(?:[0-9]{1,3}\.){3}[0-9]{1,3}\b`), repl: "[REDACTED_IP]"},
}

func sanitizeScenarioText(s string) string {
	if s == "" {
		return s
	}
	for _, rule := range sanitizationRules {
		s = rule.pattern.ReplaceAllString(s, rule.repl)
	}
	return s
}

func containsRedactedSensitive(s string) bool {
	return strings.Contains(s, "[REDACTED_")
}

func (k *Knight) ClearSkillScenarios() error {
	if k == nil {
		return nil
	}
	path := k.skillScenarioLogPath()
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func truncateSanitized(s string, max int) string {
	return truncateRunes(sanitizeScenarioText(s), max)
}
