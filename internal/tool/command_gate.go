package tool

import (
	"fmt"
	"path/filepath"
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
	// #814: evaluate ONLY against the quote/heredoc-inert form — the shell
	// never executes quoted interiors or heredoc bodies as command args.
	quotedInert bool
}

type cleanRule struct {
	desc    string
	pattern *regexp.Regexp
}

// NewCommandGate creates the command safety gate with all rules.
func NewCommandGate() *CommandGate {
	g := &CommandGate{}

	// rm dispersed-form subpatterns (#384): f-flag, r-flag, critical path.
	// RE2 has no lookarounds, so "all three in any order" below is spelled
	// as the 6 permutations of F/R/P — this catches dispersed forms like
	// `rm --force /etc --recursive` where the target sits BETWEEN flags.
	rmF := `(?:-[a-zA-Z]*f[a-zA-Z]*|--force)`
	rmR := `(?:-[a-zA-Z]*r[a-zA-Z]*|--recursive)`
	// #813: bare `/` matched the leading slash of ANY absolute path, so
	// routine build hygiene (rm -rf /tmp/build-cache) was hard-Blocked as
	// root destruction. Keep only genuine catastrophic roots; /tmp and
	// /var/folders style caches are deliberate non-members.
	rmP := `(?:(~|/home|/Users|/etc|/var|/usr|/bin|/sbin|/lib|/boot)\b|/dev/)`
	// #436: newline is a shell command separator too — the gap class must
	// not cross it, else a benign multi-line script (`# comment` + `rm -f x`
	// + `grep -r ... /etc`) let the grep segment contribute -r and /etc to
	// the rm segment and hard-blocked the whole command.
	rmGap := `[^;&|\n]*`

	// ================================================================
	// BLOCK — catastrophic commands with zero legitimate use
	// ================================================================
	g.blockRules = []*gateRule{

		// --- Filesystem destruction ---
		// #814: quotedInert — the shell never executes quoted interiors or
		// heredoc bodies as rm arguments, so these rules must not assemble
		// F/R/P elements across quotes (`grep 'rm -rf /etc/hosts' README.md`,
		// script-writing heredocs).
		{kind: "catastrophic", desc: "recursive force delete of root/critical directory", quotedInert: true,
			pattern: regexp.MustCompile(`(?i)\brm\s+(-[a-zA-Z]*r[a-zA-Z]*f[a-zA-Z]*|-[a-zA-Z]*f[a-zA-Z]*r[a-zA-Z]*|--recursive\s+--force|--force\s+--recursive)\s+(/(\s|$)|~/|/home/?|/Users/?|/etc/?|/var/?|/usr/?|/bin/?|/sbin/?|/lib/?|/boot/?|System|Applications|/dev/)`)},
		{kind: "catastrophic", desc: "recursive force delete (alternate flag order)", quotedInert: true,
			// Long-flag alternation uses literal --recursive/--force: inside a
			// raw string the old \\-\\- form only matched a literal backslash
			// before each dash, so the long-flag branch was dead code (#384).
			// RE2 has no lookarounds, so "all three elements in any order" is
			// expressed as the 6 permutations of F(f-flag), R(r-flag), P(path)
			// — this also fixes the dispersed form `rm --force /etc
			// --recursive` where the target sits BETWEEN the flags (the old
			// pattern required the path strictly after both flags).
			// #410: the gap between elements must not cross command separators
			// (; & |) — otherwise a later, unrelated segment of a compound
			// command could contribute the "missing" flag or path anchor and
			// hard-block benign commands like `rm -f old.tar && grep -r x /etc`.
			// The rule therefore only evaluates the single rm sub-command.
			pattern: regexp.MustCompile(`(?i)\brm\s+(?:` +
				rmGap + rmF + rmGap + rmR + rmGap + rmP + `|` +
				rmGap + rmF + rmGap + rmP + rmGap + rmR + `|` +
				rmGap + rmR + rmGap + rmF + rmGap + rmP + `|` +
				rmGap + rmR + rmGap + rmP + rmGap + rmF + `|` +
				rmGap + rmP + rmGap + rmF + rmGap + rmR + `|` +
				rmGap + rmP + rmGap + rmR + rmGap + rmF +
				`)`)},
		{kind: "catastrophic", desc: "disk format/erase",
			pattern: regexp.MustCompile(`(?i)\b(mkfs\.|dd\s+if=.*of=/dev/|diskutil\s+eraseDisk|diskutil\s+partitionDisk)`)},
		{kind: "catastrophic", desc: "fork bomb",
			pattern: regexp.MustCompile(`(?i)(:\(\)\{\s*:\|:\&\s*\}|fork\s+bomb)`)},
		{kind: "catastrophic", desc: "filesystem wipe via chmod",
			pattern: regexp.MustCompile(`(?i)\bchmod\s+(-R\s+)?000\s+/`)},
		// --- Windows filesystem destruction ---
		{kind: "catastrophic", desc: "Windows recursive delete (rd/rmdir /s /q on system drive)",
			pattern: regexp.MustCompile(`(?i)\b(rd|rmdir)\s+.*(/[a-z]*s[a-z]*q[a-z]*|/s\s*/q|/q\s*/s)\s+"?([Cc]:\\"?\s*$|[Cc]:\\(Windows|Users|Program))`)},
		{kind: "catastrophic", desc: "Windows force delete of system files (del /s /q on system dir)",
			pattern: regexp.MustCompile(`(?i)\bdel\s+.*(/[a-z]*s[a-z]*q[a-z]*|/s\s*/q|/q\s*/s)\s+"?[Cc]:\\(Windows|Program)`)},
		{kind: "catastrophic", desc: "Windows disk format (format command)",
			pattern: regexp.MustCompile(`(?i)\bformat\s+"?[A-Za-z]:("?$|[\s/"])`)},

		// --- System control ---
		{kind: "catastrophic", desc: "system shutdown/reboot",
			pattern: regexp.MustCompile(`(?i)\b(shutdown\b.*(-h|-r|now)|\breboot\b|\bhalt\b|\bpoweroff\b|init\s+[06])`)},
		{kind: "catastrophic", desc: "kernel module manipulation",
			pattern: regexp.MustCompile(`(?i)\b(rmmod|insmod|modprobe)\s+`)},

		// --- Credential exfiltration ---
		{kind: "catastrophic", desc: "credential exfiltration via network",
			// #817: the two file-path alternates previously carried \b anchors
			// before NON-word chars (~ and /) — a boundary never exists there,
			// so `curl -T ~/.ssh/id_rsa` and `-d @/etc/passwd` passed the hard
			// Block. Path alternates are now anchored by their own delimiters.
			pattern: regexp.MustCompile(`(?i)\b(curl|wget|nc|ncat)\s+.*((~|@|=|\s|\()/\.(ssh|gnupg|aws|env)/|(^|[\s=@(])/etc/(passwd|shadow)|--post-file.*\.(ssh|aws|env|gnupg)|<\s*~?/\.\w+/)`)},

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
		// Windows recursive delete — #816: the old /[a-z]{0,3}s[a-z]{0,2}
		// token shape matched plain Unix dirs (/srv, /src, /sys). Real
		// Windows invocations carry a drive letter or backslash path; require
		// that context so Unix rmdir/del stop false-triggering.
		{kind: "destructive", desc: "Windows recursive directory delete (rd/rmdir /s)",
			pattern: regexp.MustCompile(`(?i)\b(rd|rmdir)\s+([^&|;\n]*[/\\]s(\s|$)[^&|;\n]*([a-z]:[\\/]|\\\\)|[a-z]:[\\/][^&|;\n]*[/\\]s(\s|$))`)},
		{kind: "destructive", desc: "Windows recursive file delete (del /s)",
			pattern: regexp.MustCompile(`(?i)\bdel\s+([^&|;\n]*[/\\]s(\s|$)[^&|;\n]*([a-z]:[\\/]|\\\\)|[a-z]:[\\/][^&|;\n]*[/\\]s(\s|$))`)},
		{kind: "destructive", desc: "Windows registry deletion (reg delete)",
			pattern: regexp.MustCompile(`(?i)\breg\s+delete\b`)},
		{kind: "destructive", desc: "overwrite /etc/hosts",
			pattern: regexp.MustCompile(`(?i)>\s*/etc/hosts\b`)},
		{kind: "destructive", desc: "overwrite SSH config/known_hosts",
			pattern: regexp.MustCompile(`(?i)>\s*~/\.ssh/(config|known_hosts)\b`)},

		// --- Privilege escalation ---
		{kind: "security", desc: "sudo command requires elevated privileges",
			pattern: regexp.MustCompile(`(?i)\bsudo\s+`)},

		// --- Network-based risks ---
		{kind: "security", desc: "piping remote content to shell",
			// #815: trailing \b — 'sh' is a prefix of sha256sum/shasum/shfmt,
			// so checksum verification (curl ... | sha256sum -c) was flagged.
			pattern: regexp.MustCompile(`(?i)(curl|wget)\s+.*\|\s*(ba)?sh\b`)},
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
	// #436: shell does NOT treat separators inside quotes as separators,
	// but our regex gap class consumed them — `rm -f "a;b" -r /etc`
	// de-blocked to a mere Ask. Match against a normalized view where
	// quoted segments keep their outer quotes but drop inner separator
	// noise... simplest correct approach: match BOTH the raw and the
	// quote-stripped-interior form; if the quote-stripped form blocks, the
	// user's real intent still contains the destructive combination.
	matchCmd := cmd
	if norm := stripQuotedSeparators(cmd); norm != cmd {
		matchCmd = cmd + "\n" + norm
	}
	inertCmd := blankQuotedAndHeredocs(cmd)
	for _, rule := range g.blockRules {
		target := matchCmd
		if rule.quotedInert {
			target = inertCmd
		}
		if rule.pattern.MatchString(target) {
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

	// ---- Layer 3b: Interactive command warning ----
	// Detect commands that will hang indefinitely waiting for stdin or that
	// run forever. These waste the full timeout. We add it as a warning so
	// the agent sees it in the tool result.
	if interactive := g.InteractiveCommandWarning(cmd); interactive != "" {
		result.Warnings = append(result.Warnings, interactive)
	}

	// ---- Apply cleaning rules ----
	cleaned := cmd
	for _, rule := range g.cleanRules {
		cleaned = rule.pattern.ReplaceAllString(cleaned, "")
	}
	result.CleanedCmd = strings.TrimSpace(cleaned)

	return result
}

// stripQuotedSeparators removes shell separator characters (; & |) that
// appear INSIDE quoted segments (#436). The regex gate treats those chars
// as command separators, but a real shell does not — so `rm -f "a;b" -r /etc`
// must be evaluated as if the quoted content contained no separator, letting
// the rule see the true destructive flag combination. Unquoted separators
// are preserved so segment scoping (#410) still applies.
func stripQuotedSeparators(cmd string) string {
	var b strings.Builder
	b.Grow(len(cmd))
	var quote byte // 0 = outside quotes
	for i := 0; i < len(cmd); i++ {
		c := cmd[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
				b.WriteByte(c)
			} else if c == ';' || c == '&' || c == '|' {
				// separator inside quotes — drop it
			} else {
				b.WriteByte(c)
			}
		case c == '"' || c == '\'':
			quote = c
			b.WriteByte(c)
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// blankQuotedAndHeredocs replaces quoted-string interiors and heredoc
// bodies with inert filler (#814). The rm permutation rules must not
// assemble F/R/P elements from text the shell will never execute as rm
// arguments — `grep 'rm -rf /etc/hosts' README.md` or a script-writing
// heredoc mentioning rm must not hard-Block the enclosing legitimate
// command.
func blankQuotedAndHeredocs(cmd string) string {
	lines := strings.Split(cmd, "\n")
	out := make([]string, 0, len(lines))
	heredoc := ""
	for _, line := range lines {
		if heredoc != "" {
			if strings.TrimSpace(line) == heredoc {
				heredoc = ""
				out = append(out, line)
			} else {
				out = append(out, " ")
			}
			continue
		}
		out = append(out, blankQuotes(line))
		if m := heredocOpenerRe.FindStringSubmatch(line); m != nil {
			heredoc = m[1]
		}
	}
	return strings.Join(out, "\n")
}

var heredocOpenerRe = regexp.MustCompile(`<<-?\s*['\"]?([A-Za-z_][A-Za-z0-9_]*)`)

// blankQuotes replaces quoted interiors (single/double) with a space.
func blankQuotes(line string) string {
	var b strings.Builder
	b.Grow(len(line))
	quote := byte(0)
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
				b.WriteByte(c)
			}
			// interior blanked
		case c == '"' || c == '\'':
			quote = c
			b.WriteByte(c)
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
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

// InteractiveCommandWarning detects commands that will hang indefinitely waiting
// for stdin or that run forever without exiting. These are the #1 cause of
// 30-minute timeout waste in AI coding agents.
//
// Three categories:
//  1. Bare REPL invocations — `python`, `node`, `irb` without a script argument.
//     These drop into a read-eval-print loop that blocks on stdin forever.
//  2. Interactive pagers/editors — `vim`, `nano`, `less`, `more` always wait
//     for keystrokes and never exit on their own.
//  3. Infinite-follow/monitor commands — `tail -f`, `top`, `watch` run forever
//     by design.
//
// Returns a non-empty warning string if detected, or "" otherwise.
func (g *CommandGate) InteractiveCommandWarning(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return ""
	}

	// Handle compound commands: check each sub-command independently.
	// Split on common separators (|, ;, &&, ||) and check each part.
	// #337: segments on the RIGHT side of a pipe receive stdin from the
	// upstream segment, which closes on completion — bare cat/tee there read
	// EOF and exit immediately. The stdin-reader infinite check is skipped
	// for pipe-fed segments (it stays active for the first segment).
	parts, pipeFed := splitCompoundCommandWithPipes(cmd)
	for idx, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if w := checkInteractivePartPart(part, idx > 0 && pipeFed[idx]); w != "" {
			return w
		}
	}
	return ""
}

// checkInteractivePartPart is checkInteractivePart with pipe-context awareness
// (#337): pipeFed=true means the segment's stdin comes from the upstream
// pipeline, so bare stdin-readers (cat/tee/sleep) exit on EOF instead of
// hanging.
func checkInteractivePartPart(cmd string, pipeFed bool) string {
	if pipeFed {
		fields := strings.Fields(cmd)
		if len(fields) > 0 {
			bin := strings.ToLower(filepath.Base(fields[0]))
			if bin == "cat" || bin == "tee" || bin == "sleep" {
				return ""
			}
		}
	}
	return checkInteractivePart(cmd)
}

// splitCompoundCommand splits on shell separators (|, ;, &&, ||).
// Each returned part is a pipeline segment. #337: the caller needs to know
// which segments are pipe-fed (right side of a single '|'), so the splitter
// records separator kinds alongside parts.
func splitCompoundCommand(cmd string) []string {
	segs, _ := splitCompoundCommandWithPipes(cmd)
	return segs
}

// splitCompoundCommandWithPipes additionally returns pipeFed, parallel to
// segs: pipeFed[i] is true when segment i receives stdin from a pipe (the
// separator before it was a single '|'). The first segment is never pipe-fed.
func splitCompoundCommandWithPipes(cmd string) (segs []string, pipeFed []bool) {
	var parts []string
	var current strings.Builder
	inSingleQuote := false
	inDoubleQuote := false
	lastSepWasPipe := false

	i := 0
	for i < len(cmd) {
		ch := cmd[i]

		// Handle escape sequences outside single quotes.
		if ch == '\\' && !inSingleQuote && i+1 < len(cmd) {
			current.WriteByte(ch)
			current.WriteByte(cmd[i+1])
			i += 2
			continue
		}

		// Toggle quote state.
		if ch == '\'' && !inDoubleQuote {
			inSingleQuote = !inSingleQuote
			current.WriteByte(ch)
			i++
			continue
		}
		if ch == '"' && !inSingleQuote {
			inDoubleQuote = !inDoubleQuote
			current.WriteByte(ch)
			i++
			continue
		}

		// Check for top-level separators when not quoted.
		if !inSingleQuote && !inDoubleQuote {
			if sep, isPipe, advance := matchSeparator(cmd, i); sep {
				parts = append(parts, current.String())
				pipeFed = append(pipeFed, lastSepWasPipe)
				current.Reset()
				lastSepWasPipe = isPipe
				i += advance
				continue
			}
		}

		current.WriteByte(ch)
		i++
	}

	if current.Len() > 0 {
		parts = append(parts, current.String())
		pipeFed = append(pipeFed, lastSepWasPipe)
	}
	return parts, pipeFed
}

// matchSeparator checks if position i in cmd is a shell separator (|, ;, &&, ||).
// Returns (matched, isPipe, advance): isPipe is true only for a single '|'
// (a pipeline feeding the next segment's stdin); '||' is a logical OR whose
// right side does NOT receive a pipe (#337).
func matchSeparator(cmd string, i int) (matched, isPipe bool, advance int) {
	ch := cmd[i]
	if ch == ';' || ch == '|' {
		if i+1 < len(cmd) && ch == '|' && cmd[i+1] == '|' {
			return true, false, 2
		}
		return true, ch == '|', 1
	}
	if i+1 < len(cmd) && ch == '&' && cmd[i+1] == '&' {
		return true, false, 2
	}
	return false, false, 0
}

// checkInteractivePart checks a single command pipeline segment for interactive
// behavior.
func checkInteractivePart(cmd string) string {
	cmd = strings.TrimSpace(cmd)

	// Extract the first word (the command name) and the remaining arguments.
	fields := strings.Fields(cmd)
	if len(fields) == 0 {
		return ""
	}

	// Strip any path prefix from the command (e.g., /usr/bin/python → python).
	binName := filepath.Base(fields[0])
	binName = strings.ToLower(binName)
	// Strip common Windows extensions.
	for _, ext := range []string{".exe", ".bat", ".cmd", ".ps1"} {
		binName = strings.TrimSuffix(binName, ext)
	}

	args := fields[1:]

	// --- Category 1: Bare REPL invocations (no script argument) ---
	// These launch a REPL when invoked without arguments. With -c, -e, or a
	// script file, they run one-shot and exit normally.
	if isBareREPL(binName, args) {
		return fmt.Sprintf(
			"Command %q appears to be a bare REPL invocation that will wait for stdin input indefinitely "+
				"(30-minute timeout). This will block the agent loop for the full timeout duration. "+
				"Instead: pass a script file (%s script.py), use -c/%s -e for inline code, or use start_command for interactive sessions.",
			binName, binName, binName)
	}

	// --- Category 2: Interactive pagers and editors ---
	// These ALWAYS wait for keyboard input regardless of arguments.
	if isInteractiveApp(binName) {
		// Check for non-interactive overrides that make these safe.
		if hasNonInteractiveOverride(binName, args) {
			return ""
		}
		suggestion := interactiveAppSuggestion(binName)
		return fmt.Sprintf(
			"Command %q is an interactive application that will wait for keyboard input indefinitely "+
				"(30-minute timeout). %s Alternatively, use start_command for interactive sessions.",
			binName, suggestion)
	}

	// --- Category 3: Infinite-follow and monitor commands ---
	// tail -f, top, watch, etc. run forever by design.
	if isInfiniteCommand(binName, args) {
		suggestion := infiniteCommandSuggestion(binName)
		return fmt.Sprintf(
			"Command %q runs indefinitely and will not exit on its own (30-minute timeout). %s "+
				"Use start_command for long-running processes, or add appropriate arguments to make it exit.",
			binName, suggestion)
	}

	return ""
}

// isBareREPL returns true if the binary is a REPL interpreter invoked without
// a script file or inline-code flag.
func isBareREPL(bin string, args []string) bool {
	repls := map[string]bool{
		"python": true, "python2": true, "python3": true,
		"node": true, "deno": true, "bun": true,
		"irb": true, "pry": true,
		"php": true, "psy": true,
		"lua":   true,
		"erl":   true,
		"swipl": true,
		"r":     true,
		"psql":  true, "mysql": true, "sqlite3": true,
		"redis-cli": true, "mongosh": true, "mongo": true,
		"elixir": true,
		"jshell": true, "kotlin": true, "kotlinc": true,
		"ghci":  true,
		"ocaml": true,
		"clj":   true,
		"tclsh": true,
		"bc":    true,
	}
	if !repls[bin] {
		return false
	}

	// Common flags that make REPLs non-interactive (run-once).
	// - python: -c (inline code), -m (run module)
	// - node: -e (inline), --eval
	// - deno: -e (inline)
	// - bun: -e (inline)
	// - sqlite3: < file.sql or .read
	// - bc: -q (quiet, but still interactive without input)
	nonInteractiveFlags := map[string]bool{
		"-c": true, "--command": true,
		"-e": true, "--eval": true,
		"-m": true,
	}
	// If any argument is a non-interactive flag or a file path (not starting
	// with -), the REPL will run one-shot, not hang.
	for _, a := range args {
		if nonInteractiveFlags[a] {
			return false
		}
		// A positional argument that looks like a file (has a dot, or is a
		// relative/absolute path) means a script file — non-interactive.
		if !strings.HasPrefix(a, "-") {
			return false
		}
	}

	// Special case: sqlite3 with a database file but no piped input.
	// `sqlite3 db.sqlite` still drops into interactive mode.
	// However, `sqlite3 db.sqlite < dump.sql` or with a SQL command arg is fine.
	// We only flag truly bare invocations.
	if bin == "sqlite3" || bin == "mysql" || bin == "psql" || bin == "redis-cli" {
		// These hang with just a database/connection name.
		return true
	}

	return true
}

// isInteractiveApp returns true for pagers and editors that always wait for
// keyboard input.
func isInteractiveApp(bin string) bool {
	apps := map[string]bool{
		// Editors
		"vim": true, "vi": true, "nvim": true, "nano": true, "emacs": true,
		"pico": true, "micro": true, "helix": true, "hx": true,
		"ed": true, "ex": true,
		// Pagers
		"less": true, "more": true, "most": true,
		// Interactive monitors
		"top": true, "htop": true, "btop": true, "iotop": true, "atop": true,
		"glances": true,
		// Interactive shells launched explicitly
		"bash": true, "zsh": true, "sh": true, "fish": true, "csh": true,
		"tcsh": true, "pwsh": true, "powershell": true,
	}
	return apps[bin]
}

// nonInteractiveOverrides maps binary names to the set of flags that make
// them non-interactive (exit after one-shot).
var nonInteractiveOverrides = map[string]map[string]bool{
	"less":       {"-E": true, "--quit-at-eof": true, "-F": true, "--quit-if-one-screen": true},
	"bash":       {"-c": true},
	"sh":         {"-c": true},
	"zsh":        {"-c": true},
	"fish":       {"-c": true},
	"top":        {"-b": true, "-n": true},
	"htop":       {"-C": true},
	"powershell": {"-Command": true, "-c": true, "-File": true},
	"pwsh":       {"-Command": true, "-c": true, "-File": true},
}

// hasNonInteractiveOverride checks if the command has flags that make an
// otherwise interactive app non-interactive.
func hasNonInteractiveOverride(bin string, args []string) bool {
	overrides, ok := nonInteractiveOverrides[bin]
	if !ok {
		return false
	}
	for _, a := range args {
		if overrides[a] {
			return true
		}
		// Combined short flags: top -n1, top -bn1
		if bin == "top" && strings.HasPrefix(a, "-n") {
			return true
		}
	}
	return false
}

// interactiveAppSuggestion returns a helpful suggestion for how to replace the
// interactive command with a non-interactive alternative.
func interactiveAppSuggestion(bin string) string {
	switch bin {
	case "vim", "vi", "nvim", "nano", "emacs", "pico", "micro", "helix", "hx", "ed", "ex":
		return "Use edit_file/write_file/read_file tools instead of a text editor."
	case "less", "more", "most":
		return "Use read_file or run_command with 'cat' to view file contents."
	case "top", "htop", "btop", "iotop", "atop", "glances":
		return "Use run_command with 'ps aux' or specific metrics commands instead."
	case "bash", "zsh", "sh", "fish", "csh", "tcsh":
		return "Use run_command with the shell script (bash -c 'commands') instead of an interactive shell."
	case "powershell", "pwsh":
		return "Use run_command with 'pwsh -Command \"...\"' instead of an interactive shell."
	default:
		return "Use a non-interactive alternative."
	}
}

// infiniteCmdCheckers maps binary names to functions that check whether the
// command runs forever. Each checker receives the args and returns true if the
// invocation is infinite/hanging.
var infiniteCmdCheckers = map[string]func(args []string) bool{
	"tail":   isInfiniteTail,
	"cat":    isBareStdinReader,
	"tee":    isBareStdinReader,
	"sleep":  isBareStdinReader,
	"nc":     isInfiniteNetcat,
	"ncat":   isInfiniteNetcat,
	"netcat": isInfiniteNetcat,
	"watch":  func(_ []string) bool { return true },
	"yes":    func(_ []string) bool { return true },
}

// isInfiniteCommand returns true for commands that run forever by design.
func isInfiniteCommand(bin string, args []string) bool {
	checker, ok := infiniteCmdCheckers[bin]
	if !ok {
		return false
	}
	return checker(args)
}

// isInfiniteTail detects tail -f / --follow / -F / combined -fq flags.
func isInfiniteTail(args []string) bool {
	for _, a := range args {
		if a == "-f" || a == "--follow" || strings.HasPrefix(a, "-F") {
			return true
		}
		// Combined short flags containing 'f' (e.g. -fq)
		if strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.Contains(a, "f") {
			return true
		}
	}
	return false
}

// isBareStdinReader returns true when a command that normally reads from stdin
// is invoked with no arguments (cat, tee, sleep without duration).
func isBareStdinReader(args []string) bool {
	return len(args) == 0
}

// isInfiniteNetcat detects nc/ncat/netcat in listen mode (-l / --listen).
func isInfiniteNetcat(args []string) bool {
	for _, a := range args {
		if a == "-l" || a == "--listen" {
			return true
		}
	}
	return false
}

// infiniteCommandSuggestion returns a helpful suggestion for making an infinite
// command exit.
func infiniteCommandSuggestion(bin string) string {
	switch bin {
	case "tail":
		return "Use tail without -f to read the end of a file, or use run_command with a line count (tail -n 100 file)."
	case "watch":
		return "Use run_command to run the command once instead of repeatedly."
	case "yes":
		return "Pipe to head -n 1 to limit output (yes | head -n 1)."
	case "cat", "tee":
		return "Provide a file argument or pipe input instead of reading from stdin."
	case "nc", "ncat", "netcat":
		return "Provide a timeout or use non-interactive alternatives for network testing."
	default:
		return "Add a timeout or exit condition."
	}
}

// IsDestructive returns true if the command matches any block or ask rule.
// #444: ask rules are included — the gate classifies 'rm -rf <relative>',
// 'git reset --hard', 'terraform destroy' etc. as ask-level destructive,
// and the doc contract (and any danger-assessing caller) must see them.
func (g *CommandGate) IsDestructive(cmd string) bool {
	for _, rule := range g.blockRules {
		if rule.pattern.MatchString(cmd) {
			return true
		}
	}
	for _, rule := range g.askRules {
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
