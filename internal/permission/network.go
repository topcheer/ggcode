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
	{NetworkExfiltrate, regexp.MustCompile(`(?i)\bcurl\b.*(-d|-data|--data|--data-binary|--data-raw|--post-file)?[=\s]*-?\s*<\s*\S`), "curl with input redirection sends a local file as request body"},
	{NetworkExfiltrate, regexp.MustCompile(`(?i)\bwget\b.*--post-file`), "wget posting a local file to a URL"},

	// scp/rsync to remote hosts (destination contains user@host: pattern)
	{NetworkExfiltrate, regexp.MustCompile(`(?i)\bscp\b`), "scp copies files to/from a remote host"},
	{NetworkExfiltrate, regexp.MustCompile(`(?i)\brsync\b.*\S+@\S+:`), "rsync syncing local files to a remote host"},
	{NetworkExfiltrate, regexp.MustCompile(`(?i)\brsync\b.*:`), "rsync syncing to a remote destination"},

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
