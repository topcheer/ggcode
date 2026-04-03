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

// NewDangerousDetector creates a detector with default dangerous patterns.
func NewDangerousDetector() *DangerousDetector {
	d := &DangerousDetector{}
	d.patterns = []dangerPattern{
		// Critical: destructive commands
		{DangerCritical, regexp.MustCompile(`(?i)\brm\s+(-[a-zA-Z]*f[a-zA-Z]*\s+)?/\s*$`), "rm -rf / would delete the entire filesystem"},
		{DangerCritical, regexp.MustCompile(`(?i)\brm\s+(-[a-zA-Z]*f[a-zA-Z]*\s+)?/\*`), "rm -rf /* would delete the entire filesystem"},
		{DangerCritical, regexp.MustCompile(`(?i)\bmkfs\b`), "mkfs would format a disk"},
		{DangerCritical, regexp.MustCompile(`(?i)\bdd\s+.*\bif=/dev/`), "dd with device input could destroy data"},
		{DangerCritical, regexp.MustCompile(`(?i)\bshred\b`), "shred securely deletes files"},
		{DangerCritical, regexp.MustCompile(`(?i)\bchmod\s+(-[a-zA-Z]*R[a-zA-Z]*\s+)?777\s+/\s*$`), "chmod 777 / is dangerous"},
		{DangerCritical, regexp.MustCompile(`(?i):\(\)\s*\{\s*:\|:\s*&\s*\}\s*;:`), "fork bomb detected"},

		// High: privilege escalation, system-wide changes
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

		// Medium: potentially destructive
		{DangerMedium, regexp.MustCompile(`(?i)\brm\s+(-[a-zA-Z]*r[a-zA-Z]*\s+).*\*`), "recursive rm with wildcard"},
		{DangerMedium, regexp.MustCompile(`(?i)\brm\s+(-[a-zA-Z]*f[a-zA-Z]*\s+)`), "force rm without confirmation"},
		{DangerMedium, regexp.MustCompile(`(?i)\bsudo\b`), "running command with elevated privileges"},
		{DangerMedium, regexp.MustCompile(`(?i)\bcurl\b.*\|\s*bash\b`), "piping remote script to bash"},
		{DangerMedium, regexp.MustCompile(`(?i)\bwget\b.*\|\s*sh\b`), "piping remote script to shell"},
		{DangerMedium, regexp.MustCompile(`(?i)\bnc\b.*-e\b`), "netcat in listen mode could be a reverse shell"},
		{DangerMedium, regexp.MustCompile(`(?i)\b>\s*/dev/sd[a-z]`), "writing directly to a disk device"},
		{DangerMedium, regexp.MustCompile(`(?i)\bcrontab\b`), "modifying cron jobs"},
		{DangerMedium, regexp.MustCompile(`(?i)\bnsenter\b`), "nsenter can escape containers"},
		{DangerMedium, regexp.MustCompile(`(?i)\bchroot\b`), "chroot changes the root directory"},

		// Low: worth noting
		{DangerLow, regexp.MustCompile(`(?i)\bchmod\s+777\b`), "setting world-writable permissions"},
		{DangerLow, regexp.MustCompile(`(?i)\bfind\b.*-delete\b`), "find with -delete"},
		{DangerLow, regexp.MustCompile(`(?i)\bmv\b.*\*.*\b/dev/null\b`), "moving files to /dev/null"},
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
