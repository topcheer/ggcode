package agent

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

const maxRedactScanLen = 256 * 1024

var secretPatterns = []struct {
	name    string
	pattern *regexp.Regexp
}{

	{"aws_access_key", regexp.MustCompile(`\b(AKIA[0-9A-Z]{16})\b`)},
	{"aws_secret_key", regexp.MustCompile(`(?i)aws_secret_access_key["'\s:=]+([A-Za-z0-9/+=]{40})`)},
	{"gcp_api_key", regexp.MustCompile(`\b(AIza[0-9A-Za-z_\-]{35})\b`)},
	{"azure_key", regexp.MustCompile(`(?i)azure[_-]?(?:account|storage)[_-]?key["'\s:=]+([A-Za-z0-9+/=]{86,88})`)},

	{"github_token", regexp.MustCompile(`\b(gh[pousr]_[A-Za-z0-9]{36,255})\b`)},
	{"github_legacy", regexp.MustCompile(`\b(gh[po]_[A-Za-z0-9]{36})\b`)},
	{"gitlab_token", regexp.MustCompile(`\b(glpat-[A-Za-z0-9_\-]{20})\b`)},
	{"slack_token", regexp.MustCompile(`\b(xox[bpras]-[A-Za-z0-9-]{10,72})\b`)},
	{"stripe_key", regexp.MustCompile(`\b((?:sk|pk|rk)_(?:test_|live_)?[A-Za-z0-9]{24,})\b`)},

	{"openai_or_anthropic_key", regexp.MustCompile(`\b(sk-(?:proj-|ant-)?[A-Za-z0-9_-]{20,})\b`)},

	{"private_key", regexp.MustCompile(`(?s)(-----BEGIN (?:[A-Z ]+)PRIVATE KEY-----.*?-----END (?:[A-Z ]+)PRIVATE KEY-----)`)},

	{"bearer_token", regexp.MustCompile(`(?i)\b(bearer\s+)([A-Za-z0-9_\-\.=]{20,})`)},

	{"assignment_secret", regexp.MustCompile(
		`(?i)((?:api[_-]?key|secret|token|passwd|password|auth[_-]?token|access[_-]?token|private[_-]?key|client[_-]?secret)["'\s]*[:=]\s*["']?)([A-Za-z0-9+/=_\-]{20,})["']?`)},

	{"jwt", regexp.MustCompile(`\b(eyJ[A-Za-z0-9_\-]{10,}\.eyJ[A-Za-z0-9_\-]{10,}\.[A-Za-z0-9_\-]{10,})\b`)},
}

var redactedMarkerRe = regexp.MustCompile(`\[REDACTED:[a-z_]+\]`)

func isFileWriteTool(toolName string) bool {
	switch toolName {

	case "edit_file", "write_file", "multi_edit_file", "multi_file_edit",
		"multi_file_write", "batch_replace", "notebook_edit",

		"lsp_rename":
		return true
	default:
		return false
	}
}

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

const redactionNotice = "[SECURITY: %d potential secret(s) detected and masked in this tool output to prevent leakage to external APIs. " +
	"The masked values were replaced with [REDACTED:type] markers. " +
	"If you need the actual value for a legitimate task, ask the user to provide it directly.]\n\n"

func redactSecrets(toolName, content string) string {

	if !externalContentTools[toolName] && !strings.HasPrefix(toolName, "mcp__") {
		return content
	}
	if len(content) < 10 {
		return content
	}

	redacted := content

	unscanned := ""
	if len(redacted) > maxRedactScanLen {
		unscanned = redacted[maxRedactScanLen:]
		redacted = redacted[:maxRedactScanLen]
	}
	count := 0

	for _, sp := range secretPatterns {

		groups := sp.pattern.NumSubexp()
		if groups >= 2 {
			var sb strings.Builder
			last := 0
			locs := sp.pattern.FindAllStringSubmatchIndex(redacted, -1)
			for _, loc := range locs {
				if len(loc) < 6 || loc[4] < 0 || loc[5] < 0 {
					continue
				}
				count++
				sb.WriteString(redacted[last:loc[4]])
				sb.WriteString("[REDACTED:" + sp.name + "]")
				last = loc[5]
			}
			if count > 0 || locs != nil {
				sb.WriteString(redacted[last:])
				redacted = sb.String()
			}
		} else {

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
