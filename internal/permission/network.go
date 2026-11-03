package permission

import (
	"regexp"
	"strings"
)

type NetworkRisk int

const (
	NetworkNone NetworkRisk = iota

	NetworkAccess

	NetworkExfiltrate
)

func (r NetworkRisk) String() string {
	switch r {
	case NetworkNone:
		return "none"
	case NetworkAccess:
		return "network"
	case NetworkExfiltrate:
		return "exfiltrate"
	default:
		return "unknown"
	}
}

type NetworkCheck struct {
	Risk   NetworkRisk
	Reason string
}

type networkPattern struct {
	risk   NetworkRisk
	re     *regexp.Regexp
	reason string
}

var networkPatterns = []networkPattern{

	{NetworkExfiltrate, regexp.MustCompile(`(?i)\bcurl\b.*(--data[- ]binary|-d|--data)[=\s]*@`), "curl sending local file contents via POST data"},

	{NetworkExfiltrate, regexp.MustCompile(`(?i)\bcurl\b.*--upload-file[=\s]`), "curl uploading a local file to a remote server"},
	{NetworkExfiltrate, regexp.MustCompile(`(?i)\bcurl\b.*\s-T(=[^\s]+|[=\s]|[^=\s])`), "curl -T uploading a local file to a remote server"},
	{NetworkExfiltrate, regexp.MustCompile(`(?i)\bcurl\b.*\s(-F[^\s=]*=|--form[=\s])`), "curl multipart form carrying local file data (@path)"},
	{NetworkExfiltrate, regexp.MustCompile(`(?i)\bcurl\b.*--post-file[=\s]`), "curl posting a local file to a URL"},
	{NetworkExfiltrate, regexp.MustCompile(`(?i)\bcurl\b.*-F\s+.*=@`), "curl uploading a local file as form data"},

	{NetworkExfiltrate, regexp.MustCompile(`(?i)\bcurl\b.*(--upload-file|-T)[=\s]*-\s*<`), "curl uploading stdin (redirected from a local file) to a remote server"},
	{NetworkExfiltrate, regexp.MustCompile(`(?i)\bcurl\b.*(-d|--data|--data-binary|--data-raw|--post-file)[=\s]*-?\s*<\s*\S`), "curl with a data flag and input redirection sends a local file as request body"},
	{NetworkExfiltrate, regexp.MustCompile(`(?i)\bwget\b.*--post-file`), "wget posting a local file to a URL"},

	{NetworkExfiltrate, regexp.MustCompile(`(?i)\b(nc|ncat|netcat)\b.*<\s*\S`), "netcat piping a local file to a remote host"},
	{NetworkExfiltrate, regexp.MustCompile(`(?i)\bncat\b.*<\s`), "ncat piping a local file to a remote host"},

	{NetworkExfiltrate, regexp.MustCompile(`(?i)\bsocat\b.*(TCP|UDP|OPENSSL|EXEC|SYSTEM|fork)[^|]*:`), "socat can relay data to external endpoints"},

	{NetworkExfiltrate, regexp.MustCompile(`(?i)\b(base64|xxd|od)\b.*\|\s*(curl|wget|nc|ncat)\b`), "encoding local data and piping to a network command (potential exfiltration)"},

	{NetworkExfiltrate, regexp.MustCompile(`(?i)\b(cat|type)\b.*\|\s*(curl|wget|nc|ncat)\b`), "piping file contents to a network command (potential exfiltration)"},

	{NetworkExfiltrate, regexp.MustCompile(`(?i)\bpython3?\s+-c\b.*(urllib|requests|http\.client|socket)`), "Python one-liner with network library (potential exfiltration)"},
	{NetworkExfiltrate, regexp.MustCompile(`(?i)\bnode\s+-e\b.*(http|https|net\.|fetch|axios)`), "Node.js one-liner with network library (potential exfiltration)"},
	{NetworkExfiltrate, regexp.MustCompile(`(?i)\bruby\s+-e\b.*(net/http|socket|uri)`), "Ruby one-liner with network library (potential exfiltration)"},
	{NetworkExfiltrate, regexp.MustCompile(`(?i)\bperl\s+-e\b.*(IO::Socket|LWP|HTTP::Tiny)`), "Perl one-liner with network library (potential exfiltration)"},

	{NetworkAccess, regexp.MustCompile(`(?i)\bcurl\b`), "curl makes an outbound network request"},
	{NetworkAccess, regexp.MustCompile(`(?i)\bwget\b`), "wget makes an outbound network request"},
	{NetworkAccess, regexp.MustCompile(`(?i)\b(nc|ncat|netcat)\b.*\s+\S+\s+\d+`), "netcat connecting to a remote host:port"},
	{NetworkAccess, regexp.MustCompile(`(?i)\bncat\b.*\s+\S+\s+\d+`), "ncat connecting to a remote host:port"},

	{NetworkAccess, regexp.MustCompile(`(?i)\bssh\b`), "ssh connects to a remote host"},
	{NetworkAccess, regexp.MustCompile(`(?i)\bansible\b`), "ansible executes commands on remote hosts"},
	{NetworkAccess, regexp.MustCompile(`(?i)\btelnet\b`), "telnet connects to a remote host"},
	{NetworkAccess, regexp.MustCompile(`(?i)\bftp\b\s`), "ftp connects to a remote server"},
}

func CheckNetwork(command string) NetworkCheck {
	cmd := strings.TrimSpace(command)
	if cmd == "" {
		return NetworkCheck{Risk: NetworkNone}
	}

	base := checkNetworkPatterns(cmd)

	if dir, ok := scpRsyncDirection(cmd); ok && dir.Risk > base.Risk {
		return dir
	}
	return base
}

func checkNetworkPatterns(cmd string) NetworkCheck {
	for _, p := range networkPatterns {
		if p.re.MatchString(cmd) {
			return NetworkCheck{
				Risk:   p.risk,
				Reason: p.reason,
			}
		}
	}
	return NetworkCheck{Risk: NetworkNone}
}

func IsNetworkCommand(command string) bool {
	return CheckNetwork(command).Risk != NetworkNone
}

func IsNetworkExfiltrate(command string) bool {
	return CheckNetwork(command).Risk == NetworkExfiltrate
}

func (c NetworkCheck) Suggestion() string {
	if c.Risk == NetworkNone {
		return ""
	}
	return c.Reason
}

func scpRsyncDirection(cmd string) (NetworkCheck, bool) {
	tokens := tokenizeCommand(cmd)
	found := false
	var worst NetworkCheck
	for i := 0; i < len(tokens); i++ {
		base := tokens[i]
		if j := strings.LastIndexByte(base, '/'); j >= 0 {
			base = base[j+1:]
		}
		if base != "scp" && base != "rsync" && !strings.EqualFold(base, "scp") && !strings.EqualFold(base, "rsync") {
			continue
		}
		found = true
		if res := analyzeTransfer(strings.EqualFold(base, "scp"), tokens, i+1); res.Risk > worst.Risk {
			worst = res
		}
	}
	return worst, found
}

func analyzeTransfer(isScp bool, tokens []string, start int) NetworkCheck {
	var operands []string
	for i := start; i < len(tokens); i++ {
		t := tokens[i]
		if isShellSeparator(t) {
			break
		}
		if strings.HasPrefix(t, "-") && t != "-" && t != "--" {

			name := strings.TrimLeft(t, "-")
			if strings.ContainsRune(name, '=') {
				continue
			}
			if optionTakesValue(isScp, name) {
				i++
			}
			continue
		}
		operands = append(operands, t)
	}
	if len(operands) == 0 {
		return NetworkCheck{Risk: NetworkAccess, Reason: "scp/rsync command without operands (direction unknown)"}
	}

	if isRemoteOperand(operands[len(operands)-1]) {
		return NetworkCheck{Risk: NetworkExfiltrate, Reason: "scp/rsync copying local files to a remote host"}
	}
	for _, src := range operands[:len(operands)-1] {
		if isRemoteOperand(src) {
			return NetworkCheck{Risk: NetworkAccess, Reason: "scp/rsync downloading from a remote host (remote is the source, not the destination)"}
		}
	}
	return NetworkCheck{Risk: NetworkAccess, Reason: "scp/rsync local copy (no remote operand)"}
}

func optionTakesValue(isScp bool, name string) bool {
	if isScp {
		switch name {
		case "i", "l", "o", "P", "S", "F", "c", "J":
			return true
		}
		return false
	}
	if name == "e" {
		return true
	}
	switch name {
	case "rsh", "exclude", "include", "filter", "exclude-from", "include-from",
		"files-from", "suffix", "chmod", "chown", "usermap", "groupmap",
		"rsync-path", "password-file", "compare-dest", "copy-dest", "link-dest",
		"backup-dir", "block-size", "max-size", "min-size", "compress-level",
		"log-file", "out-format", "temp-dir", "partial-dir":

		return true
	}
	return false
}

func isRemoteOperand(s string) bool {
	if s == "" || s == "-" {
		return false
	}
	if strings.HasPrefix(s, "rsync://") || strings.HasPrefix(s, "ssh://") {
		return true
	}
	colon := strings.IndexByte(s, ':')
	if colon <= 0 {
		return false
	}
	if strings.ContainsAny(s[:colon], "/\\") {
		return false
	}
	if colon == 1 && isASCIILetter(s[0]) {
		return false
	}
	return true
}

func isASCIILetter(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isShellSeparator(t string) bool {
	switch t {
	case "|", ";", "&", "&&", "||", ">", ">>", "<", "2>", "2>>":
		return true
	}
	return false
}

func tokenizeCommand(cmd string) []string {
	t := &cmdTokenizer{}
	for i := 0; i < len(cmd); i++ {
		c := cmd[i]
		switch {
		case t.quote != 0:
			if c == t.quote {
				t.quote = 0
			} else {
				t.cur.WriteByte(c)
			}
		case c == '\'' || c == '"':
			t.quote = c
		case c == '\\' && i+1 < len(cmd):
			i++
			t.cur.WriteByte(cmd[i])
		case isShellSpace(c):
			t.flush()
		case isShellOperatorChar(c):
			i = t.pushOperator(cmd, i)
		default:
			t.cur.WriteByte(c)
		}
	}
	t.flush()
	return t.tokens
}

func isShellSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\r' || c == '\n'
}

func isShellOperatorChar(c byte) bool {
	return c == '|' || c == ';' || c == '&' || c == '<' || c == '>'
}

// cmdTokenizer carries tokenizer state between characters so the main loop
// stays a flat switch.
type cmdTokenizer struct {
	tokens []string
	cur    strings.Builder
	quote  byte
}

func (t *cmdTokenizer) flush() {
	if t.cur.Len() > 0 {
		t.tokens = append(t.tokens, t.cur.String())
		t.cur.Reset()
	}
}

// pushOperator emits a shell operator token starting at cmd[i], collapsing
// runs of the same operator character (&&, ||, >>). An fd prefix ("2>")
// belongs to the separator, not an operand, so a pending all-digit token is
// folded into it. Returns the index of the last consumed byte.
func (t *cmdTokenizer) pushOperator(cmd string, i int) int {
	if t.cur.Len() > 0 && isAllDigits(t.cur.String()) {
		t.cur.Reset()
	}
	t.flush()
	c := cmd[i]
	j := i
	for j < len(cmd) && cmd[j] == c {
		j++
	}
	t.tokens = append(t.tokens, cmd[i:j])
	return j - 1
}

func isAllDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return len(s) > 0
}
