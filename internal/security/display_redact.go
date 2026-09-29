package security

import (
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
	// gates holds case-sensitive literals that every match of pattern must
	// contain. When none occur in the input the pattern is skipped entirely,
	// avoiding a full regex scan per pattern on secret-free text. Patterns
	// compiled with (?i) or without a stable literal stay nil (always scan).
	gates []string
}{
	{"aws_access_key", regexp.MustCompile(`\b(AKIA[0-9A-Z]{16})\b`), []string{"AKIA"}},
	{"gcp_api_key", regexp.MustCompile(`\b(AIza[0-9A-Za-z_\-]{35})\b`), []string{"AIza"}},
	{"azure_key", regexp.MustCompile(`(?i)azure[_-]?(?:account|storage)[_-]?key["'\s:=]+([A-Za-z0-9+/=]{86,88})`), nil},
	{"github_token", regexp.MustCompile(`\b(gh[pousr]_[A-Za-z0-9]{36,255})\b`), []string{"ghp_", "gho_", "ghu_", "ghs_", "ghr_"}},
	// #1289: fine-grained PAT, same shape as secretdetect.go's
	// github_fine_grained_token (#793) - the detection layer got the
	// pattern but this display list drifted. Keep the two in sync when
	// adding formats; bare github_pat_ text is not matched by gh[pousr]_.
	{"github_fine_grained_token", regexp.MustCompile(`\b(github_pat_[0-9A-Za-z_]{82})\b`), []string{"github_pat_"}},
	{"gitlab_token", regexp.MustCompile(`\b(glpat-[A-Za-z0-9_\-]{20})\b`), []string{"glpat-"}},
	{"slack_token", regexp.MustCompile(`\b(xox[bpras]-[A-Za-z0-9-]{10,72})\b`), []string{"xox"}},
	{"stripe_key", regexp.MustCompile(`\b((?:sk|pk|rk)_(?:test_|live_)?[A-Za-z0-9]{24,})\b`), []string{"sk_", "pk_", "rk_"}},
	// #1626-B: the prefix class required >=1 char (RSA/EC/OPENSSH/...),
	// so bare PKCS#8 `-----BEGIN PRIVATE KEY-----` - the format most
	// modern SDK keys ship in - passed through UNMASKED while the DETECTION
	// layer (secretdetect) matched it via its optional group: detection and
	// display drifted apart for the 4th time. `*` allows the empty prefix.
	{"private_key", regexp.MustCompile(`(?s)(-----BEGIN (?:[A-Z ]*)PRIVATE KEY-----.*?-----END (?:[A-Z ]*)PRIVATE KEY-----)`), []string{"-----BEGIN"}},
	{"openai_key", regexp.MustCompile(`\b(sk-(?:proj-|svcacct-)?[A-Za-z0-9\-_]{20,})\b`), []string{"sk-"}},
	{"anthropic_key", regexp.MustCompile(`\b(sk-ant-[A-Za-z0-9\-_]{70,})\b`), []string{"sk-ant-"}},
	{"jwt", regexp.MustCompile(`\b(eyJ[A-Za-z0-9_\-]{10,}\.eyJ[A-Za-z0-9_\-]{10,}\.[A-Za-z0-9_\-]{10,})\b`), []string{"eyJ"}},
	// Assignment-style: key=value or key: value
	{"assignment_secret", regexp.MustCompile(
		`(?i)((?:api[_-]?key|secret|token|passwd|password|auth[_-]?token|access[_-]?token|private[_-]?key|client[_-]?secret)["'\s]*[:=]\s*["']?)([A-Za-z0-9+/=_\-]{20,})(["']?)`), nil},
	// #1306: seven formats that existed ONLY in the detection layer
	// (secretdetect.go) - third drift recurrence after #1289. Mirrors the
	// detection patterns so TUI/IM/desktop redaction covers them.
	{"aws_secret_access_key", regexp.MustCompile(`(?i)(aws_secret_access_key["'\s:=]+)([A-Za-z0-9/+=]{40})`), nil},
	{"azure_account_key_conn", regexp.MustCompile(`(AccountKey=)([A-Za-z0-9+/=]{50,})`), []string{"AccountKey="}},
	{"npm_token", regexp.MustCompile(`\b(npm_[0-9A-Za-z]{36})\b`), []string{"npm_"}},
	{"pypi_token", regexp.MustCompile(`\b(pypi-AgEIcHlwaW5p[A-Za-z0-9\-_]{50,})\b`), []string{"pypi-"}},
	{"docker_pat", regexp.MustCompile(`\b(dckr_pat_[0-9A-Za-z\-_]{27})\b`), []string{"dckr_pat_"}},
	{"twilio_key", regexp.MustCompile(`\b(SK[0-9a-fA-F]{32})\b`), []string{"SK"}},
	{"db_conn_password", regexp.MustCompile(`(?i)((?:postgres|postgresql|mongodb|mysql|redis|amqp)://[^:/\s@"']+:[^@\s"']{6,}@)`), nil},
}

// containsAnyGates reports whether s may match a gated pattern: either the
// pattern has no gates (always scan) or at least one gate literal occurs in s.
func containsAnyGates(s string, gates []string) bool {
	if len(gates) == 0 {
		return true
	}
	for _, g := range gates {
		if strings.Contains(s, g) {
			return true
		}
	}
	return false
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

	for _, sp := range displaySecretPatterns {
		if !containsAnyGates(redacted, sp.gates) {
			continue
		}
		groups := sp.pattern.NumSubexp()
		if groups >= 2 {
			// Multi-group: mask only the value (last capture group)
			redacted = sp.pattern.ReplaceAllStringFunc(redacted, func(match string) string {
				sub := sp.pattern.FindStringSubmatch(match)
				if len(sub) < 3 {
					return match
				}
				// the screen after the mask, across all three UI paths (TUI
				// echo, IM push, desktop render). Suffixes only exist for 3+
				// group patterns.
				suffix := ""
				if len(sub) > 3 {
					suffix = sub[len(sub)-1]
				}
				return sub[1] + maskValue(sub[2]) + suffix
			})
		} else {
			// Single-group: mask the entire match
			redacted = sp.pattern.ReplaceAllStringFunc(redacted, func(match string) string {
				return maskValue(match)
			})
		}
	}

	return redacted
}

// HasSecretPattern returns true if the content contains any known secret pattern.
// Useful for deciding whether to apply redaction without scanning twice.
func HasSecretPattern(content string) bool {
	if len(content) < 10 {
		return false
	}
	for _, sp := range displaySecretPatterns {
		if !containsAnyGates(content, sp.gates) {
			continue
		}
		if sp.pattern.MatchString(content) {
			return true
		}
	}
	return false
}
