package security

import (
	"regexp"
	"strings"
)

var displaySecretPatterns = []struct {
	name    string
	pattern *regexp.Regexp

	gates []string
}{
	{"aws_access_key", regexp.MustCompile(`\b(AKIA[0-9A-Z]{16})\b`), []string{"AKIA"}},
	{"gcp_api_key", regexp.MustCompile(`\b(AIza[0-9A-Za-z_\-]{35})\b`), []string{"AIza"}},
	{"azure_key", regexp.MustCompile(`(?i)azure[_-]?(?:account|storage)[_-]?key["'\s:=]+([A-Za-z0-9+/=]{86,88})`), nil},
	{"github_token", regexp.MustCompile(`\b(gh[pousr]_[A-Za-z0-9]{36,255})\b`), []string{"ghp_", "gho_", "ghu_", "ghs_", "ghr_"}},

	{"github_fine_grained_token", regexp.MustCompile(`\b(github_pat_[0-9A-Za-z_]{82})\b`), []string{"github_pat_"}},
	{"gitlab_token", regexp.MustCompile(`\b(glpat-[A-Za-z0-9_\-]{20})\b`), []string{"glpat-"}},
	{"slack_token", regexp.MustCompile(`\b(xox[bpras]-[A-Za-z0-9-]{10,72})\b`), []string{"xox"}},
	{"stripe_key", regexp.MustCompile(`\b((?:sk|pk|rk)_(?:test_|live_)?[A-Za-z0-9]{24,})\b`), []string{"sk_", "pk_", "rk_"}},

	{"private_key", regexp.MustCompile(`(?s)(-----BEGIN (?:[A-Z ]*)PRIVATE KEY-----.*?-----END (?:[A-Z ]*)PRIVATE KEY-----)`), []string{"-----BEGIN"}},

	{"private_key_truncated", regexp.MustCompile(`(?s)(-----BEGIN (?:[A-Z ]*)PRIVATE KEY-----[\r\n]+[A-Za-z0-9+/=]{20,}.*)`), []string{"-----BEGIN"}},
	{"openai_key", regexp.MustCompile(`\b(sk-(?:proj-|svcacct-)?[A-Za-z0-9\-_]{20,})\b`), []string{"sk-"}},
	{"anthropic_key", regexp.MustCompile(`\b(sk-ant-[A-Za-z0-9\-_]{70,})\b`), []string{"sk-ant-"}},
	{"jwt", regexp.MustCompile(`\b(eyJ[A-Za-z0-9_\-]{10,}\.eyJ[A-Za-z0-9_\-]{10,}\.[A-Za-z0-9_\-]{10,})\b`), []string{"eyJ"}},

	{"assignment_secret", regexp.MustCompile(
		`(?i)((?:api[_-]?key|secret|token|passwd|password|auth[_-]?token|access[_-]?token|private[_-]?key|client[_-]?secret)["'\s]*[:=]\s*["']?)([A-Za-z0-9+/=_\-]{20,})(["']?)`), nil},

	{"aws_secret_access_key", regexp.MustCompile(`(?i)(aws_secret_access_key["'\s:=]+)([A-Za-z0-9/+=]{40})`), nil},
	{"azure_account_key_conn", regexp.MustCompile(`(AccountKey=)([A-Za-z0-9+/=]{50,})`), []string{"AccountKey="}},
	{"npm_token", regexp.MustCompile(`\b(npm_[0-9A-Za-z]{36})\b`), []string{"npm_"}},
	{"pypi_token", regexp.MustCompile(`\b(pypi-AgEIcHlwaW5p[A-Za-z0-9\-_]{50,})\b`), []string{"pypi-"}},
	{"docker_pat", regexp.MustCompile(`\b(dckr_pat_[0-9A-Za-z\-_]{27})\b`), []string{"dckr_pat_"}},
	{"twilio_key", regexp.MustCompile(`\b(SK[0-9a-fA-F]{32})\b`), []string{"SK"}},
	{"db_conn_password", regexp.MustCompile(`(?i)((?:postgres|postgresql|mongodb|mysql|redis|amqp)://[^:/\s@"']+:[^@\s"']{6,}@)`), nil},
}

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

func maskValue(value string) string {
	if len(value) <= 12 {
		return strings.Repeat("*", len(value))
	}
	return value[:4] + strings.Repeat("*", len(value)-8) + value[len(value)-4:]
}

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

			redacted = sp.pattern.ReplaceAllStringFunc(redacted, func(match string) string {
				sub := sp.pattern.FindStringSubmatch(match)
				if len(sub) < 3 {
					return match
				}

				suffix := ""
				if len(sub) > 3 {
					suffix = sub[len(sub)-1]
				}
				return sub[1] + maskValue(sub[2]) + suffix
			})
		} else {

			redacted = sp.pattern.ReplaceAllStringFunc(redacted, func(match string) string {
				return maskValue(match)
			})
		}
	}

	return redacted
}

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
