package knight

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

// SelfReflectionReport summarizes Knight's recent activity for human-visible
// output and for appending a meta-lesson to semantic memory.
type SelfReflectionReport struct {
	Window           time.Duration
	ActiveSkills     int
	StagingSkills    int
	UnusedActive     []string
	HighUsageActive  []string
	RecentRejects    []string
	RecentPromotions []string
	Recommendations  []SkillActionRecommendation
	GeneratedAt      time.Time
}

// RunSelfReflection inspects Knight's own recent decisions and produces a short
// summary. It is a low-cost, deterministic pass — no LLM call. The summary is
// also appended to semantic memory as a meta-lesson so future analyzer + eval
// prompts see how earlier Knight decisions actually turned out.
func (k *Knight) RunSelfReflection(_ context.Context, window time.Duration) (SelfReflectionReport, error) {
	if k == nil {
		return SelfReflectionReport{}, fmt.Errorf("knight: nil receiver")
	}
	report := SelfReflectionReport{Window: window, GeneratedAt: time.Now()}
	if window <= 0 {
		window = 7 * 24 * time.Hour
		report.Window = window
	}
	cutoff := time.Now().Add(-window)

	active, _ := k.index.ActiveSkills()
	staging, _ := k.index.StagingSkills()
	report.ActiveSkills = len(active)
	report.StagingSkills = len(staging)

	type usageRow struct {
		ref      string
		count    int
		lastUsed time.Time
	}
	rows := make([]usageRow, 0, len(active))
	for _, s := range active {
		if s == nil {
			continue
		}
		ref := s.Scope + ":" + s.Name
		count, lastUsed, _ := k.SkillUsage(ref)
		rows = append(rows, usageRow{ref: ref, count: count, lastUsed: lastUsed})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].count > rows[j].count })
	for _, r := range rows {
		if r.count == 0 || r.lastUsed.Before(cutoff) {
			report.UnusedActive = append(report.UnusedActive, r.ref)
		} else if r.count >= 3 {
			report.HighUsageActive = append(report.HighUsageActive, fmt.Sprintf("%s (used=%d)", r.ref, r.count))
		}
	}

	if k.rejects != nil {
		recent := k.rejects.Recent(20)
		for _, e := range recent {
			if e.Time.Before(cutoff) {
				continue
			}
			label := fmt.Sprintf("%s/%s [%s]", e.Scope, e.Name, e.Action)
			if e.Reason != "" {
				label += " — " + e.Reason
			}
			report.RecentRejects = append(report.RecentRejects, label)
		}
	}

	mem, _ := k.RecentSemanticMemory(60)
	for _, m := range mem {
		if m.Time.Before(cutoff) {
			continue
		}
		if m.Kind == "skill-promoted" || m.Kind == "skill-auto-promoted" {
			report.RecentPromotions = append(report.RecentPromotions, m.Summary)
		}
	}
	report.Recommendations, _ = k.deriveSkillActionRecommendations(active, staging, cutoff)

	summary := report.MetaLesson()
	if strings.TrimSpace(summary) != "" {
		_ = k.RecordSemanticMemory("self-reflection", summary, nil, "nightly-reflection")
	}
	return report, nil
}

// MetaLesson renders the report as a single short summary suitable for
// recording back into semantic memory.
func (r SelfReflectionReport) MetaLesson() string {
	parts := []string{
		fmt.Sprintf("active=%d staging=%d", r.ActiveSkills, r.StagingSkills),
	}
	if n := len(r.UnusedActive); n > 0 {
		max := 5
		if n < max {
			max = n
		}
		parts = append(parts, fmt.Sprintf("unused=%d (sample: %s)", n, strings.Join(r.UnusedActive[:max], ", ")))
	}
	if n := len(r.HighUsageActive); n > 0 {
		max := 5
		if n < max {
			max = n
		}
		parts = append(parts, fmt.Sprintf("high-usage=%d (sample: %s)", n, strings.Join(r.HighUsageActive[:max], ", ")))
	}
	if n := len(r.RecentRejects); n > 0 {
		max := 5
		if n < max {
			max = n
		}
		parts = append(parts, fmt.Sprintf("recent-rejects=%d (sample: %s)", n, strings.Join(r.RecentRejects[:max], "; ")))
	}
	if n := len(r.RecentPromotions); n > 0 {
		parts = append(parts, fmt.Sprintf("recent-promotions=%d", n))
	}
	if n := len(r.Recommendations); n > 0 {
		max := 3
		if n < max {
			max = n
		}
		sample := make([]string, 0, max)
		for _, rec := range r.Recommendations[:max] {
			sample = append(sample, rec.Action+" "+rec.Ref)
		}
		parts = append(parts, fmt.Sprintf("actions=%d (sample: %s)", n, strings.Join(sample, ", ")))
	}
	return "self-reflection: " + strings.Join(parts, "; ")
}

// FormatHuman returns a multi-line human-readable rendering of the report.
func (r SelfReflectionReport) FormatHuman() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Knight self-reflection (window: %s)\n", r.Window)
	fmt.Fprintf(&b, "  active skills: %d, staging: %d\n", r.ActiveSkills, r.StagingSkills)
	if len(r.UnusedActive) > 0 {
		fmt.Fprintf(&b, "  unused/stale active (%d): %s\n", len(r.UnusedActive), strings.Join(r.UnusedActive, ", "))
	}
	if len(r.HighUsageActive) > 0 {
		fmt.Fprintf(&b, "  high-usage active (%d): %s\n", len(r.HighUsageActive), strings.Join(r.HighUsageActive, ", "))
	}
	if len(r.RecentPromotions) > 0 {
		fmt.Fprintf(&b, "  recent promotions: %d\n", len(r.RecentPromotions))
	}
	if len(r.RecentRejects) > 0 {
		fmt.Fprintf(&b, "  recent rejects: %d\n    %s\n", len(r.RecentRejects), strings.Join(r.RecentRejects, "\n    "))
	}
	if len(r.Recommendations) > 0 {
		fmt.Fprintf(&b, "  recommended actions (%d):\n", len(r.Recommendations))
		for _, rec := range r.Recommendations {
			fmt.Fprintf(&b, "    - %s\n", rec.formatHuman())
		}
	}
	return b.String()
}
