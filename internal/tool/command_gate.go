package tool

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

// ---------------------------------------------------------------------------
// GateBehavior — Claude Code inspired three-state model
//
//   Allow  → command is safe, execute immediately
//   Ask    → command needs user confirmation (destructive or suspicious)
//   Block  → command is never executed (catastrophic, no legitimate use)
//
// In Claude Code, nearly everything is "ask" rather than "block". The only
// hard blocks are patterns that have zero legitimate use for an AI coding
// assistant and would cause immediate irreversible harm.
// ---------------------------------------------------------------------------

type GateBehavior int

const (
	Allow GateBehavior = iota
	Ask
	Block
)

// GateResult is the outcome of a command gate check.
type GateResult struct {
	Behavior   GateBehavior
	CleanedCmd string   // sanitized command (may differ from original)
	Reason     string   // human-readable explanation
	Warnings   []string // informational warnings (shown but don't affect flow)
}

// Allowed returns true if the command can execute without confirmation.
func (r GateResult) Allowed() bool { return r.Behavior == Allow }

// NeedsConfirmation returns true if the command requires user approval.
func (r GateResult) NeedsConfirmation() bool { return r.Behavior == Ask }

// IsBlocked returns true if the command must never execute.
func (r GateResult) IsBlocked() bool { return r.Behavior == Block }

// ---------------------------------------------------------------------------
// CommandGate — the safety checker
// ---------------------------------------------------------------------------

// CommandGate performs safety checks on shell commands before execution.
// Inspired by Claude Code's bashSecurity.ts architecture:
//
// Layer 1: Pre-checks — control chars, injection patterns, parser differentials
// Layer 2: Catastrophic block — patterns with zero legitimate use
// Layer 3: Destructive warning — informational, doesn't block
//
// The gate runs regardless of autopilot mode. In supervised mode, "ask" results
// prompt the user. In autopilot mode, "ask" results still block execution unless
// an explicit override is configured.
type CommandGate struct {
	blockRules []*gateRule
	askRules   []*gateRule
	cleanRules []cleanRule
}

type gateRule struct {
	desc    string
	pattern *regexp.Regexp
	kind    string // "catastrophic" | "injection" | "destructive" | "security"
}

type cleanRule struct {
	desc    string
	pattern *regexp.Regexp
}

// NewCommandGate creates the command safety gate with all rules.
func NewCommandGate() *CommandGate {
	g := &CommandGate{}

	// ================================================================
	// BLOCK — catastrophic commands with zero legitimate use
	// ================================================================
	g.blockRules = []*gateRule{

		// --- Filesystem destruction ---
		{kind: "catastrophic", desc: "recursive force delete of root/critical directory",
			pattern: regexp.MustCompile(`(?i)\brm\s+(-[a-zA-Z]*r[a-zA-Z]*f[a-zA-Z]*|-[a-zA-Z]*f[a-zA-Z]*r[a-zA-Z]*|--recursive\s+--force|--force\s+--recursive)\s+(/|~/|/home/?|/Users/?|/etc/?|/var/?|/usr/?|System|Applications)`)},
		{kind: "catastrophic", desc: "recursive force delete (alternate flag order)",
			pattern: regexp.MustCompile(`(?i)\brm\s+.*(-[a-zA-Z]*r[a-zA-Z]*|\\-\\-recursive).*(-[a-zA-Z]*f[a-zA-Z]*|\\-\\-force).*\s+(/|~/|/home|/Users|/etc|/var|/usr)`)},
		{kind: "catastrophic", desc: "disk format/erase",
			pattern: regexp.MustCompile(`(?i)\b(mkfs\.|dd\s+if=.*of=/dev/|diskutil\s+eraseDisk|diskutil\s+partitionDisk)`)},
		{kind: "catastrophic", desc: "fork bomb",
			pattern: regexp.MustCompile(`(?i)(:\(\)\{\s*:\|:\&\s*\}|fork\s+bomb)`)},
		{kind: "catastrophic", desc: "filesystem wipe via chmod",
			pattern: regexp.MustCompile(`(?i)\bchmod\s+(-R\s+)?000\s+/`)},

		// --- System control ---
		{kind: "catastrophic", desc: "system shutdown/reboot",
			pattern: regexp.MustCompile(`(?i)\b(shutdown\b.*(-h|-r|now)|\breboot\b|\bhalt\b|\bpoweroff\b|init\s+[06])`)},
		{kind: "catastrophic", desc: "kernel module manipulation",
			pattern: regexp.MustCompile(`(?i)\b(rmmod|insmod|modprobe)\s+`)},

		// --- Credential exfiltration ---
		{kind: "catastrophic", desc: "credential exfiltration via network",
			pattern: regexp.MustCompile(`(?i)\b(curl|wget|nc|ncat)\s+.*(\b~?/\.(ssh|gnupg|aws|env)/|\b/etc/(passwd|shadow)|--post-file.*\.(ssh|aws|env|gnupg)|<\s*~?/\.\w+/)`)},

		// --- History manipulation to hide tracks ---
		{kind: "catastrophic", desc: "history manipulation to hide tracks",
			pattern: regexp.MustCompile(`(?i)(unset\s+HISTFILE|export\s+HISTFILE=/dev/null|history\s+(-c|--clear)|>\s*~?/\.(bash_history|zsh_history))`)},

		// --- Disable security tools ---
		{kind: "catastrophic", desc: "disable security tooling",
			pattern: regexp.MustCompile(`(?i)\b(killall|pkill)\s+(-[0-9]+\s+)?(Little.?Snitch|LuLu|SecuritySpy|fseventsd|sandboxd)`)},

		// --- Overwrite critical files (block, not ask — no legitimate use for AI) ---
		{kind: "catastrophic", desc: "overwrite critical system files",
			pattern: regexp.MustCompile(`(?i)>\s*/etc/(passwd|shadow|sudoers|fstab)\b`)},
		{kind: "catastrophic", desc: "overwrite SSH authorized_keys",
			pattern: regexp.MustCompile(`(?i)>\s*~/\.ssh/authorized_keys\b`)},
		{kind: "catastrophic", desc: "recursive chown on root",
			pattern: regexp.MustCompile(`(?i)\bchown\s+-R\s+\S+\s+/`)},
	}

	// ================================================================
	// ASK — destructive or suspicious, needs user confirmation
	// ================================================================
	g.askRules = []*gateRule{

		// --- Command injection patterns (Claude Code style) ---
		{kind: "injection", desc: "command substitution $() may hide nested commands",
			pattern: regexp.MustCompile(`\$\(`)},
		{kind: "injection", desc: "process substitution <() can bypass path checks",
			pattern: regexp.MustCompile(`<\(`)},
		{kind: "injection", desc: "process substitution >() can redirect output covertly",
			pattern: regexp.MustCompile(`>\(`)},
		{kind: "injection", desc: "backtick command substitution",
			pattern: regexp.MustCompile("`.+`")},
		{kind: "injection", desc: "parameter expansion ${} can execute code",
			pattern: regexp.MustCompile(`\$\{`)},

		// --- Dangerous file operations ---
		{kind: "destructive", desc: "recursive force delete",
			pattern: regexp.MustCompile(`(?i)\brm\s+(-[a-zA-Z]*f[a-zA-Z]*\s+)?-[a-zA-Z]*r[a-zA-Z]*\s+`)},
		{kind: "destructive", desc: "force delete without confirmation",
			pattern: regexp.MustCompile(`(?i)\brm\s+-[a-zA-Z]*f[a-zA-Z]*\s+`)},
		{kind: "destructive", desc: "overwrite /etc/hosts",
			pattern: regexp.MustCompile(`(?i)>\s*/etc/hosts\b`)},
		{kind: "destructive", desc: "overwrite SSH config/known_hosts",
			pattern: regexp.MustCompile(`(?i)>\s*~/\.ssh/(config|known_hosts)\b`)},

		// --- Privilege escalation ---
		{kind: "security", desc: "sudo command requires elevated privileges",
			pattern: regexp.MustCompile(`(?i)\bsudo\s+`)},

		// --- Network-based risks ---
		{kind: "security", desc: "piping remote content to shell",
			pattern: regexp.MustCompile(`(?i)(curl|wget)\s+.*\|\s*(ba)?sh`)},
		{kind: "security", desc: "download and execute script",
			pattern: regexp.MustCompile(`(?i)(curl|wget)\s+.*>\s*/tmp/.*\.\w+\s*&&\s*(ba)?sh\s+/tmp/`)},

		// --- Infrastructure (from Claude Code destructiveCommandWarning) ---
		{kind: "destructive", desc: "git reset --hard may discard uncommitted changes",
			pattern: regexp.MustCompile(`(?i)\bgit\s+reset\s+--hard\b`)},
		{kind: "destructive", desc: "git push --force may overwrite remote history",
			pattern: regexp.MustCompile(`(?i)\bgit\s+push\b[^;&|\n]*\s(--force|-f)\b`)},
		{kind: "destructive", desc: "git clean -f may permanently delete untracked files",
			pattern: regexp.MustCompile(`(?i)\bgit\s+clean\b[^;&|\n]*-[a-zA-Z]*f`)},
		{kind: "destructive", desc: "database DROP/TRUNCATE operation",
			pattern: regexp.MustCompile(`(?i)\b(DROP|TRUNCATE)\s+(TABLE|DATABASE|SCHEMA)\b`)},
		{kind: "destructive", desc: "DELETE without WHERE clause",
			pattern: regexp.MustCompile(`(?i)\bDELETE\s+FROM\s+\w+[\s;"']*($|[;\n])`)},
		{kind: "destructive", desc: "kubectl delete may remove Kubernetes resources",
			pattern: regexp.MustCompile(`(?i)\bkubectl\s+delete\b`)},
		{kind: "destructive", desc: "terraform destroy may remove infrastructure",
			pattern: regexp.MustCompile(`(?i)\bterraform\s+destroy\b`)},

		// --- Permission modifications ---
		{kind: "security", desc: "chmod on system directories",
			pattern: regexp.MustCompile(`(?i)\bchmod\s+(-R\s+)?[0-7]+\s+/(etc|var|usr|System)\b`)},

		// --- Destructive cron ---
		{kind: "destructive", desc: "destructive cron job",
			pattern: regexp.MustCompile(`(?i)crontab\s+.*\brm\s+-r`)},
	}

	// ================================================================
	// CLEAN — auto-fix known safe transformations
	// ================================================================
	g.cleanRules = []cleanRule{
		{desc: "remove dangerous GIT_PAGER override",
			pattern: regexp.MustCompile(`(?i)GIT_PAGER=\S+`)},
	}

	return g
}

// Check evaluates a command against all safety rules.
// Returns a GateResult with Behavior = Allow, Ask, or Block.
func (g *CommandGate) Check(cmd string) GateResult {
	result := GateResult{
		Behavior:   Allow,
		CleanedCmd: cmd,
	}

	// ---- Layer 0: Pre-checks (Claude Code's parseForSecurity pre-checks) ----
	if pre := g.preChecks(cmd); pre.Behavior != Allow {
		return pre
	}

	// ---- Layer 1: Block rules (catastrophic, never execute) ----
	for _, rule := range g.blockRules {
		if rule.pattern.MatchString(cmd) {
			return GateResult{
				Behavior:   Block,
				CleanedCmd: cmd,
				Reason:     fmt.Sprintf("Command blocked: %s", rule.desc),
			}
		}
	}

	// ---- Layer 2: Ask rules (destructive/suspicious, needs confirmation) ----
	var askReasons []string
	for _, rule := range g.askRules {
		if rule.pattern.MatchString(cmd) {
			askReasons = append(askReasons, rule.desc)
		}
	}

	if len(askReasons) > 0 {
		result.Behavior = Ask
		result.Reason = fmt.Sprintf("Command requires confirmation: %s", strings.Join(askReasons, "; "))
	}

	// ---- Layer 3: Destructive warnings (informational only) ----
	result.Warnings = g.destructiveWarnings(cmd)

	// ---- Apply cleaning rules ----
	cleaned := cmd
	for _, rule := range g.cleanRules {
		cleaned = rule.pattern.ReplaceAllString(cleaned, "")
	}
	result.CleanedCmd = strings.TrimSpace(cleaned)

	return result
}

// preChecks runs Claude Code-style pre-validation that catches parser
// differentials and injection vectors before pattern matching.
func (g *CommandGate) preChecks(cmd string) GateResult {
	// Control characters (null bytes, etc.) — bash silently drops them
	// but they confuse our regex checks.
	for _, ch := range cmd {
		if ch < 0x20 && ch != '\n' && ch != '\r' && ch != '\t' {
			return GateResult{
				Behavior:   Ask,
				CleanedCmd: cmd,
				Reason:     "Command contains non-printable control characters that could bypass security checks",
			}
		}
		if ch == 0x7F {
			return GateResult{
				Behavior:   Ask,
				CleanedCmd: cmd,
				Reason:     "Command contains DEL control character",
			}
		}
	}

	// Unicode whitespace — shell-quote and bash disagree on word boundaries
	for _, ch := range cmd {
		if unicode.IsSpace(ch) && ch != ' ' && ch != '\t' && ch != '\n' && ch != '\r' {
			return GateResult{
				Behavior:   Ask,
				CleanedCmd: cmd,
				Reason:     "Command contains Unicode whitespace that could bypass security checks",
			}
		}
	}

	// Multiple commands via semicolons with suspicious patterns
	if strings.Contains(cmd, ";") {
		parts := splitSemicolons(cmd)
		for _, part := range parts {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			// Check if any sub-command is catastrophic
			for _, rule := range g.blockRules {
				if rule.pattern.MatchString(part) {
					return GateResult{
						Behavior:   Block,
						CleanedCmd: cmd,
						Reason:     fmt.Sprintf("Command blocked: %s (in compound command)", rule.desc),
					}
				}
			}
		}
	}

	return GateResult{Behavior: Allow, CleanedCmd: cmd}
}

// destructiveWarnings returns informational warnings about destructive patterns.
// These are shown to the user but don't affect the allow/ask/block decision.
// Modeled after Claude Code's destructiveCommandWarning.ts.
func (g *CommandGate) destructiveWarnings(cmd string) []string {
	// These are separate from askRules — they provide context in the
	// permission dialog without changing the gate behavior.
	warnings := []string{}

	destructivePatterns := []struct {
		pattern *regexp.Regexp
		warning string
	}{
		{regexp.MustCompile(`(?i)\bgit\s+reset\s+--hard\b`), "Note: may discard uncommitted changes"},
		{regexp.MustCompile(`(?i)\bgit\s+push\b[^;&|\n]*\s(--force|-f)\b`), "Note: may overwrite remote history"},
		{regexp.MustCompile(`(?i)\bgit\s+clean\b[^;&|\n]*-[a-zA-Z]*f`), "Note: may permanently delete untracked files"},
		{regexp.MustCompile(`(?i)\bgit\s+checkout\s+(--\s+)?\.\s*($|[;&|])`), "Note: may discard all working tree changes"},
		{regexp.MustCompile(`(?i)\bgit\s+restore\s+(--\s+)?\.\s*($|[;&|])`), "Note: may discard all working tree changes"},
		{regexp.MustCompile(`(?i)\bgit\s+stash\s+(drop|clear)\b`), "Note: may permanently remove stashed changes"},
		{regexp.MustCompile(`(?i)\bgit\s+branch\s+-D\b`), "Note: may force-delete a branch"},
		{regexp.MustCompile(`(?i)\bgit\s+(commit|push|merge)\b[^;&|\n]*--no-verify\b`), "Note: may skip safety hooks"},
		{regexp.MustCompile(`(?i)\bgit\s+commit\b[^;&|\n]*--amend\b`), "Note: may rewrite the last commit"},
		{regexp.MustCompile(`(?i)\brm\s+-[a-zA-Z]*r`), "Note: recursively removing files"},
		{regexp.MustCompile(`(?i)\brm\s+-[a-zA-Z]*f`), "Note: force-removing files without confirmation"},
		{regexp.MustCompile(`(?i)\b(DROP|TRUNCATE)\s+(TABLE|DATABASE)`), "Note: may drop or truncate database objects"},
		{regexp.MustCompile(`(?i)\bkubectl\s+delete\b`), "Note: may delete Kubernetes resources"},
		{regexp.MustCompile(`(?i)\bterraform\s+destroy\b`), "Note: may destroy Terraform infrastructure"},
	}

	for _, p := range destructivePatterns {
		if p.pattern.MatchString(cmd) {
			warnings = append(warnings, p.warning)
		}
	}

	return warnings
}

// IsDestructive returns true if the command matches any block or ask rule.
func (g *CommandGate) IsDestructive(cmd string) bool {
	for _, rule := range g.blockRules {
		if rule.pattern.MatchString(cmd) {
			return true
		}
	}
	return false
}

// splitSemicolons splits a command on unquoted semicolons.
func splitSemicolons(cmd string) []string {
	var parts []string
	var current strings.Builder
	inSingleQuote := false
	inDoubleQuote := false

	for i := 0; i < len(cmd); i++ {
		ch := cmd[i]

		if ch == '\\' && !inSingleQuote && i+1 < len(cmd) {
			current.WriteByte(ch)
			current.WriteByte(cmd[i+1])
			i++
			continue
		}

		if ch == '\'' && !inDoubleQuote {
			inSingleQuote = !inSingleQuote
			current.WriteByte(ch)
			continue
		}

		if ch == '"' && !inSingleQuote {
			inDoubleQuote = !inDoubleQuote
			current.WriteByte(ch)
			continue
		}

		if ch == ';' && !inSingleQuote && !inDoubleQuote {
			parts = append(parts, current.String())
			current.Reset()
			continue
		}

		current.WriteByte(ch)
	}

	if current.Len() > 0 {
		parts = append(parts, current.String())
	}

	return parts
}
