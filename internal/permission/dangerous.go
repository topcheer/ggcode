package permission

import (
	"fmt"
	"regexp"
	"strings"
)

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

type DangerousCheck struct {
	Level   DangerLevel
	Pattern string
	Reason  string
}

type DangerousDetector struct {
	patterns []dangerPattern
}

type dangerPattern struct {
	level  DangerLevel
	re     *regexp.Regexp
	reason string
}

// #5474: wrapper words may carry their own flags before reaching rm -
// valueless (`xargs -0`), inline-valued (`sudo --preserve-env=PATH`),
// numeric (`nice -n 5`) and KEY=VALUE env assignments (`env VAR=1 nice`).
// The old anchor required exactly one bare whitespace after the wrapper, so
// those shapes fell outside the anchor and their trailing `rm -rf` went
// unanchored (agent-layer parity fix was #5456/#5460).
const rmCmdAnchor = `(?:^|[|;&\n` + "`" + `("']\s*|(?:sudo|doas|xargs|nohup|env|nice)(?:\s+(?:-{1,2}[A-Za-z][\w-]*(?:=[^\s;|&` + "`" + `]*|"(?:[^"\\]|\\.)*"|'(?:[^'\\]|\\.)*')?|[A-Za-z_][A-Za-z0-9_]*=[^\s;|&` + "`" + `]*|-\d|\d+))*\s+)`

func NewDangerousDetector() *DangerousDetector {
	d := &DangerousDetector{}
	d.patterns = []dangerPattern{

		{DangerCritical, regexp.MustCompile(`(?i)\brm\s+(?:(?:-{1,2}[a-zA-Z][a-zA-Z-]*|-[a-zA-Z]+)\s+)*(?:-[a-zA-Z]*f[a-zA-Z]*\s+)?/\s*$`), "rm -rf / would delete the entire filesystem"},

		{DangerCritical, regexp.MustCompile(`(?i)\brm\s+(?:(?:-{1,2}[a-zA-Z][a-zA-Z-]*|-[a-zA-Z]+)\s+)*(?:-[a-zA-Z]*f[a-zA-Z]*\s+)?["']/["']{1,2}\s*$`), "rm -rf quoted-root would delete the entire filesystem"},

		{DangerCritical, regexp.MustCompile(`(?i)\brm\s+(?:(?:-{1,2}[a-zA-Z][a-zA-Z-]*|-[a-zA-Z]+)\s+)*(?:-[a-zA-Z]*f[a-zA-Z]*\s+)?["']?\$HOME["']{0,2}(/.*)?\s*$`), "rm -rf $HOME would delete the user's entire home directory"},

		{DangerCritical, regexp.MustCompile(`(?i)\brm\s+(?:(?:-{1,2}[a-zA-Z][a-zA-Z-]*|-[a-zA-Z]+)\s+)*(?:-[a-zA-Z]*r[a-zA-Z]*\s+)?["']?~["']{0,2}(/.*)?\s*$`), "rm -r ~ would delete the user's entire home directory"},
		{DangerCritical, regexp.MustCompile(`(?i)\brm\s+(?:(?:-{1,2}[a-zA-Z][a-zA-Z-]*|-[a-zA-Z]+)\s+)*(?:-[a-zA-Z]*f[a-zA-Z]*\s+)?/\*`), "rm -rf /* would delete the entire filesystem"},

		{DangerCritical, regexp.MustCompile(`(?i)\brm\b[^|;&\n]*--no-preserve-root`), "rm --no-preserve-root can delete the root filesystem"},
		{DangerCritical, regexp.MustCompile(`(?i)\bmkfs\b`), "mkfs would format a disk"},

		{DangerHigh, regexp.MustCompile(`(?i)\bdd\s+.*\bif=/dev/(sd|hd|nvme|vd|r?disk|mmcblk|mapper|loop|dm-|xvd)`), "dd reading from a raw device is unusual - verify the operands"},

		{DangerCritical, regexp.MustCompile(`(?i)\bdd\b[^;|&]*\bof=/dev/(sd|hd|nvme|vd|r?disk|mmcblk|mapper|loop|dm-|xvd)`), "dd writing to a raw device would destroy it"},
		{DangerCritical, regexp.MustCompile(`(?i)\bshred\b`), "shred securely deletes files"},
		{DangerCritical, regexp.MustCompile(`(?i)\bchmod\s+(-[a-zA-Z]*R[a-zA-Z]*\s+)?777\s+/\s*$`), "chmod 777 / is dangerous"},
		{DangerCritical, regexp.MustCompile(`(?i):\(\)\s*\{\s*:\|:\s*&\s*\}\s*;:`), "fork bomb detected"},

		{DangerCritical, regexp.MustCompile(`(?i)Remove-Item.*-Recurse.*-Force.*[A-Z]:\\?\s*$`), "Remove-Item -Recurse -Force on drive root would delete everything"},
		{DangerCritical, regexp.MustCompile(`(?i)Remove-Item.*-Recurse.*-Force.*\\Windows`), "removing Windows system directory"},
		{DangerCritical, regexp.MustCompile(`(?i)Format-Volume`), "Format-Volume would format a disk"},
		{DangerCritical, regexp.MustCompile(`(?i)Clear-Disk`), "Clear-Disk would wipe a disk"},
		{DangerCritical, regexp.MustCompile(`(?i)Set-Content.*PhysicalDrive`), "writing directly to a physical disk device"},
		{DangerCritical, regexp.MustCompile(`(?i)while\s*\(\s*\$true\s*\)\s*\{?\s*Start-Process`), "PowerShell fork bomb pattern"},

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

		{DangerHigh, regexp.MustCompile(`(?i)Start-Process.*-Verb\s+RunAs`), "Start-Process -Verb RunAs elevates privileges"},
		{DangerHigh, regexp.MustCompile(`(?i)\bnet\s+(user|localgroup)\s+.*/add\b`), "creating new user or adding to group"},
		{DangerHigh, regexp.MustCompile(`(?i)Set-ExecutionPolicy`), "changing PowerShell execution policy"},
		{DangerHigh, regexp.MustCompile(`(?i)Disable-WindowsOptionalFeature`), "disabling Windows features"},
		{DangerHigh, regexp.MustCompile(`(?i)Stop-Service.*-Force`), "force-stopping Windows services"},
		{DangerHigh, regexp.MustCompile(`(?i)Set-ItemProperty.*HKLM:`), "modifying registry machine settings"},

		{DangerMedium, regexp.MustCompile(`(?i)` + rmCmdAnchor + `rm\s+(?:-[a-zA-Z]*r[a-zA-Z]*(?:\s|$)|--recursive(?:\s|$)).*\*`), "recursive rm with wildcard"},
		{DangerMedium, regexp.MustCompile(`(?i)` + rmCmdAnchor + `rm\s+(?:-[a-zA-Z]*f[a-zA-Z]*(?:\s|$)|--force(?:\s|$))`), "force rm without confirmation"},
		{DangerMedium, regexp.MustCompile(`(?i)\bsudo\b`), "running command with elevated privileges"},
		{DangerMedium, regexp.MustCompile(`(?i)\bcurl\b.*\|\s*bash\b`), "piping remote script to bash"},
		{DangerMedium, regexp.MustCompile(`(?i)\bwget\b.*\|\s*sh\b`), "piping remote script to shell"},
		{DangerMedium, regexp.MustCompile(`(?i)\bnc\b.*-e\b`), "netcat in listen mode could be a reverse shell"},

		// CVE-2026-2256-class bypasses (round-24/26 NEW_GAP): single-entry
		// deny-list evasion via decode pipes, remote-content eval, allowed
		// interpreters running arbitrary logic, and staged download-execute
		// chains. Each alone looks benign; the combinations are the attack.
		{DangerHigh, regexp.MustCompile(`(?i)\b(?:base64\s+(?:-d|--decode)|openssl\s+enc\b[^|;\n]*\s-d|xxd\s+(?:-r|--revert))\s*[^|;\n]*\|\s*(?:ba|z|da)?sh\b`), "decode output piped straight into a shell - obfuscated command execution"},
		{DangerHigh, regexp.MustCompile(`(?i)\beval\s+["']?\$\(\s*(?:curl|wget)\b`), "fetching remote content straight into eval executes attacker-controlled code"},
		{DangerHigh, regexp.MustCompile(`(?i)(?:\bsource\b|(?:^|[\s;&|])\.)\s+<\(\s*(?:curl|wget)\b`), "fetching remote content via process substitution into source executes attacker-controlled code"},
		{DangerHigh, regexp.MustCompile(`(?i)\bpython3?\s+(?:-\w+\s+)*-c\b[^|\n]{0,400}\b(?:os\.system|subprocess\.|pty\.spawn|socket\.socket)`), "interpreter -c embedding OS/subprocess/socket calls bypasses command deny-lists"},
		{DangerHigh, regexp.MustCompile(`(?i)\bperl\s+(?:-\w+\s+)*-e\b[^|\n]{0,400}\b(?:system\s*\(|exec\s*\(|open\s*\([^)]*\|)`), "perl -e with system/exec/pipe-open runs arbitrary logic outside the deny-list"},
		{DangerHigh, regexp.MustCompile(`(?i)\bnode\s+(?:-\w+\s+)*-e\b[^|\n]{0,400}\b(?:child_process|execSync|spawnSync)`), "node -e spawning child processes bypasses command deny-lists"},
		{DangerHigh, regexp.MustCompile(`(?i)\bruby\s+(?:-\w+\s+)*-e\b[^|\n]{0,400}\b(?:system\s*\(|IO\.popen|Open3\.)`), "ruby -e with system/popen runs arbitrary logic outside the deny-list"},
		{DangerHigh, regexp.MustCompile(`(?i)\b(?:ba|z|da)?sh\s+-c\b[^|;\n]{0,200}(?:curl|wget)\b[^|;\n]{0,200}\|\s*(?:ba|z|da)?sh\b`), "sh -c wrapping a remote fetch piped back into a shell"},
		{DangerMedium, regexp.MustCompile(`(?i)\b(?:curl|wget)\b[^&;\n]{0,200}(?:&&|;)\s*chmod\s+(?:\+x|777)[^&;\n]{0,200}(?:&&|;)\s*\.{0,2}/?[A-Za-z0-9_./-]+`), "download -> chmod executable -> run chain stages attacker-controlled code"},

		{DangerCritical, regexp.MustCompile(`(?i)>{1,2}\s*/dev/(sd|hd|nvme|vd|r?disk|mmcblk|mapper|loop|dm-|xvd)`), "writing directly to a disk device would destroy it"},
		{DangerMedium, regexp.MustCompile(`(?i)\bcrontab\b`), "modifying cron jobs"},
		{DangerMedium, regexp.MustCompile(`(?i)\bnsenter\b`), "nsenter can escape containers"},
		{DangerMedium, regexp.MustCompile(`(?i)\bchroot\b`), "chroot changes the root directory"},

		{DangerMedium, regexp.MustCompile(`(?i)Invoke-WebRequest.*Invoke-Expression`), "piping remote content to Invoke-Expression (like curl|bash)"},
		{DangerMedium, regexp.MustCompile(`(?i)iwr.*\|.*iex`), "piping remote content to iex (alias shorthand)"},
		{DangerMedium, regexp.MustCompile(`(?i)Invoke-Expression.*Invoke-WebRequest`), "executing remote content via Invoke-Expression"},
		{DangerMedium, regexp.MustCompile(`(?i)Remove-Item.*-Recurse.*-Force`), "recursive forced deletion"},
		{DangerMedium, regexp.MustCompile(`(?i)schtasks\s+/create`), "creating scheduled tasks"},
		{DangerMedium, regexp.MustCompile(`(?i)reg\s+delete.*HKLM`), "deleting registry machine keys"},

		{DangerLow, regexp.MustCompile(`(?i)\bchmod\s+777\b`), "setting world-writable permissions"},
		{DangerLow, regexp.MustCompile(`(?i)\bfind\b.*-delete\b`), "find with -delete"},
		{DangerLow, regexp.MustCompile(`(?i)\bmv\b.*\*.*\b/dev/null\b`), "moving files to /dev/null"},

		{DangerHigh, regexp.MustCompile(`(?i)\bgit\s+push\b.*--force(?:\s|$)`), "git push --force can overwrite remote history irrevocably"},
		{DangerHigh, regexp.MustCompile(`(?i)\bgit\s+push\b.*\s-f(?:\s|$)`), "git push -f can overwrite remote history irrevocably"},

		{DangerHigh, regexp.MustCompile(`(?i)\bgit\s+push\b[^;|&]*\s\+\S+:\S+`), "git push +refspec forces the push (overwrites remote history)"},
		{DangerHigh, regexp.MustCompile(`(?i)\bgit\s+push\b.*--force=`), "git push --force=<val> forces the push"},
		{DangerHigh, regexp.MustCompile(`(?i)\bgit\s+(?:checkout|switch)\s+(?:-[a-zA-Z]*\s+)*-[a-zA-Z]*f[a-zA-Z]*\s+\S`), "git checkout/switch -f discards local changes"},
		{DangerHigh, regexp.MustCompile(`(?i)\bgit\s+push\b.*--mirror\b`), "git push --mirror can force-delete remote branches"},
		{DangerHigh, regexp.MustCompile(`(?i)\bgit\s+push\b.*--delete\b`), "git push --delete removes a remote branch"},
		{DangerHigh, regexp.MustCompile(`(?i)\bgit\s+reset\s+--hard\b`), "git reset --hard discards ALL uncommitted changes permanently"},
		{DangerHigh, regexp.MustCompile(`(?i)\bgit\s+clean\s+-[a-z]*f`), "git clean -f permanently deletes untracked files"},
		{DangerHigh, regexp.MustCompile(`(?i)\bgit\s+checkout\s+--\s+\.`), "git checkout -- . discards ALL working tree changes"},
		{DangerHigh, regexp.MustCompile(`(?i)\bgit\s+checkout\s+\.\s*$`), "git checkout . discards ALL working tree changes"},

		{DangerHigh, regexp.MustCompile(`(?i)\bgit\s+checkout\s+\S+\s+--\s+\S`), "git checkout <ref> -- <paths> discards working tree changes for those paths"},
		{DangerHigh, regexp.MustCompile(`(?i)\bgit\s+restore\s+--staged\s+--worktree\s+\.`), "git restore --staged --worktree . discards ALL changes"},
		{DangerHigh, regexp.MustCompile(`(?i)\bgit\s+restore\s+--worktree\s+\.`), "git restore --worktree . discards ALL working tree changes"},

		{DangerMedium, regexp.MustCompile(`git\s+branch\s+-[a-z]*D[a-z]*`), "git branch -D force-deletes a branch (loses unmerged work)"},
		{DangerMedium, regexp.MustCompile(`(?i)\bgit\s+tag\s+-[a-z]*d[a-z]*\s+.*\*`), "git tag -d with wildcard deletes multiple tags"},
		{DangerMedium, regexp.MustCompile(`(?i)\bgit\s+stash\s+clear\b`), "git stash clear permanently removes ALL stashed changes"},
		{DangerMedium, regexp.MustCompile(`(?i)\bgit\s+stash\s+drop\b`), "git stash drop permanently removes a stash entry"},
		{DangerMedium, regexp.MustCompile(`(?i)\bgit\s+push\b.*--force-with-lease\b`), "git push --force-with-lease can overwrite remote (safer but still risky)"},
		{DangerMedium, regexp.MustCompile(`(?i)\bgit\s+rebase\s+--abort\b`), "git rebase --abort discards in-progress rebase work"},
		{DangerMedium, regexp.MustCompile(`(?i)\bgit\s+merge\s+--abort\b`), "git merge --abort discards in-progress merge work"},
		{DangerMedium, regexp.MustCompile(`(?i)\bgit\s+restore\s+--staged\s+\.\s*$`), "git restore --staged . unstages ALL staged changes"},

		{DangerCritical, regexp.MustCompile(`(?i)do\s+shell\s+script\s+["\'].*rm\s+-rf\s+/["\']`), "AppleScript do shell script with rm -rf /"},
		{DangerCritical, regexp.MustCompile(`(?i)do\s+shell\s+script\s+["\'].*rm\s+-rf\s+/\*["\']`), "AppleScript do shell script with rm -rf /*"},
		{DangerCritical, regexp.MustCompile(`(?i)do\s+shell\s+script\s+["\'].*mkfs\b`), "AppleScript do shell script with mkfs"},

		{DangerHigh, regexp.MustCompile(`(?i)do\s+shell\s+script\s+["\'].*dd\s+if=/dev/(sd|hd|nvme|vd|r?disk|mmcblk|mapper|loop|dm-|xvd)`), "AppleScript dd reading from a raw device"},
		{DangerCritical, regexp.MustCompile(`(?i)do\s+shell\s+script\s+["\'].*chmod\s+-R\s+777\s+/["\']`), "AppleScript do shell script with chmod 777 /"},

		{DangerHigh, regexp.MustCompile(`(?i)do\s+shell\s+script\s+["\'].*sudo\b`), "AppleScript do shell script with sudo"},
		{DangerHigh, regexp.MustCompile(`(?i)security\s+find-generic-password`), "AppleScript accessing Keychain passwords"},
		{DangerHigh, regexp.MustCompile(`(?i)security\s+delete-generic-password`), "AppleScript deleting Keychain passwords"},
		{DangerHigh, regexp.MustCompile(`(?i)do\s+shell\s+script\s+["\'].*curl\b.*\|\s*bash`), "AppleScript piping curl to bash"},
		{DangerHigh, regexp.MustCompile(`(?i)do\s+shell\s+script\s+["\'].*wget\b.*\|\s*sh`), "AppleScript piping wget to shell"},
		{DangerHigh, regexp.MustCompile(`(?i)do\s+shell\s+script\s+["\'].*nc\b.*-e`), "AppleScript netcat with -e (reverse shell)"},

		{DangerHigh, regexp.MustCompile(`(?i)>\s*\S*\.ssh/`), "writing to SSH directory could install unauthorized keys"},
		{DangerHigh, regexp.MustCompile(`(?i)>\s*/etc/cron`), "writing to cron directory could install persistent scheduled tasks"},
		{DangerHigh, regexp.MustCompile(`(?i)>\s*/etc/passwd`), "writing to passwd file could add unauthorized users"},
		{DangerHigh, regexp.MustCompile(`(?i)>\s*/etc/shadow`), "writing to shadow file could modify password hashes"},
		{DangerHigh, regexp.MustCompile(`(?i)>\s*/etc/sudoers`), "writing to sudoers could grant unauthorized root access"},
		{DangerHigh, regexp.MustCompile(`(?i)>\s*/usr/local/bin/`), "writing to system binary directory could install trojans"},
		{DangerHigh, regexp.MustCompile(`(?i)>\s*/usr/bin/`), "writing to system binary directory could install trojans"},

		{DangerMedium, regexp.MustCompile(`(?i)>\s*~/\.bashrc`), "writing to .bashrc could execute malicious code on shell startup"},
		{DangerMedium, regexp.MustCompile(`(?i)>\s*~/\.zshrc`), "writing to .zshrc could execute malicious code on shell startup"},
		{DangerMedium, regexp.MustCompile(`(?i)>\s*~/\.profile`), "writing to .profile could execute malicious code on login"},
		{DangerMedium, regexp.MustCompile(`(?i)>\s*/etc/hosts`), "writing to hosts file could hijack DNS resolution"},
		{DangerMedium, regexp.MustCompile(`(?i)>\s*/etc/environment`), "writing to environment config could inject malicious variables"},

		{DangerMedium, regexp.MustCompile(`(?i)do\s+shell\s+script.*rm\s+-rf`), "AppleScript do shell script with rm -rf"},
		{DangerMedium, regexp.MustCompile(`(?i)do\s+shell\s+script.*rm\s+-rf\s+\*`), "AppleScript do shell script with rm -rf *"},
		{DangerMedium, regexp.MustCompile(`(?i)do\s+shell\s+script.*>\s*/dev/sd[a-z]`), "AppleScript writing to disk device"},
	}
	return d
}

func (d *DangerousDetector) IsDangerous(command string) bool {
	return d.Check(command).Level >= DangerMedium
}

func (d *DangerousDetector) Check(command string) DangerousCheck {

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

func (d *DangerousDetector) IsExtremelyDangerous(command string) bool {
	check := d.Check(command)
	return check.Level >= DangerCritical
}

func (c DangerousCheck) Suggestion() string {
	if c.Level == DangerNone {
		return "This command appears safe."
	}
	return fmt.Sprintf("[%s] %s", strings.ToUpper(c.Level.String()), c.Reason)
}
