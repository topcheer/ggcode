package security

import (
	"fmt"
	"regexp"
	"strings"
)

// Display-time secret redaction for UI layers (TUI, Desktop GUI, IM push).
//
// Unlike the agent-layer redaction (which was removed because it corrupted
// agent context by replacing real API keys with [REDACTED] markers before
// the agent could process them), this module ONLY runs at display time -
// after the agent has finished processing. This ensures:
//
//   1. Agent internal processing uses plaintext (can read/edit config files)
//   2. What the user SEES in TUI/GUI/IM has secrets masked for safety
//   3. What gets pushed to IM (Telegram, Discord, etc.) has secrets masked
//
// This is a heuristic, pattern-based defense - not a complete DLP solution.

// displaySecretPatterns mirrors the high-precision patterns used for agent-layer
// redaction, but operates purely on display text.
var displaySecretPatterns = []struct {
	name    string
	pattern *regexp.Regexp
}{
	{"aws_access_key", regexp.MustCompile(`\b(AKIA[0-9A-Z]{16})\b`)},
	{"gcp_api_key", regexp.MustCompile(`\b(AIza[0-9A-Za-z_\-]{35})\b`)},
	{"azure_key", regexp.MustCompile(`(?i)azure[_-]?(?:account|storage)[_-]?key["'\s:=]+([A-Za-z0-9+/=]{86,88})`)},
	{"github_token", regexp.MustCompile(`\b(gh[pousr]_[A-Za-z0-9]{36,255})\b`)},
	// #1289: fine-grained PAT, same shape as secretdetect.go's
	// github_fine_grained_token (#793) - the detection layer got the
	// pattern but this display list drifted. Keep the two in sync when
	// adding formats; bare github_pat_ text is not matched by gh[pousr]_.
	{"github_fine_grained_token", regexp.MustCompile(`\b(github_pat_[0-9A-Za-z_]{82})\b`)},
	{"gitlab_token", regexp.MustCompile(`\b(glpat-[A-Za-z0-9_\-]{20})\b`)},
	{"slack_token", regexp.MustCompile(`\b(xox[bpras]-[A-Za-z0-9-]{10,72})\b`)},
	{"stripe_key", regexp.MustCompile(`\b((?:sk|pk|rk)_(?:test_|live_)?[A-Za-z0-9]{24,})\b`)},
	{"private_key", regexp.MustCompile(`(?s)(-----BEGIN (?:[A-Z ]+)PRIVATE KEY-----.*?-----END (?:[A-Z ]+)PRIVATE KEY-----)`)},
	{"openai_key", regexp.MustCompile(`\b(sk-(?:proj-|svcacct-)?[A-Za-z0-9\-_]{20,})\b`)},
	{"anthropic_key", regexp.MustCompile(`\b(sk-ant-[A-Za-z0-9\-_]{70,})\b`)},
	{"jwt", regexp.MustCompile(`\b(eyJ[A-Za-z0-9_\-]{10,}\.eyJ[A-Za-z0-9_\-]{10,}\.[A-Za-z0-9_\-]{10,})\b`)},
	// Assignment-style: key=value or key: value
	{"assignment_secret", regexp.MustCompile(
		`(?i)((?:api[_-]?key|secret|token|passwd|password|auth[_-]?token|access[_-]?token|private[_-]?key|client[_-]?secret)["'\s]*[:=]\s*["']?)([A-Za-z0-9+/=_\-]{20,})(["']?)`)},
}

// maskValue masks the middle portion of a secret for display.
// Shows first 4 and last 4 chars, replaces the rest with asterisks.
func maskValue(value string) string {
	if len(value) <= 12 {
		return strings.Repeat("*", len(value))
	}
	return value[:4] + strings.Repeat("*", len(value)-8) + value[len(value)-4:]
}

// RedactForDisplay masks known secret patterns in text for safe display.
// This is intended for TUI rendering, Desktop GUI, and IM message formatting.
// It does NOT modify the underlying data — only the display representation.
func RedactForDisplay(content string) string {
	if len(content) < 10 {
		return content
	}
	redacted := content
	changed := false

	for _, sp := range displaySecretPatterns {
		groups := sp.pattern.NumSubexp()
		if groups >= 2 {
			// Multi-group: mask only the value (last capture group)
			redacted = sp.pattern.ReplaceAllStringFunc(redacted, func(match string) string {
				sub := sp.pattern.FindStringSubmatch(match)
				if len(sub) < 3 {
					return match
				}
				changed = true
				// Keep prefix + masked value + suffix
				return sub[1] + maskValue(sub[2]) + sub[len(sub)-1]
			})
		} else {
			// Single-group: mask the entire match
			redacted = sp.pattern.ReplaceAllStringFunc(redacted, func(match string) string {
				changed = true
				return maskValue(match)
			})
		}
	}

	_ = changed // no notice needed for display redaction
	return redacted
}

// HasSecretPattern returns true if the content contains any known secret pattern.
// Useful for deciding whether to apply redaction without scanning twice.
func HasSecretPattern(content string) bool {
	if len(content) < 10 {
		return false
	}
	for _, sp := range displaySecretPatterns {
		if sp.pattern.MatchString(content) {
			return true
		}
	}
	return false
}

// FormatRedactionNotice returns a notice string explaining that secrets were
// masked in the display. Returns empty if no redaction notice is needed.
func FormatRedactionNotice() string {
	return fmt.Sprintf("[显示脱敏: 检测到敏感信息已做掩码处理 / Display redacted: sensitive values masked for safety]\n")
}
