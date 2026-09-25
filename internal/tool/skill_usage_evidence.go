package tool

import (
	"fmt"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/commands"
)

// skillStaleAfter marks the age beyond which a last-used timestamp is
// surfaced as stale evidence in skill listings.
const skillStaleAfter = 30 * 24 * time.Hour

// skillInventoryLister lists every loaded skill name. Implemented by
// commands.Manager; asserted narrowly so test doubles stay simple.
type skillInventoryLister interface {
	SkillNames() []string
}

// skillUsageKey normalizes a skill name the same way RecordUsage does, so
// evidence lookups hit the persisted usage file keys.
func skillUsageKey(name string) string {
	return strings.TrimSpace(strings.TrimPrefix(name, "/"))
}

// skillUsageEvidenceLabel renders raw usage evidence into a compact,
// model-readable marker: "(never used)", "(used 3x, last 2d ago)" or
// "(used 1x, last 45d ago, stale)".
func skillUsageEvidenceLabel(ev commands.SkillUsageEvidence, ok bool, now time.Time) string {
	if !ok || ev.UsageCount <= 0 || ev.LastUsedAt <= 0 {
		return "(never used)"
	}
	age := now.Sub(time.UnixMilli(ev.LastUsedAt))
	if age < 0 {
		age = 0
	}
	suffix := " ago"
	if age > skillStaleAfter {
		suffix = " ago, stale"
	}
	return fmt.Sprintf("(used %dx, last %s%s)", ev.UsageCount, formatSkillAge(age), suffix)
}

// formatSkillAge renders a duration in compact coarse units.
func formatSkillAge(d time.Duration) string {
	switch {
	case d < time.Hour:
		return "<1h"
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d < 60*24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	case d < 365*24*time.Hour:
		return fmt.Sprintf("%dmo", int(d.Hours()/(24*30)))
	default:
		return fmt.Sprintf("%dy", int(d.Hours()/(24*365)))
	}
}

// buildSkillInventoryNote summarizes cross-session usage evidence across all
// loaded skills. It closes the feedback loop for skill sedimentation: the
// agent about to create yet another skill sees how much of the existing
// inventory is actually proven, and is nudged toward reusing or refining
// proven skills instead of growing unproven surface.
func buildSkillInventoryNote(names []string) string {
	if len(names) == 0 {
		return ""
	}
	snap := commands.SkillUsageSnapshot()
	used := 0
	for _, n := range names {
		if ev, ok := snap[skillUsageKey(n)]; ok && ev.UsageCount > 0 {
			used++
		}
	}
	neverUsed := len(names) - used
	var sb strings.Builder
	fmt.Fprintf(&sb, "Skill inventory: %d total, %d with recorded usage", len(names), used)
	if neverUsed > 0 {
		fmt.Fprintf(&sb, ", %d never used", neverUsed)
	}
	sb.WriteString(".")
	if neverUsed > 0 {
		sb.WriteString(" Prefer reusing or refining a proven skill over creating a new one when possible.")
	}
	return sb.String()
}
