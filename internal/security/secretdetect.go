package security

import (
	"regexp"
	"strings"
)

type SecretPattern struct {
	ID                string
	Name              string
	Severity          string
	Pattern           *regexp.Regexp
	CheckPlaceholders bool
}

type Finding struct {
	PatternID string
	Name      string
	Severity  string
	Line      int
	Match     string
}

var secretPatterns = []SecretPattern{

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

	{
		ID:       "github_pat",
		Name:     "GitHub Personal Access Token",
		Severity: "high",
		Pattern:  regexp.MustCompile(`gh[pousr]_[0-9A-Za-z]{36,255}`),
	},
	{
		ID:       "github_fine_grained_token",
		Name:     "GitHub Fine-Grained Token",
		Severity: "high",

		Pattern: regexp.MustCompile(`github_pat_[0-9A-Za-z_]{82}`),
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

		Pattern: regexp.MustCompile("pypi-AgEIcHlwaW5p[A-Za-z0-9\\-_]{50,}"),
	},
	{
		ID:       "docker_token",
		Name:     "DHub PAT",
		Severity: "medium",
		Pattern:  regexp.MustCompile(`dckr_pat_[0-9A-Za-z\-_]{27}`),
	},

	{
		ID:       "private_key_block",
		Name:     "Private Key (PEM)",
		Severity: "high",

		Pattern: regexp.MustCompile(`-----BEGIN (?:ENCRYPTED |RSA |EC |OPENSSH |DSA |PGP )?PRIVATE KEY-----`),
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

	{
		ID:       "db_conn_password",
		Name:     "Database Connection String with Password",
		Severity: "high",
		Pattern:  regexp.MustCompile(`(?i)(?:postgres|postgresql|mongodb|mysql|redis|amqp)://[^:/\s@"']+:[^@\s"']{6,}@`),
	},

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

var fileAllowlistPatterns = []*regexp.Regexp{
	regexp.MustCompile(`_test\.go$`),

	regexp.MustCompile(`\.test\.go$`),
	regexp.MustCompile(`\.test\.py$`),
	regexp.MustCompile(`\.test\.(js|ts|mjs|jsx|tsx)$`),
	regexp.MustCompile(`\.test\.(json|ya?ml|golden|txt|input|output)$`),
	regexp.MustCompile(`testdata[/\\]`),
	regexp.MustCompile(`_fixture`),
	regexp.MustCompile(`(?i)\.example$`),
	regexp.MustCompile(`(?i)\.sample$`),
	regexp.MustCompile(`(?i)\.template$`),
	regexp.MustCompile(`secretdetect_test\.go$`),
}

func ScanForSecrets(filePath, content string) []Finding {
	if isAllowlisted(filePath) {
		return nil
	}

	var findings []Finding
	lines := strings.Split(content, "\n")

	for _, sp := range secretPatterns {
		matches := sp.Pattern.FindAllStringIndex(content, -1)

		if sp.ID == "openai_api_key" {
			filtered := matches[:0]
			for _, loc := range matches {
				if strings.HasPrefix(content[loc[0]:], "sk-ant-") {
					continue
				}
				filtered = append(filtered, loc)
			}
			matches = filtered
		}
		for _, loc := range matches {
			matchText := content[loc[0]:loc[1]]

			submatch := sp.Pattern.FindStringSubmatch(matchText)
			secretValue := matchText
			if len(submatch) > 1 {
				secretValue = submatch[1]
			}

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

func lineForOffset(lines []string, offset int) int {
	pos := 0
	for i, line := range lines {
		if pos+len(line) >= offset {
			return i + 1
		}
		pos += len(line) + 1
	}
	return 1
}

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

func maskSecret(value string) string {
	if len(value) <= 12 {
		return strings.Repeat("*", len(value))
	}
	return value[:4] + strings.Repeat("*", len(value)-8) + value[len(value)-4:]
}

func isAllowlisted(filePath string) bool {
	for _, re := range fileAllowlistPatterns {
		if re.MatchString(filePath) {
			return true
		}
	}
	return false
}

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

func (f Finding) Sprintf() string {
	sev := strings.ToUpper(f.Severity)
	return "WARNING [" + sev + "] " + f.Name + " at line " + itoa(f.Line) + ": " + f.Match + "\n"
}

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
