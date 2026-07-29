package security

import (
	"regexp"
	"strings"
)

// SecretPattern defines a detectable secret type.
type SecretPattern struct {
	ID                string
	Name              string
	Severity          string // "high", "medium", "low"
	Pattern           *regexp.Regexp
	CheckPlaceholders bool // if true, skip values that look like placeholders
}

// Finding represents a single detected secret occurrence.
type Finding struct {
	PatternID string
	Name      string
	Severity  string
	Line      int    // 1-based line number
	Match     string // the matched text (may be partially masked in output)
}

// secretPatterns is the curated list of high-signal secret patterns.
// Patterns are ordered by specificity: provider-specific keys first, then
// generic high-entropy patterns. False-positive-prone patterns use
// look-arounds or minimum-length thresholds to reduce noise.
var secretPatterns = []SecretPattern{
	// ---- Cloud provider keys ----
	{
		ID:       "aws_access_key",
		Name:     "AWS Access Key ID",
		Severity: "high",
		Pattern:  regexp.MustCompile(`AKIA[0-9A-Z]{16}`),
	},
	{
		ID:       "aws_secret_key",
		Name:     "AWS Secret Access Key",
		Severity: "high",
		Pattern:  regexp.MustCompile(`(?i)aws_secret_access_key["'\s:=]+([A-Za-z0-9/+=]{40})`),
	},
	{
		ID:       "gcp_api_key",
		Name:     "Google API Key",
		Severity: "high",
		Pattern:  regexp.MustCompile(`AIza[0-9A-Za-z\-_]{35}`),
	},
	{
		ID:       "gcp_service_account",
		Name:     "Google Service Account Private Key",
		Severity: "high",
		Pattern:  regexp.MustCompile(`"type"\s*:\s*"service_account"`),
	},
	{
		ID:       "azure_key",
		Name:     "Azure Account Key",
		Severity: "high",
		Pattern:  regexp.MustCompile(`AccountKey=[A-Za-z0-9+/=]{50,}`),
	},

	// ---- Source control / CI tokens ----
	{
		ID:       "github_pat",
		Name:     "GitHub Personal Access Token",
		Severity: "high",
		Pattern:  regexp.MustCompile(`gh[pousr]_[0-9A-Za-z]{36,255}`),
	},
	{
		ID:       "github_classic_token",
		Name:     "GitHub Classic Token",
		Severity: "high",
		Pattern:  regexp.MustCompile(`github_pat_[0-9A-Za-z_]{82}`),
	},
	{
		ID:       "gitlab_token",
		Name:     "GitLab Token",
		Severity: "high",
		Pattern:  regexp.MustCompile(`glpat-[0-9A-Za-z\-_]{20}`),
	},
	{
		ID:       "slack_token",
		Name:     "Slack Token",
		Severity: "high",
		Pattern:  regexp.MustCompile(`xox[baprs]-[0-9A-Za-z\-]{10,48}`),
	},

	// ---- Package manager / registry tokens ----
	{
		ID:       "npm_token",
		Name:     "NPM Auth Token",
		Severity: "high",
		Pattern:  regexp.MustCompile(`npm_[0-9A-Za-z]{36}`),
	},
	{
		ID:       "pypi_token",
		Name:     "PyPI Upload Token",
		Severity: "high",
		Pattern:  regexp.MustCompile(`pypi-AgEIcHlHaVZpWlBbWlFbWlFbWlFb[A-Za-z0-9\-_]{60}`),
	},
	{
		ID:       "docker_token",
		Name:     "DHub PAT",
		Severity: "medium",
		Pattern:  regexp.MustCompile(`dckr_pat_[0-9A-Za-z\-_]{27}`),
	},

	// ---- Private keys ----
	{
		ID:       "private_key_block",
		Name:     "Private Key (PEM)",
		Severity: "high",
		Pattern:  regexp.MustCompile(`-----BEGIN (?:RSA |EC |OPENSSH |DSA |PGP )?PRIVATE KEY-----`),
	},
	{
		ID:       "ssh_private_key",
		Name:     "SSH Private Key (OpenSSH)",
		Severity: "high",
		Pattern:  regexp.MustCompile(`openssh-key-v1`),
	},
	{
		ID:       "jwt_token",
		Name:     "JWT Token",
		Severity: "medium",
		Pattern:  regexp.MustCompile(`eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}`),
	},

	// ---- Database connection strings ----
	{
		ID:       "db_conn_password",
		Name:     "Database Connection String with Password",
		Severity: "high",
		Pattern:  regexp.MustCompile(`(?i)(?:postgres|postgresql|mongodb|mysql|redis|amqp)://[^:/\s@"']+:[^@\s"']{6,}@`),
	},

	// ---- Generic API key assignments ----
	// Catches common patterns like: api_key = "sk-...", secret = "...", password = "..."
	// Requires a minimum length and is scoped to assignment context to reduce false positives.
	{
		ID:                "generic_api_key_assignment",
		Name:              "Hardcoded API Key / Secret Assignment",
		Severity:          "medium",
		Pattern:           regexp.MustCompile(`(?i)(?:api[_-]?key|secret|auth[_-]?token|access[_-]?token|client[_-]?secret|private[_-]?key|passwd|password)\s*[:=]\s*["']([A-Za-z0-9\-_+/=]{20,})["']`),
		CheckPlaceholders: true,
	},
	{
		ID:       "openai_api_key",
		Name:     "OpenAI API Key",
		Severity: "high",
		// Covers classic sk-... and modern sk-proj-.../sk-svcacct-... formats
		Pattern: regexp.MustCompile(`sk-(?:proj-|svcacct-)?[A-Za-z0-9\-_]{20,}`),
	},
	{
		ID:       "anthropic_api_key",
		Name:     "Anthropic API Key",
		Severity: "high",
		Pattern:  regexp.MustCompile(`sk-ant-[A-Za-z0-9\-_]{70,}`),
	},
	{
		ID:       "stripe_key",
		Name:     "Stripe Secret Key",
		Severity: "high",
		Pattern:  regexp.MustCompile(`sk_live_[0-9A-Za-z]{24,}`),
	},
	{
		ID:       "twilio_key",
		Name:     "Twilio Auth Token",
		Severity: "high",
		Pattern:  regexp.MustCompile(`SK[0-9a-fA-F]{32}`),
	},
}

// fileAllowlist lists file extensions and path patterns where secret-like
// strings are expected (e.g., test fixtures, .env.example). We don't scan
// these to avoid noise.
var fileAllowlistPatterns = []*regexp.Regexp{
	regexp.MustCompile(`_test\.go$`),
	regexp.MustCompile(`\.test\.`),
	regexp.MustCompile(`testdata[/\\]`), // both Unix and Windows path separators
	regexp.MustCompile(`_fixture`),
	regexp.MustCompile(`(?i)\.example$`),
	regexp.MustCompile(`(?i)\.sample$`),
	regexp.MustCompile(`(?i)\.template$`),
	regexp.MustCompile(`secretdetect_test\.go$`),
}

// ScanForSecrets scans content for known secret patterns and returns findings.
// filePath is used to skip allowlisted files (test fixtures, examples, etc.).
func ScanForSecrets(filePath, content string) []Finding {
	if isAllowlisted(filePath) {
		return nil
	}

	var findings []Finding
	lines := strings.Split(content, "\n")

	for _, sp := range secretPatterns {
		matches := sp.Pattern.FindAllStringIndex(content, -1)
		for _, loc := range matches {
			matchText := content[loc[0]:loc[1]]
			// Extract the actual secret portion if the pattern has capture groups
			submatch := sp.Pattern.FindStringSubmatch(matchText)
			secretValue := matchText
			if len(submatch) > 1 {
				secretValue = submatch[1]
			}

			// Skip obvious placeholders for generic patterns only.
			// Provider-specific patterns (AWS, GitHub, etc.) are specific
			// enough that placeholder filtering would cause false negatives.
			if sp.CheckPlaceholders && isPlaceholder(secretValue) {
				continue
			}

			lineNum := lineForOffset(lines, loc[0])
			findings = append(findings, Finding{
				PatternID: sp.ID,
				Name:      sp.Name,
				Severity:  sp.Severity,
				Line:      lineNum,
				Match:     maskSecret(secretValue),
			})
		}
	}

	return findings
}

// lineForOffset returns the 1-based line number for a byte offset.
func lineForOffset(lines []string, offset int) int {
	pos := 0
	for i, line := range lines {
		if pos+len(line) >= offset {
			return i + 1
		}
		pos += len(line) + 1 // +1 for newline
	}
	return 1
}

// isPlaceholder returns true for common placeholder/dummy values that are
// not real secrets.
func isPlaceholder(value string) bool {
	lower := strings.ToLower(value)
	placeholders := []string{
		"your-api-key", "your_api_key", "xxx", "placeholder",
		"example", "dummy", "fake", "test", "changeme", "change-me",
		"your-key-here", "insert-key", "replace-me", "redacted",
		"your-openai", "sk-your", "sk-test", "sk-example",
	}
	for _, p := range placeholders {
		if strings.Contains(lower, p) {
			return true
		}
	}
	// All same character (e.g., "aaaa...")
	if len(value) >= 4 {
		first := value[0]
		allSame := true
		for i := 1; i < len(value); i++ {
			if value[i] != first {
				allSame = false
				break
			}
		}
		if allSame {
			return true
		}
	}
	return false
}

// maskSecret masks the middle portion of a secret for safe display.
// Shows first 4 and last 4 characters, replacing the rest with asterisks.
func maskSecret(value string) string {
	if len(value) <= 12 {
		return strings.Repeat("*", len(value))
	}
	return value[:4] + strings.Repeat("*", len(value)-8) + value[len(value)-4:]
}

// isAllowlisted checks whether a file path matches any allowlist pattern.
func isAllowlisted(filePath string) bool {
	for _, re := range fileAllowlistPatterns {
		if re.MatchString(filePath) {
			return true
		}
	}
	return false
}

// FormatWarnings formats findings into a human-readable warning string
// suitable for appending to a tool result.
func FormatWarnings(findings []Finding) string {
	if len(findings) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("\n\n--- SECURITY WARNING: Potential secrets detected ---\n")
	for _, f := range findings {
		sb.WriteString(f.Sprintf())
	}
	sb.WriteString("These look like real credentials. Verify they are not live secrets.\n")
	sb.WriteString("If accidental, remove them immediately and rotate the compromised key.\n")
	sb.WriteString("--- END SECURITY WARNING ---\n")
	return sb.String()
}

// Sprintf returns a formatted single-line description of a finding.
func (f Finding) Sprintf() string {
	sev := strings.ToUpper(f.Severity)
	return "WARNING [" + sev + "] " + f.Name + " at line " + itoa(f.Line) + ": " + f.Match + "\n"
}

// itoa is a minimal int-to-string without importing strconv (keeps the
// formatting code self-contained for testability).
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
