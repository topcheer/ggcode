package agent

import (
	"fmt"
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

// redactedMarkerRe detects [REDACTED:type] markers that were inserted by
// redactSecrets into tool outputs. If an agent tries to write these markers
// back to files (edit_file, write_file, etc.), the real secret value would
// be destroyed. This check prevents that by warning before the write happens.
var redactedMarkerRe = regexp.MustCompile(`\[REDACTED:[a-z_]+\]`)

// isFileWriteTool returns true if the tool writes file content where injecting
// a REDACTED marker would corrupt the file.
func isFileWriteTool(toolName string) bool {
	switch toolName {
	case "edit_file", "write_file", "multi_edit_file", "multi_file_edit", "notebook_edit":
		return true
	default:
		return false
	}
}

// checkRedactedInWrite scans tool call arguments for REDACTED markers when the
// tool writes files. Returns a warning message if markers are found, empty otherwise.
// This prevents the agent from writing "[REDACTED:stripe_key]" literally into
// config files, which would destroy the real API key value.
func checkRedactedInWrite(toolName, args string) string {
	if !isFileWriteTool(toolName) {
		return ""
	}
	matches := redactedMarkerRe.FindAllString(args, -1)
	if len(matches) == 0 {
		return ""
	}
	unique := make(map[string]bool)
	for _, m := range matches {
		unique[m] = true
	}
	warning := fmt.Sprintf("[SECURITY: %d REDACTED marker(s) found in %s arguments. "+
		"STOP: You are about to write REDACTED placeholder text into a file. "+
		"The real secret value was masked when you read it earlier and is NOT available in your context. "+
		"Writing these markers will DESTROY the existing API key/secret in the file. "+
		"Do NOT proceed with this write. Either: "+
		"1. Skip this edit entirely (the existing value is correct) "+
		"2. Ask the user to provide the actual value "+
		"3. Use a placeholder variable like ${API_KEY} instead of the redacted marker]",
		len(unique), toolName)
	debug.Log("secret-redact", "blocked REDACTED marker in %s args, markers=%d", toolName, len(matches))
	return warning
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

	redacted := content
	// #1195: the scan WINDOW is capped at 256KB for CPU protection, but the
	// RETURNED content is not: silently dropping the tail made completeness
	// depend on whether the first 256KB happened to contain a secret, and
	// broke later edit_file anchors targeting the tail. Redact the scanned
	// prefix, keep the unscanned tail verbatim, and declare it in the notice.
	unscanned := ""
	if len(redacted) > maxRedactScanLen {
		unscanned = redacted[maxRedactScanLen:]
		redacted = redacted[:maxRedactScanLen]
	}
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

	debug.Log("secret-redact", "masked %d secret(s) in tool=%s content_len=%d unscanned_tail=%d", count, toolName, len(content), len(unscanned))
	notice := fmt.Sprintf(redactionNotice, count)
	if unscanned != "" {
		notice += "[NOTE: only the first 256KB of this output was scanned for secrets; the remainder is included verbatim and unscanned.]\n\n"
	}
	return notice + redacted + unscanned
}
