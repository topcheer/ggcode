package agent

import (
	"regexp"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

// Secret detection and redaction for tool outputs.
//
// Research basis: Secret leakage in AI agent context is a top security concern
// (OWASP LLM Top 10 2025: Sensitive Information Disclosure is #2). When agents
// read files, run commands, or grep source code, the results routinely contain
// API keys, tokens, private keys, and passwords. Without redaction these secrets
// are (1) persisted to session history on disk, (2) sent to external LLM API
// providers, and (3) visible in debug logs — all potential exfiltration vectors.
//
// Competitors: GitHub Copilot redacts secrets in telemetry; Cline/OpenHands
// have user-configurable secret masking; Aider warns before sending .env files.
// Claude Code wraps tool results with security notices. This guard goes further
// by actively masking secret VALUES while preserving surrounding context so the
// agent retains enough information to work productively.
//
// This is a heuristic, pattern-based defense — not a complete DLP solution.
// It targets high-precision patterns to minimize false positives that would
// break legitimate workflows (e.g., masking a hex object ID).

// maxRedactScanLen caps the content size scanned for secrets. Very large outputs
// (>256KB) are unlikely to be fully secrets and scanning them wastes CPU.
const maxRedactScanLen = 256 * 1024

// secretPatterns defines high-precision regex patterns for common secret types.
// Each pattern is designed to match the VALUE, not the key, so we can replace
// just the sensitive part while leaving context (key names, prefixes) visible.
var secretPatterns = []struct {
	name    string
	pattern *regexp.Regexp
}{
	// Cloud provider access keys — very high precision (fixed prefixes + charset)
	{"aws_access_key", regexp.MustCompile(`\b(AKIA[0-9A-Z]{16})\b`)},
	{"aws_secret_key", regexp.MustCompile(`(?i)aws_secret_access_key["'\s:=]+([A-Za-z0-9/+=]{40})`)},
	{"gcp_api_key", regexp.MustCompile(`\b(AIza[0-9A-Za-z_\-]{35})\b`)},
	{"azure_key", regexp.MustCompile(`(?i)azure[_-]?(?:account|storage)[_-]?key["'\s:=]+([A-Za-z0-9+/=]{86,88})`)},

	// Source-control / CI tokens — fixed prefixes are very reliable
	{"github_token", regexp.MustCompile(`\b(gh[pousr]_[A-Za-z0-9]{36,255})\b`)},
	{"github_legacy", regexp.MustCompile(`\b(gh[po]_[A-Za-z0-9]{36})\b`)},
	{"gitlab_token", regexp.MustCompile(`\b(glpat-[A-Za-z0-9_\-]{20})\b`)},
	{"slack_token", regexp.MustCompile(`\b(xox[bpras]-[A-Za-z0-9-]{10,72})\b`)},
	{"stripe_key", regexp.MustCompile(`\b((?:sk|pk|rk)_(?:test_|live_)?[A-Za-z0-9]{24,})\b`)},

	// Private key blocks — very high precision (PEM header + content + footer)
	{"private_key", regexp.MustCompile(`(?s)(-----BEGIN (?:[A-Z ]+)PRIVATE KEY-----.*?-----END (?:[A-Z ]+)PRIVATE KEY-----)`)},

	// Bearer / Authorization header tokens
	{"bearer_token", regexp.MustCompile(`(?i)\b(bearer\s+)([A-Za-z0-9_\-\.=]{20,})`)},

	// Assignment-style secrets: key=value or key: value or key := value
	// Matches common secret key names followed by a high-entropy value.
	// Requires the value to be at least 20 chars of base64/hex to avoid
	// flagging short config values like port=8080.
	{"assignment_secret", regexp.MustCompile(
		`(?i)((?:api[_-]?key|secret|token|passwd|password|auth[_-]?token|access[_-]?token|private[_-]?key|client[_-]?secret)["'\s]*[:=]\s*["']?)([A-Za-z0-9+/=_\-]{20,})["']?`)},

	// JWT tokens — three base64 segments separated by dots
	{"jwt", regexp.MustCompile(`\b(eyJ[A-Za-z0-9_\-]{10,}\.eyJ[A-Za-z0-9_\-]{10,}\.[A-Za-z0-9_\-]{10,})\b`)},
}

// redactionNotice is prepended to tool results when secrets are detected.
const redactionNotice = "[SECURITY: %d potential secret(s) detected and masked in this tool output to prevent leakage to external APIs. " +
	"The masked values were replaced with [REDACTED:type] markers. " +
	"If you need the actual value for a legitimate task, ask the user to provide it directly.]\n\n"

// redactSecrets scans tool results for common secret patterns and masks their
// values with [REDACTED:type] markers. This prevents secrets from being sent
// to external LLM providers or persisted verbatim in session history.
//
// The function preserves key names, labels, and surrounding context so the
// agent retains enough information to understand the structure of the data.
// Returns the content with secrets masked. If no secrets are found, returns
// the original content unchanged.
func redactSecrets(toolName, content string) string {
	// Only scan outputs from tools that return external/file content.
	if !externalContentTools[toolName] && !strings.HasPrefix(toolName, "mcp__") {
		return content
	}
	if len(content) < 10 {
		return content
	}

	scanContent := content
	if len(scanContent) > maxRedactScanLen {
		scanContent = scanContent[:maxRedactScanLen]
	}

	redacted := content
	count := 0

	for _, sp := range secretPatterns {
		// For patterns with capture groups, mask only the value group.
		// For single-group patterns (e.g. aws_access_key), mask the whole match.
		groups := sp.pattern.NumSubexp()
		if groups >= 2 {
			// Multi-group: mask only the last capture group (the secret value)
			redacted = sp.pattern.ReplaceAllStringFunc(redacted, func(match string) string {
				sub := sp.pattern.FindStringSubmatch(match)
				if len(sub) < 2 {
					return match
				}
				count++
				masked := "[REDACTED:" + sp.name + "]"
				// Replace the value portion while keeping the prefix group intact
				result := sub[1] + masked
				// Append any trailing characters after the value in the original match
				// by reconstructing from the submatches
				for i := 3; i < len(sub); i++ {
					if sub[i] != "" {
						result += sub[i]
					}
				}
				return result
			})
		} else {
			// Single-group or no-group: mask the entire match
			redacted = sp.pattern.ReplaceAllStringFunc(redacted, func(match string) string {
				count++
				return "[REDACTED:" + sp.name + "]"
			})
		}
	}

	if count == 0 {
		return content
	}

	debug.Log("secret-redact", "masked %d secret(s) in tool=%s content_len=%d", count, toolName, len(content))
	return redacted
}
