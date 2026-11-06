package tool

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

type GateBehavior int

const (
	Allow GateBehavior = iota
	Ask
	Block
)

type GateResult struct {
	Behavior   GateBehavior
	CleanedCmd string
	Reason     string
	Warnings   []string
}

func (r GateResult) Allowed() bool { return r.Behavior == Allow }

func (r GateResult) NeedsConfirmation() bool { return r.Behavior == Ask }

func (r GateResult) IsBlocked() bool { return r.Behavior == Block }

type CommandGate struct {
	blockRules []*gateRule
	askRules   []*gateRule
	cleanRules []cleanRule
}

type gateRule struct {
	desc           string
	pattern        *regexp.Regexp
	kind           string
	quotedInert    bool
	benignPrefixes []string
}

type cleanRule struct {
	desc    string
	pattern *regexp.Regexp
	replace string
}

func NewCommandGate() *CommandGate {
	g := &CommandGate{}

	rmF := `(?:-[a-zA-Z]*f[a-zA-Z]*|--force)`
	rmR := `(?:-[a-zA-Z]*r[a-zA-Z]*|--recursive)`

	rmP := `(?:(~/|~(?:[ \t]|$))|(/home|/Users|/etc|/var|/usr|/bin|/sbin|/lib|/boot)\b|/dev/)`

	rmGap := `[^;&|\n]*`

	g.blockRules = []*gateRule{

		{kind: "catastrophic", desc: "recursive force delete of root/critical directory", quotedInert: true,
			benignPrefixes: []string{"/var/folders", "/private/var/folders", "/tmp/"},
			pattern:        regexp.MustCompile(`(?i)\brm\s+(-[a-zA-Z]*r[a-zA-Z]*f[a-zA-Z]*|-[a-zA-Z]*f[a-zA-Z]*r[a-zA-Z]*|--recursive\s+--force|--force\s+--recursive)\s+(/(\s|$)|~(\s|$)|~/|/home/?|/Users/?|/etc/?|/var/?|/usr/?|/bin/?|/sbin/?|/lib/?|/boot/?|System|Applications|/dev/)`)},
		{kind: "catastrophic", desc: "recursive force delete (alternate flag order)", quotedInert: true,
			benignPrefixes: []string{"/var/folders", "/private/var/folders", "/tmp/"},

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

			pattern: regexp.MustCompile(`(?i)\bchmod\s+(-R\s+)?000\s+/($|[\s;"'&|])`)},

		{kind: "catastrophic", desc: "Windows recursive delete (rd/rmdir /s /q on system drive)",
			pattern: regexp.MustCompile(`(?i)\b(rd|rmdir)\s+.*(/[a-z]*s[a-z]*q[a-z]*|/s\s*/q|/q\s*/s)\s+"?([Cc]:\\"?\s*$|[Cc]:\\(Windows|Users|Program))`)},
		{kind: "catastrophic", desc: "Windows force delete of system files (del /s /q on system dir)",
			pattern: regexp.MustCompile(`(?i)\bdel\s+.*(/[a-z]*s[a-z]*q[a-z]*|/s\s*/q|/q\s*/s)\s+"?[Cc]:\\(Windows|Program)`)},
		{kind: "catastrophic", desc: "Windows disk format (format command)",
			pattern: regexp.MustCompile(`(?i)\bformat\s+"?[A-Za-z]:("?$|[\s/"])`)},

		{kind: "catastrophic", desc: "system shutdown/reboot",

			quotedInert: true,
			pattern:     regexp.MustCompile(`(?i)\b(shutdown\b.*(-h|-r|now)|\breboot\b|\bhalt\b|\bpoweroff\b|init\s+[06])`)},
		{kind: "catastrophic", desc: "kernel module manipulation",
			pattern: regexp.MustCompile(`(?i)\b(rmmod|insmod|modprobe)\s+`)},

		{kind: "catastrophic", desc: "credential exfiltration via network",

			pattern: regexp.MustCompile(`(?i)\b(curl|wget|nc|ncat)\s+.*((~|@|=|\s|\()/\.(ssh|gnupg|aws|env)/|(^|[\s=@(])/etc/(passwd|shadow)|--post-file.*\.(ssh|aws|env|gnupg)|<\s*~?/\.\w+/)`)},

		{kind: "catastrophic", desc: "history manipulation to hide tracks",

			quotedInert: true,
			pattern:     regexp.MustCompile(`(?i)(unset\s+HISTFILE|export\s+HISTFILE=/dev/null|history\s+(-c|--clear)|>\s*~?/\.(bash_history|zsh_history))`)},

		{kind: "catastrophic", desc: "disable security tooling",

			quotedInert: true,
			pattern:     regexp.MustCompile(`(?i)\b(killall|pkill)\s+(-[0-9]+\s+)?(Little.?Snitch|LuLu|SecuritySpy|fseventsd|sandboxd)`)},

		{kind: "catastrophic", desc: "overwrite critical system files",
			pattern: regexp.MustCompile(`(?i)>\s*/etc/(passwd|shadow|sudoers|fstab)\b`)},
		{kind: "catastrophic", desc: "overwrite SSH authorized_keys",
			pattern: regexp.MustCompile(`(?i)>\s*~/\.ssh/authorized_keys\b`)},
		{kind: "catastrophic", desc: "recursive chown on root",

			pattern: regexp.MustCompile(`(?i)\bchown\s+-R\s+\S+\s+/($|[\s;"'&|])`)},
	}

	g.askRules = []*gateRule{

		{kind: "injection", desc: "decoded payload piped into shell",
			pattern: regexp.MustCompile(`(?i)\b(base64|openssl\s+enc\s+-d|xxd\s+-r\s+-p)\b.*\|\s*(ba)?sh\b`)},

		{kind: "destructive", desc: "recursive force delete", quotedInert: true,
			pattern: regexp.MustCompile(`(?i)\brm\s+(-[a-zA-Z]*f[a-zA-Z]*\s+)?-[a-zA-Z]*r[a-zA-Z]*\s+`)},
		{kind: "destructive", desc: "force delete without confirmation", quotedInert: true,
			pattern: regexp.MustCompile(`(?i)\brm\s+-[a-zA-Z]*f[a-zA-Z]*\s+`)},

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

		{kind: "security", desc: "sudo command requires elevated privileges", quotedInert: true,
			pattern: regexp.MustCompile(`(?i)\bsudo\s+`)},

		{kind: "security", desc: "piping remote content to shell",

			pattern: regexp.MustCompile(`(?i)(curl|wget)\s+.*\|\s*(ba)?sh\b`)},
		{kind: "security", desc: "download and execute script",
			pattern: regexp.MustCompile(`(?i)(curl|wget)\s+.*>\s*/tmp/.*\.\w+\s*&&\s*(ba)?sh\s+/tmp/`)},

		{kind: "security", desc: "git config writing exec-capable key (diff.external/core.pager/core.editor/core.hooksPath/core.fsmonitor/filter.*/alias.*)",
			pattern: regexp.MustCompile(`(?i)\bgit\s+config\b[^;&|\n]*\b(diff\.external|core\.(pager|editor|fsmonitor|hooksPath)|filter\.[^\s='\"]+\.(command|smudge|clean|process)|alias\.[^\s='\"]+)\s+[^\s;&|\n]`)},
		{kind: "security", desc: "git -c inline exec-capable config key (config-driven code execution, CVE-2026-112346 / adversa 2026-11 class)",
			pattern: regexp.MustCompile(`(?i)\bgit\s+(?:-[A-Za-z]+\s+)*-c\s+[\"']?(diff\.external|core\.(pager|editor|fsmonitor|hooksPath)|filter\.[^\s='\"]+\.(command|smudge|clean|process)|alias\.[^\s='\"]+)\s*=`)},

		{kind: "destructive", desc: "git reset --hard may discard uncommitted changes",
			pattern: regexp.MustCompile(`(?i)\bgit\s+reset\s+--hard\b`)},
		{kind: "destructive", desc: "git push --force may overwrite remote history",
			pattern: regexp.MustCompile(`(?i)\bgit\s+push\b[^;&|\n]*\s(--force|-f)\b`)},
		{kind: "destructive", desc: "git clean -f may permanently delete untracked files",
			pattern: regexp.MustCompile(`(?i)\bgit\s+clean\b[^;&|\n]*-[a-zA-Z]*f`)},
		{kind: "destructive", desc: "database DROP/TRUNCATE operation", quotedInert: true,
			pattern: regexp.MustCompile(`(?i)\b(DROP|TRUNCATE)\s+(TABLE|DATABASE|SCHEMA)\b`)},
		{kind: "destructive", desc: "DELETE without WHERE clause", quotedInert: true,
			pattern: regexp.MustCompile(`(?i)\bDELETE\s+FROM\s+\w+[\s;"']*($|[;\n])`)},
		{kind: "destructive", desc: "kubectl delete may remove Kubernetes resources",
			pattern: regexp.MustCompile(`(?i)\bkubectl\s+delete\b`)},
		{kind: "destructive", desc: "terraform destroy may remove infrastructure",
			pattern: regexp.MustCompile(`(?i)\bterraform\s+destroy\b`)},

		{kind: "security", desc: "chmod on system directories",
			pattern: regexp.MustCompile(`(?i)\bchmod\s+(-R\s+)?[0-7]+\s+/(etc|var|usr|System)\b`)},

		{kind: "security", desc: "recursive chown on absolute path",
			pattern: regexp.MustCompile(`(?i)\bchown\s+-R\s+\S+\s+/`)},
		{kind: "security", desc: "recursive chmod 000 on absolute path",
			pattern: regexp.MustCompile(`(?i)\bchmod\s+(-R\s+)?000\s+/`)},

		{kind: "destructive", desc: "destructive cron job",
			pattern: regexp.MustCompile(`(?i)crontab\s+.*\brm\s+-r`)},
	}

	g.cleanRules = []cleanRule{
		{desc: "remove dangerous GIT_PAGER override",

			pattern: regexp.MustCompile(`(?i)^((?:[A-Za-z_][A-Za-z0-9_]*=(?:'[^']*'|"[^"]*"|\S+)\s+)*)(?:env\s+)?GIT_PAGER=(?:'[^']*'|"[^"]*"|\S+)\s*`),

			replace: "$1",
		},
		{desc: "remove dangerous GIT_EDITOR override",

			pattern: regexp.MustCompile(`(?i)^((?:[A-Za-z_][A-Za-z0-9_]*=(?:'[^']*'|"[^"]*"|\S+)\s+)*)(?:env\s+)?GIT_EDITOR=(?:'[^']*'|"[^"]*"|\S+)\s*`),

			replace: "$1",
		},
	}

	return g
}

func (g *CommandGate) Check(cmd string) GateResult {
	result := GateResult{
		Behavior:   Allow,
		CleanedCmd: cmd,
	}

	joined := strings.ReplaceAll(strings.ReplaceAll(cmd, "\\\r\n", " "), "\\\n", " ")
	if joined != cmd {
		cmd = joined
	}

	if pre := g.preChecks(cmd); pre.Behavior != Allow {
		return pre
	}

	matchViews := gateMatchViews(cmd)
	matchCmd := strings.Join(matchViews, "\n")
	inertCmd := blankQuotedAndHeredocs(cmd)
	for _, rule := range g.blockRules {
		target := matchCmd
		if rule.quotedInert {
			target = inertCmd
		}
		for _, bp := range rule.benignPrefixes {
			target = strings.ReplaceAll(target, bp, "/benign/")
		}
		if rule.pattern.MatchString(target) {
			return GateResult{
				Behavior:   Block,
				CleanedCmd: cmd,
				Reason:     fmt.Sprintf("Command blocked: %s", rule.desc),
			}
		}
	}

	if secs := inlineSleepSeconds(blankQuotedAndHeredocs(cmd)); secs > inlineSleepBlockSeconds {
		return GateResult{
			Behavior:   Block,
			CleanedCmd: cmd,
			Reason: fmt.Sprintf(
				"Command blocked: inline sleep totals ~%d s, exceeding the %d s cap (the same abuse the sleep tool hard-caps). Long blocking waits inside a command burn the session's timeout budget. Split the work instead: start the long-running step with start_command and poll with wait_command/read_command_output, or schedule a one-shot cron for >5-minute waits; keep inline sleeps under %d s.",
				int(secs), int(inlineSleepBlockSeconds), int(inlineSleepWarnSeconds)),
		}
	}

	inertUnwrapped := blankQuotedAndHeredocs(unwrapSimpleQuotedTokens(cmd))
	var askReasons []string
	for _, rule := range g.askRules {

		if rule.quotedInert {
			if rule.pattern.MatchString(inertUnwrapped) {
				askReasons = append(askReasons, rule.desc)
			}
			continue
		}
		for _, v := range matchViews {
			if rule.pattern.MatchString(v) {
				askReasons = append(askReasons, rule.desc)
				break
			}
		}
	}

	if len(askReasons) > 0 {
		result.Behavior = Ask
		result.Reason = fmt.Sprintf("Command requires confirmation: %s", strings.Join(askReasons, "; "))
	}

	result.Warnings = g.destructiveWarnings(cmd)

	if interactive := g.InteractiveCommandWarning(cmd); interactive != "" {
		result.Warnings = append(result.Warnings, interactive)
	}

	if secs := inlineSleepSeconds(blankQuotedAndHeredocs(cmd)); secs > inlineSleepWarnSeconds {
		result.Warnings = append(result.Warnings, fmt.Sprintf(
			"Inline sleep totals ~%d s; prefer start_command + wait_command/read_command_output polling over blocking waits.",
			int(secs)))
	}

	cleaned := cmd
	for _, rule := range g.cleanRules {
		cleaned = rule.pattern.ReplaceAllString(cleaned, rule.replace)
	}
	result.CleanedCmd = strings.TrimSpace(cleaned)

	return result
}

func stripQuotedSeparators(cmd string) string {
	var b strings.Builder
	b.Grow(len(cmd))
	var quote byte
	for i := 0; i < len(cmd); i++ {
		c := cmd[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
				b.WriteByte(c)
			} else if c == ';' || c == '&' || c == '|' {

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

		probe := line
		if strings.Contains(probe, "<<<") {
			probe = strings.ReplaceAll(probe, "<<<", "   ")
		}
		if m := heredocOpenerRe.FindStringSubmatch(probe); m != nil {
			heredoc = m[1]
		}
	}
	return strings.Join(out, "\n")
}

var heredocOpenerRe = regexp.MustCompile(`<<-?\s*['\"]?([A-Za-z_][A-Za-z0-9_]*)`)

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

		case c == '"' || c == '\'':
			quote = c
			b.WriteByte(c)
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

func unwrapSimpleQuotedTokens(cmd string) string {
	lines := strings.Split(cmd, "\n")
	out := make([]string, 0, len(lines))
	heredoc := ""
	for _, line := range lines {
		if heredoc != "" {
			if strings.TrimSpace(line) == heredoc {
				heredoc = ""
			}
			out = append(out, line)
			continue
		}

		shielded := heredocOpenerRe.ReplaceAllStringFunc(line, func(m string) string {
			return strings.NewReplacer(`"`, "\x01", "'", "\x02").Replace(m)
		})
		shielded = simpleQuotedTokenRe.ReplaceAllString(shielded, "$1$2")
		out = append(out, strings.NewReplacer("\x01", `"`, "\x02", "'").Replace(shielded))
		if m := heredocOpenerRe.FindStringSubmatch(line); m != nil && len(m) > 1 {
			heredoc = m[1]
		}
	}
	return strings.Join(out, "\n")
}

var simpleQuotedTokenRe = regexp.MustCompile(`"([^"\s]+)"|'([^'\s]+)'`)

func gateMatchViews(cmd string) []string {
	views := []string{cmd}
	seen := map[string]bool{cmd: true}
	add := func(v string) {
		if v != "" && !seen[v] {
			seen[v] = true
			views = append(views, v)
		}
	}
	norm := stripQuotedSeparators(cmd)
	add(norm)
	add(unwrapSimpleQuotedTokens(cmd))
	add(unwrapSimpleQuotedTokens(norm))
	return views
}

func (g *CommandGate) preChecks(cmd string) GateResult {

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

	for _, ch := range cmd {
		if unicode.IsSpace(ch) && ch != ' ' && ch != '\t' && ch != '\n' && ch != '\r' {
			return GateResult{
				Behavior:   Ask,
				CleanedCmd: cmd,
				Reason:     "Command contains Unicode whitespace that could bypass security checks",
			}
		}
	}

	if strings.Contains(cmd, ";") {

		parts := splitSemicolons(cmd)
		for _, part := range parts {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}

			for _, rule := range g.blockRules {
				target := part
				if rule.quotedInert {

					target = blankQuotedAndHeredocs(part)
				}
				for _, bp := range rule.benignPrefixes {
					target = strings.ReplaceAll(target, bp, "/benign/")
				}
				if rule.pattern.MatchString(target) {
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

func (g *CommandGate) destructiveWarnings(cmd string) []string {

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

func (g *CommandGate) InteractiveCommandWarning(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return ""
	}

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

func splitCompoundCommandWithPipes(cmd string) (segs []string, pipeFed []bool) {
	var parts []string
	var current strings.Builder
	inSingleQuote := false
	inDoubleQuote := false
	lastSepWasPipe := false

	i := 0
	for i < len(cmd) {
		ch := cmd[i]

		if ch == '\\' && !inSingleQuote && i+1 < len(cmd) {
			current.WriteByte(ch)
			current.WriteByte(cmd[i+1])
			i += 2
			continue
		}

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

func checkInteractivePart(cmd string) string {
	cmd = strings.TrimSpace(cmd)

	fields := strings.Fields(cmd)
	if len(fields) == 0 {
		return ""
	}

	binName := filepath.Base(fields[0])
	binName = strings.ToLower(binName)

	for _, ext := range []string{".exe", ".bat", ".cmd", ".ps1"} {
		binName = strings.TrimSuffix(binName, ext)
	}

	args := fields[1:]

	if isBareREPL(binName, args) {
		return fmt.Sprintf(
			"Command %q appears to be a bare REPL invocation that will wait for stdin input indefinitely "+
				"(30-minute timeout). This will block the agent loop for the full timeout duration. "+
				"Instead: pass a script file (%s script.py), use -c/%s -e for inline code, or use start_command for interactive sessions.",
			binName, binName, binName)
	}

	if isInteractiveApp(binName) {

		if hasNonInteractiveOverride(binName, args) {
			return ""
		}
		suggestion := interactiveAppSuggestion(binName)
		return fmt.Sprintf(
			"Command %q is an interactive application that will wait for keyboard input indefinitely "+
				"(30-minute timeout). %s Alternatively, use start_command for interactive sessions.",
			binName, suggestion)
	}

	if isInfiniteCommand(binName, args) {
		suggestion := infiniteCommandSuggestion(binName)
		return fmt.Sprintf(
			"Command %q runs indefinitely and will not exit on its own (30-minute timeout). %s "+
				"Use start_command for long-running processes, or add appropriate arguments to make it exit.",
			binName, suggestion)
	}

	return ""
}

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

	nonInteractiveFlags := map[string]bool{
		"-c": true, "--command": true,
		"-e": true, "--eval": true,
		"-m": true,
	}

	exitFlags := map[string]bool{
		"-V": true, "-v": true, "--version": true,
		"-h": true, "--help": true,
		"--check": true, "-?": true,
	}

	for _, a := range args {
		if nonInteractiveFlags[a] {
			return false
		}
		if exitFlags[a] {
			return false
		}

		if !strings.HasPrefix(a, "-") {
			return false
		}
	}

	if bin == "sqlite3" || bin == "mysql" || bin == "psql" || bin == "redis-cli" {

		return true
	}

	return true
}

func isInteractiveApp(bin string) bool {
	apps := map[string]bool{

		"vim": true, "vi": true, "nvim": true, "nano": true, "emacs": true,
		"pico": true, "micro": true, "helix": true, "hx": true,
		"ed": true, "ex": true,

		"less": true, "more": true, "most": true,

		"top": true, "htop": true, "btop": true, "iotop": true, "atop": true,
		"glances": true,

		"bash": true, "zsh": true, "sh": true, "fish": true, "csh": true,
		"tcsh": true, "pwsh": true, "powershell": true,
	}
	return apps[bin]
}

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

func hasNonInteractiveOverride(bin string, args []string) bool {
	overrides, ok := nonInteractiveOverrides[bin]
	if !ok {
		return false
	}
	for _, a := range args {
		if overrides[a] {
			return true
		}

		if bin == "top" && strings.HasPrefix(a, "-n") {
			return true
		}
	}
	return false
}

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

func isInfiniteCommand(bin string, args []string) bool {
	checker, ok := infiniteCmdCheckers[bin]
	if !ok {
		return false
	}
	return checker(args)
}

func isInfiniteTail(args []string) bool {
	for _, a := range args {
		if a == "-f" || a == "--follow" || strings.HasPrefix(a, "-F") {
			return true
		}

		if strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.Contains(a, "f") {
			return true
		}
	}
	return false
}

func isBareStdinReader(args []string) bool {
	return len(args) == 0
}

func isInfiniteNetcat(args []string) bool {
	for _, a := range args {
		if a == "-l" || a == "--listen" {
			return true
		}
	}
	return false
}

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

func (g *CommandGate) IsDestructive(cmd string) bool {
	views := gateMatchViews(cmd)
	for _, rule := range g.blockRules {
		for _, v := range views {
			if rule.pattern.MatchString(v) {
				return true
			}
		}
	}
	for _, rule := range g.askRules {
		for _, v := range views {
			if rule.pattern.MatchString(v) {
				return true
			}
		}
	}
	return false
}

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
