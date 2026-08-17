package permission

import (
	"fmt"
	"regexp"
	"strings"
)

// DangerLevel indicates how dangerous a command is.
type DangerLevel int

const (
	DangerNone DangerLevel = iota
	DangerLow
	DangerMedium
	DangerHigh
	DangerCritical
)

func (l DangerLevel) String() string {
	switch l {
	case DangerNone:
		return "none"
	case DangerLow:
		return "low"
	case DangerMedium:
		return "medium"
	case DangerHigh:
		return "high"
	case DangerCritical:
		return "critical"
	default:
		return "unknown"
	}
}

// DangerousCheck represents a single danger pattern match.
type DangerousCheck struct {
	Level   DangerLevel
	Pattern string
	Reason  string
}

// DangerousDetector detects dangerous shell commands.
type DangerousDetector struct {
	patterns []dangerPattern
}

type dangerPattern struct {
	level  DangerLevel
	re     *regexp.Regexp
	reason string
}

// rmCmdAnchor matches positions where "rm" begins a command: at the start of
// the command line, after a shell separator (; | & newline ( ` " '), or after
// a common command wrapper (sudo/doas/xargs/nohup/env/nice). It replaces \b
// word-boundary matching for the workflow-level (Medium) rm patterns, which
// false-positived on subcommand words like "git rm -f" — a routine daily
// workflow hard-Denied in auto mode (#573-E) — while still catching chained
// forms like "make && rm -rf build" and long options (--force/--recursive).
// The Critical rm-root patterns stay \b-anchored anywhere in the command:
// deleting / is never a legitimate argument position, and canaries like
// `echo 'rm -rf /'` must keep matching.
const rmCmdAnchor = `(?:^|[|;&\n` + "`" + `("']\s*|(?:sudo|doas|xargs|nohup|env|nice)\s+)`

// NewDangerousDetector creates a detector with default dangerous patterns.
func NewDangerousDetector() *DangerousDetector {
	d := &DangerousDetector{}
	d.patterns = []dangerPattern{
		// Critical: destructive commands (Unix)
		{DangerCritical, regexp.MustCompile(`(?i)\brm\s+(?:-[a-zA-Z]*f[a-zA-Z]*\s+)?/\s*$`), "rm -rf / would delete the entire filesystem"},
		{DangerCritical, regexp.MustCompile(`(?i)\brm\s+(?:-[a-zA-Z]*f[a-zA-Z]*\s+)?/\*`), "rm -rf /* would delete the entire filesystem"},
		{DangerCritical, regexp.MustCompile(`(?i)\bmkfs\b`), "mkfs would format a disk"},
		{DangerCritical, regexp.MustCompile(`(?i)\bdd\s+.*\bif=/dev/`), "dd with device input could destroy data"},
		{DangerCritical, regexp.MustCompile(`(?i)\bshred\b`), "shred securely deletes files"},
		{DangerCritical, regexp.MustCompile(`(?i)\bchmod\s+(-[a-zA-Z]*R[a-zA-Z]*\s+)?777\s+/\s*$`), "chmod 777 / is dangerous"},
		{DangerCritical, regexp.MustCompile(`(?i):\(\)\s*\{\s*:\|:\s*&\s*\}\s*;:`), "fork bomb detected"},

		// Critical: destructive commands (PowerShell / Windows)
		{DangerCritical, regexp.MustCompile(`(?i)Remove-Item.*-Recurse.*-Force.*[A-Z]:\\?\s*$`), "Remove-Item -Recurse -Force on drive root would delete everything"},
		{DangerCritical, regexp.MustCompile(`(?i)Remove-Item.*-Recurse.*-Force.*\\Windows`), "removing Windows system directory"},
		{DangerCritical, regexp.MustCompile(`(?i)Format-Volume`), "Format-Volume would format a disk"},
		{DangerCritical, regexp.MustCompile(`(?i)Clear-Disk`), "Clear-Disk would wipe a disk"},
		{DangerCritical, regexp.MustCompile(`(?i)Set-Content.*PhysicalDrive`), "writing directly to a physical disk device"},
		{DangerCritical, regexp.MustCompile(`(?i)while\s*\(\s*\$true\s*\)\s*\{?\s*Start-Process`), "PowerShell fork bomb pattern"},

		// High: privilege escalation, system-wide changes (Unix)
		{DangerHigh, regexp.MustCompile(`(?i)\bsudo\s+rm\b`), "sudo rm is destructive with elevated privileges"},
		{DangerHigh, regexp.MustCompile(`(?i)\bsudo\s+mkfs\b`), "sudo mkfs would format a disk"},
		{DangerHigh, regexp.MustCompile(`(?i)\bsudo\s+dd\b`), "sudo dd with elevated privileges"},
		{DangerHigh, regexp.MustCompile(`(?i)\bmv\s+.*\s+/\s*$`), "moving files to root could break the system"},
		{DangerHigh, regexp.MustCompile(`(?i)\bkill\s+(-9\s+)?-1\b`), "kill -1 sends signal to all processes"},
		{DangerHigh, regexp.MustCompile(`(?i)\bpkill\s+(-9\s+)?-u\s+root\b`), "killing root processes"},
		{DangerHigh, regexp.MustCompile(`(?i)\bsystemctl\s+(stop|disable|mask)\b`), "stopping/disabling system services"},
		{DangerHigh, regexp.MustCompile(`(?i)\biptables\s+-F\b`), "flushing all firewall rules"},
		{DangerHigh, regexp.MustCompile(`(?i)\buserdel\b`), "deleting a user account"},
		{DangerHigh, regexp.MustCompile(`(?i)\bpasswd\b.*\broot\b`), "changing root password"},

		// High: privilege escalation (PowerShell / Windows)
		{DangerHigh, regexp.MustCompile(`(?i)Start-Process.*-Verb\s+RunAs`), "Start-Process -Verb RunAs elevates privileges"},
		{DangerHigh, regexp.MustCompile(`(?i)\bnet\s+(user|localgroup)\s+.*/add\b`), "creating new user or adding to group"},
		{DangerHigh, regexp.MustCompile(`(?i)Set-ExecutionPolicy`), "changing PowerShell execution policy"},
		{DangerHigh, regexp.MustCompile(`(?i)Disable-WindowsOptionalFeature`), "disabling Windows features"},
		{DangerHigh, regexp.MustCompile(`(?i)Stop-Service.*-Force`), "force-stopping Windows services"},
		{DangerHigh, regexp.MustCompile(`(?i)Set-ItemProperty.*HKLM:`), "modifying registry machine settings"},

		// Medium: potentially destructive (Unix)
		{DangerMedium, regexp.MustCompile(`(?i)` + rmCmdAnchor + `rm\s+(?:-[a-zA-Z]*r[a-zA-Z]*(?:\s|$)|--recursive(?:\s|$)).*\*`), "recursive rm with wildcard"},
		{DangerMedium, regexp.MustCompile(`(?i)` + rmCmdAnchor + `rm\s+(?:-[a-zA-Z]*f[a-zA-Z]*(?:\s|$)|--force(?:\s|$))`), "force rm without confirmation"},
		{DangerMedium, regexp.MustCompile(`(?i)\bsudo\b`), "running command with elevated privileges"},
		{DangerMedium, regexp.MustCompile(`(?i)\bcurl\b.*\|\s*bash\b`), "piping remote script to bash"},
		{DangerMedium, regexp.MustCompile(`(?i)\bwget\b.*\|\s*sh\b`), "piping remote script to shell"},
		{DangerMedium, regexp.MustCompile(`(?i)\bnc\b.*-e\b`), "netcat in listen mode could be a reverse shell"},
		{DangerMedium, regexp.MustCompile(`(?i)\b>\s*/dev/sd[a-z]`), "writing directly to a disk device"},
		{DangerMedium, regexp.MustCompile(`(?i)\bcrontab\b`), "modifying cron jobs"},
		{DangerMedium, regexp.MustCompile(`(?i)\bnsenter\b`), "nsenter can escape containers"},
		{DangerMedium, regexp.MustCompile(`(?i)\bchroot\b`), "chroot changes the root directory"},

		// Medium: potentially destructive (PowerShell / Windows)
		{DangerMedium, regexp.MustCompile(`(?i)Invoke-WebRequest.*Invoke-Expression`), "piping remote content to Invoke-Expression (like curl|bash)"},
		{DangerMedium, regexp.MustCompile(`(?i)iwr.*\|.*iex`), "piping remote content to iex (alias shorthand)"},
		{DangerMedium, regexp.MustCompile(`(?i)Invoke-Expression.*Invoke-WebRequest`), "executing remote content via Invoke-Expression"},
		{DangerMedium, regexp.MustCompile(`(?i)Remove-Item.*-Recurse.*-Force`), "recursive forced deletion"},
		{DangerMedium, regexp.MustCompile(`(?i)schtasks\s+/create`), "creating scheduled tasks"},
		{DangerMedium, regexp.MustCompile(`(?i)reg\s+delete.*HKLM`), "deleting registry machine keys"},

		// Low: worth noting
		{DangerLow, regexp.MustCompile(`(?i)\bchmod\s+777\b`), "setting world-writable permissions"},
		{DangerLow, regexp.MustCompile(`(?i)\bfind\b.*-delete\b`), "find with -delete"},
		{DangerLow, regexp.MustCompile(`(?i)\bmv\b.*\*.*\b/dev/null\b`), "moving files to /dev/null"},

		// High: destructive git operations that can permanently destroy work.
		// AI coding agents frequently run these without understanding consequences.
		// Patterns use (?:\s|$) after --force to exclude --force-with-lease.
		{DangerHigh, regexp.MustCompile(`(?i)\bgit\s+push\b.*--force(?:\s|$)`), "git push --force can overwrite remote history irrevocably"},
		{DangerHigh, regexp.MustCompile(`(?i)\bgit\s+push\b.*\s-f(?:\s|$)`), "git push -f can overwrite remote history irrevocably"},
		{DangerHigh, regexp.MustCompile(`(?i)\bgit\s+push\b.*--mirror\b`), "git push --mirror can force-delete remote branches"},
		{DangerHigh, regexp.MustCompile(`(?i)\bgit\s+push\b.*--delete\b`), "git push --delete removes a remote branch"},
		{DangerHigh, regexp.MustCompile(`(?i)\bgit\s+reset\s+--hard\b`), "git reset --hard discards ALL uncommitted changes permanently"},
		{DangerHigh, regexp.MustCompile(`(?i)\bgit\s+clean\s+-[a-z]*f`), "git clean -f permanently deletes untracked files"},
		{DangerHigh, regexp.MustCompile(`(?i)\bgit\s+checkout\s+--\s+\.`), "git checkout -- . discards ALL working tree changes"},
		{DangerHigh, regexp.MustCompile(`(?i)\bgit\s+checkout\s+\.\s*$`), "git checkout . discards ALL working tree changes"},
		// #551-E: `git checkout <ref> -- <paths>` (e.g. `git checkout HEAD -- .`
		// or `git checkout main -- src/`) overwrites working tree files with the
		// ref's version — same data loss as the bare forms above, but none of the
		// existing patterns matched it (they require `--` or `.` directly after
		// `checkout`). \S+ captures the branch/commit/tag; requiring a non-space
		// path token after `--` keeps `git checkout -b feat` and plain
		// `git checkout main` (switch-only, no discard) unmatched.
		{DangerHigh, regexp.MustCompile(`(?i)\bgit\s+checkout\s+\S+\s+--\s+\S`), "git checkout <ref> -- <paths> discards working tree changes for those paths"},
		{DangerHigh, regexp.MustCompile(`(?i)\bgit\s+restore\s+--staged\s+--worktree\s+\.`), "git restore --staged --worktree . discards ALL changes"},
		{DangerHigh, regexp.MustCompile(`(?i)\bgit\s+restore\s+--worktree\s+\.`), "git restore --worktree . discards ALL working tree changes"},

		// Medium: potentially destructive git operations.
		// Note: git branch -D uses case-sensitive D (not (?i)) to distinguish
		// from -d which only deletes fully merged branches.
		{DangerMedium, regexp.MustCompile(`git\s+branch\s+-[a-z]*D[a-z]*`), "git branch -D force-deletes a branch (loses unmerged work)"},
		{DangerMedium, regexp.MustCompile(`(?i)\bgit\s+tag\s+-[a-z]*d[a-z]*\s+.*\*`), "git tag -d with wildcard deletes multiple tags"},
		{DangerMedium, regexp.MustCompile(`(?i)\bgit\s+stash\s+clear\b`), "git stash clear permanently removes ALL stashed changes"},
		{DangerMedium, regexp.MustCompile(`(?i)\bgit\s+stash\s+drop\b`), "git stash drop permanently removes a stash entry"},
		{DangerMedium, regexp.MustCompile(`(?i)\bgit\s+push\b.*--force-with-lease\b`), "git push --force-with-lease can overwrite remote (safer but still risky)"},
		{DangerMedium, regexp.MustCompile(`(?i)\bgit\s+rebase\s+--abort\b`), "git rebase --abort discards in-progress rebase work"},
		{DangerMedium, regexp.MustCompile(`(?i)\bgit\s+merge\s+--abort\b`), "git merge --abort discards in-progress merge work"},
		{DangerMedium, regexp.MustCompile(`(?i)\bgit\s+restore\s+--staged\s+\.\s*$`), "git restore --staged . unstages ALL staged changes"},

		// Critical: AppleScript destructive commands
		{DangerCritical, regexp.MustCompile(`(?i)do\s+shell\s+script\s+["\'].*rm\s+-rf\s+/["\']`), "AppleScript do shell script with rm -rf /"},
		{DangerCritical, regexp.MustCompile(`(?i)do\s+shell\s+script\s+["\'].*rm\s+-rf\s+/\*["\']`), "AppleScript do shell script with rm -rf /*"},
		{DangerCritical, regexp.MustCompile(`(?i)do\s+shell\s+script\s+["\'].*mkfs\b`), "AppleScript do shell script with mkfs"},
		{DangerCritical, regexp.MustCompile(`(?i)do\s+shell\s+script\s+["\'].*dd\s+if=/dev/`), "AppleScript do shell script with dd device input"},
		{DangerCritical, regexp.MustCompile(`(?i)do\s+shell\s+script\s+["\'].*chmod\s+-R\s+777\s+/["\']`), "AppleScript do shell script with chmod 777 /"},

		// High: AppleScript privilege escalation and sensitive access
		{DangerHigh, regexp.MustCompile(`(?i)do\s+shell\s+script\s+["\'].*sudo\b`), "AppleScript do shell script with sudo"},
		{DangerHigh, regexp.MustCompile(`(?i)security\s+find-generic-password`), "AppleScript accessing Keychain passwords"},
		{DangerHigh, regexp.MustCompile(`(?i)security\s+delete-generic-password`), "AppleScript deleting Keychain passwords"},
		{DangerHigh, regexp.MustCompile(`(?i)do\s+shell\s+script\s+["\'].*curl\b.*\|\s*bash`), "AppleScript piping curl to bash"},
		{DangerHigh, regexp.MustCompile(`(?i)do\s+shell\s+script\s+["\'].*wget\b.*\|\s*sh`), "AppleScript piping wget to shell"},
		{DangerHigh, regexp.MustCompile(`(?i)do\s+shell\s+script\s+["\'].*nc\b.*-e`), "AppleScript netcat with -e (reverse shell)"},

		// High: output redirection to sensitive system paths.
		// AI agents can be tricked via prompt injection into writing malicious
		// content to SSH keys, shell startup files, cron jobs, or system
		// binaries. These paths allow persistent backdoors.
		{DangerHigh, regexp.MustCompile(`(?i)>\s*\S*\.ssh/`), "writing to SSH directory could install unauthorized keys"},
		{DangerHigh, regexp.MustCompile(`(?i)>\s*/etc/cron`), "writing to cron directory could install persistent scheduled tasks"},
		{DangerHigh, regexp.MustCompile(`(?i)>\s*/etc/passwd`), "writing to passwd file could add unauthorized users"},
		{DangerHigh, regexp.MustCompile(`(?i)>\s*/etc/shadow`), "writing to shadow file could modify password hashes"},
		{DangerHigh, regexp.MustCompile(`(?i)>\s*/etc/sudoers`), "writing to sudoers could grant unauthorized root access"},
		{DangerHigh, regexp.MustCompile(`(?i)>\s*/usr/local/bin/`), "writing to system binary directory could install trojans"},
		{DangerHigh, regexp.MustCompile(`(?i)>\s*/usr/bin/`), "writing to system binary directory could install trojans"},

		// Medium: output redirection to shell startup files and system config.
		// These allow code execution on next login/shell start.
		{DangerMedium, regexp.MustCompile(`(?i)>\s*~/\.bashrc`), "writing to .bashrc could execute malicious code on shell startup"},
		{DangerMedium, regexp.MustCompile(`(?i)>\s*~/\.zshrc`), "writing to .zshrc could execute malicious code on shell startup"},
		{DangerMedium, regexp.MustCompile(`(?i)>\s*~/\.profile`), "writing to .profile could execute malicious code on login"},
		{DangerMedium, regexp.MustCompile(`(?i)>\s*/etc/hosts`), "writing to hosts file could hijack DNS resolution"},
		{DangerMedium, regexp.MustCompile(`(?i)>\s*/etc/environment`), "writing to environment config could inject malicious variables"},

		// Medium: AppleScript potentially destructive operations
		{DangerMedium, regexp.MustCompile(`(?i)do\s+shell\s+script.*rm\s+-rf`), "AppleScript do shell script with rm -rf"},
		{DangerMedium, regexp.MustCompile(`(?i)do\s+shell\s+script.*rm\s+-rf\s+\*`), "AppleScript do shell script with rm -rf *"},
		{DangerMedium, regexp.MustCompile(`(?i)do\s+shell\s+script.*>\s*/dev/sd[a-z]`), "AppleScript writing to disk device"},
	}
	return d
}

// IsDangerous returns true if the command matches any dangerous pattern.
func (d *DangerousDetector) IsDangerous(command string) bool {
	return d.Check(command).Level >= DangerMedium
}

// Check returns the most severe danger match for the command.
func (d *DangerousDetector) Check(command string) DangerousCheck {
	// Trim leading/trailing whitespace
	cmd := strings.TrimSpace(command)

	var worst DangerousCheck
	worst.Level = DangerNone

	for _, p := range d.patterns {
		if p.re.MatchString(cmd) {
			if p.level > worst.Level {
				worst = DangerousCheck{
					Level:   p.level,
					Pattern: p.re.String(),
					Reason:  p.reason,
				}
			}
		}
	}

	return worst
}

// IsExtremelyDangerous returns true if the command matches critical-level patterns.
// Used by BypassMode to decide which operations still need confirmation.
func (d *DangerousDetector) IsExtremelyDangerous(command string) bool {
	check := d.Check(command)
	return check.Level >= DangerCritical
}

// Suggestion returns a human-readable suggestion for the danger check.
func (c DangerousCheck) Suggestion() string {
	if c.Level == DangerNone {
		return "This command appears safe."
	}
	return fmt.Sprintf("[%s] %s", strings.ToUpper(c.Level.String()), c.Reason)
}
