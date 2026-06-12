package knight

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// SkillActionRecommendation is a deterministic governance suggestion Knight can
// surface to the operator without taking any destructive action on its own.
type SkillActionRecommendation struct {
	Ref      string
	Action   string
	Priority string
	Reason   string
	Command  string
}

func (r SkillActionRecommendation) formatHuman() string {
	parts := []string{fmt.Sprintf("[%s] %s %s", r.Priority, r.Action, r.Ref)}
	if strings.TrimSpace(r.Reason) != "" {
		parts = append(parts, "— "+r.Reason)
	}
	if strings.TrimSpace(r.Command) != "" {
		parts = append(parts, fmt.Sprintf("(next: %s)", r.Command))
	}
	return strings.Join(parts, " ")
}

// SkillGovernanceAudit summarizes active/staging scope health and the strongest
// operator-facing recommendations for pruning or tightening the skill set.
type SkillGovernanceAudit struct {
	Window              time.Duration
	GeneratedAt         time.Time
	ActiveSkills        int
	ProjectSkills       int
	GlobalSkills        int
	FrozenSkills        int
	StagingSkills       int
	GlobalStagingSkills int
	StaleGlobalSkills   []string
	RecentGlobalEvents  []string
	Recommendations     []SkillActionRecommendation
}

func (a SkillGovernanceAudit) FormatHuman() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Knight governance audit (window: %s)\n", a.Window)
	fmt.Fprintf(&b, "  active skills: %d (project=%d, global=%d, frozen=%d)\n", a.ActiveSkills, a.ProjectSkills, a.GlobalSkills, a.FrozenSkills)
	fmt.Fprintf(&b, "  staging skills: %d (global=%d)\n", a.StagingSkills, a.GlobalStagingSkills)
	if len(a.StaleGlobalSkills) > 0 {
		fmt.Fprintf(&b, "  stale global skills (%d): %s\n", len(a.StaleGlobalSkills), strings.Join(a.StaleGlobalSkills, ", "))
	}
	if len(a.RecentGlobalEvents) > 0 {
		fmt.Fprintf(&b, "  recent global events (%d):\n    %s\n", len(a.RecentGlobalEvents), strings.Join(a.RecentGlobalEvents, "\n    "))
	}
	if len(a.Recommendations) > 0 {
		fmt.Fprintf(&b, "  recommended actions (%d):\n", len(a.Recommendations))
		for _, rec := range a.Recommendations {
			fmt.Fprintf(&b, "    - %s\n", rec.formatHuman())
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// RunGovernanceAudit generates a read-only governance summary. Unlike
// RunSelfReflection, it does not write back to semantic memory.
func (k *Knight) RunGovernanceAudit(window time.Duration) (SkillGovernanceAudit, error) {
	if k == nil {
		return SkillGovernanceAudit{}, fmt.Errorf("knight: nil receiver")
	}
	if window <= 0 {
		window = 30 * 24 * time.Hour
	}
	active, _ := k.index.ActiveSkills()
	staging, _ := k.index.StagingSkills()
	now := time.Now()
	cutoff := now.Add(-window)
	audit := SkillGovernanceAudit{
		Window:        window,
		GeneratedAt:   now,
		ActiveSkills:  len(active),
		StagingSkills: len(staging),
	}
	for _, s := range active {
		if s == nil {
			continue
		}
		if s.Scope == "global" {
			audit.GlobalSkills++
		} else {
			audit.ProjectSkills++
		}
		if s.Meta.Frozen {
			audit.FrozenSkills++
		}
	}
	for _, s := range staging {
		if s != nil && s.Scope == "global" {
			audit.GlobalStagingSkills++
		}
	}
	recs, staleGlobals := k.deriveSkillActionRecommendations(active, staging, cutoff)
	audit.Recommendations = recs
	audit.StaleGlobalSkills = staleGlobals
	if k.rejects != nil {
		events := k.rejects.Recent(40)
		for _, event := range events {
			if event.Scope != "global" || event.Time.Before(cutoff) {
				continue
			}
			audit.RecentGlobalEvents = append(audit.RecentGlobalEvents, formatRejectFeedback(event))
			if len(audit.RecentGlobalEvents) >= 8 {
				break
			}
		}
	}
	return audit, nil
}

func (k *Knight) deriveSkillActionRecommendations(active, staging []*SkillEntry, cutoff time.Time) ([]SkillActionRecommendation, []string) {
	stagingRefs := make(map[string]struct{}, len(staging))
	for _, s := range staging {
		if s == nil {
			continue
		}
		stagingRefs[formatSkillRef(s.Scope, s.Name)] = struct{}{}
	}
	rejectCount := make(map[string]int)
	if k.rejects != nil {
		for _, event := range k.rejects.Recent(100) {
			if event.Time.Before(cutoff) {
				continue
			}
			rejectCount[formatSkillRef(event.Scope, event.Name)]++
		}
	}
	recs := map[string]SkillActionRecommendation{}
	var staleGlobals []string
	for _, s := range active {
		if s == nil {
			continue
		}
		ref := formatSkillRef(s.Scope, s.Name)
		snapshot, _ := k.skillUsageSnapshot(ref)
		exposed := snapshot.PromptExposureCount
		promptOK := int(snapshot.PromptSuccessDecayed + 0.5)
		promptFail := int(snapshot.PromptFailureDecayed + 0.5)
		stale := skillLooksStale(s, snapshot, cutoff)
		if stale && s.Scope == "global" {
			staleGlobals = append(staleGlobals, ref)
		}
		if s.Meta.Frozen {
			continue
		}
		if _, alreadyStaged := stagingRefs[ref]; !alreadyStaged {
			if stale {
				if s.Scope == "global" {
					addSkillRecommendation(recs, SkillActionRecommendation{
						Ref:      ref,
						Action:   "review-retire",
						Priority: priorityForRejectCount(rejectCount[ref]),
						Reason:   staleReason(s, snapshot, exposed, cutoff),
						Command:  "/knight freeze " + ref,
					})
				} else {
					addSkillRecommendation(recs, SkillActionRecommendation{
						Ref:      ref,
						Action:   "freeze",
						Priority: priorityForRejectCount(rejectCount[ref]),
						Reason:   staleReason(s, snapshot, exposed, cutoff),
						Command:  "/knight freeze " + ref,
					})
				}
			}
			if exposed >= knightPromptIgnoredThreshold && snapshot.UsageCount == 0 &&
				(promptOK+promptFail) >= knightPromptOutcomeMinSamples && promptFail >= promptOK {
				addSkillRecommendation(recs, SkillActionRecommendation{
					Ref:      ref,
					Action:   "tighten-trigger",
					Priority: "medium",
					Reason:   fmt.Sprintf("shown %d times with no explicit use and weak prompt-visible outcomes (+%d/-%d)", exposed, promptOK, promptFail),
				})
			}
			if rejectCount[ref] >= 2 {
				action := "freeze"
				if s.Scope == "global" {
					action = "review-retire"
				}
				addSkillRecommendation(recs, SkillActionRecommendation{
					Ref:      ref,
					Action:   action,
					Priority: "high",
					Reason:   fmt.Sprintf("recent operator feedback rejected or rolled back this skill %d times", rejectCount[ref]),
					Command:  "/knight freeze " + ref,
				})
			}
		}
	}
	for _, rec := range k.findOverlapRecommendations(active) {
		addSkillRecommendation(recs, rec)
	}
	out := make([]SkillActionRecommendation, 0, len(recs))
	for _, rec := range recs {
		out = append(out, rec)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if priorityRank(out[i].Priority) != priorityRank(out[j].Priority) {
			return priorityRank(out[i].Priority) > priorityRank(out[j].Priority)
		}
		if out[i].Action != out[j].Action {
			return out[i].Action < out[j].Action
		}
		return out[i].Ref < out[j].Ref
	})
	sort.Strings(staleGlobals)
	return out, staleGlobals
}

func (k *Knight) findOverlapRecommendations(active []*SkillEntry) []SkillActionRecommendation {
	type overlapCandidate struct {
		refA, refB string
		similarity float64
		reason     string
	}
	contentByRef := make(map[string]string, len(active))
	for _, entry := range active {
		if entry == nil {
			continue
		}
		if body, err := readSkillContent(entry.Path); err == nil {
			contentByRef[formatSkillRef(entry.Scope, entry.Name)] = string(body)
		}
	}
	var overlaps []overlapCandidate
	for i := 0; i < len(active); i++ {
		if active[i] == nil {
			continue
		}
		refI := formatSkillRef(active[i].Scope, active[i].Name)
		bodyI := contentByRef[refI]
		if bodyI == "" {
			continue
		}
		for j := i + 1; j < len(active); j++ {
			if active[j] == nil || active[i].Scope != active[j].Scope {
				continue
			}
			refJ := formatSkillRef(active[j].Scope, active[j].Name)
			bodyJ := contentByRef[refJ]
			if bodyJ == "" {
				continue
			}
			overlap := computeRuleBasedOverlap(active[i], bodyI, []*SkillEntry{active[j]}, func(*SkillEntry) string { return bodyJ })
			if !overlap.HasOverlap {
				continue
			}
			overlaps = append(overlaps, overlapCandidate{
				refA:       refI,
				refB:       refJ,
				similarity: overlap.WorstSimilarity,
				reason:     formatOverlapRationale(overlap),
			})
		}
	}
	sort.SliceStable(overlaps, func(i, j int) bool { return overlaps[i].similarity > overlaps[j].similarity })
	seen := map[string]struct{}{}
	out := make([]SkillActionRecommendation, 0, len(overlaps))
	for _, overlap := range overlaps {
		key := overlap.refA + "|" + overlap.refB
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, SkillActionRecommendation{
			Ref:      overlap.refA + " <-> " + overlap.refB,
			Action:   "merge-review",
			Priority: "medium",
			Reason:   overlap.reason,
		})
		if len(out) >= 3 {
			break
		}
	}
	return out
}

func (k *Knight) skillUsageSnapshot(ref string) (skillUsage, bool) {
	if k == nil || k.usage == nil {
		return skillUsage{}, false
	}
	return k.usage.Snapshot(ref)
}

func skillLooksStale(entry *SkillEntry, usage skillUsage, cutoff time.Time) bool {
	if entry == nil {
		return false
	}
	if !usage.LastUsed.IsZero() && usage.LastUsed.Before(cutoff) {
		return true
	}
	createdAt := skillCreatedAt(entry)
	return !createdAt.IsZero() && createdAt.Before(cutoff) &&
		usage.UsageCount == 0 &&
		usage.PromptExposureCount >= knightPromptIgnoredThreshold
}

func staleReason(entry *SkillEntry, usage skillUsage, exposed int, cutoff time.Time) string {
	if entry == nil {
		return "skill looks stale"
	}
	if !usage.LastUsed.IsZero() && usage.LastUsed.Before(cutoff) {
		return fmt.Sprintf("no confirmed use since %s", usage.LastUsed.Format("2006-01-02"))
	}
	createdAt := skillCreatedAt(entry)
	if !createdAt.IsZero() && createdAt.Before(cutoff) && usage.UsageCount == 0 {
		return fmt.Sprintf("created %s, shown %d times, but never explicitly used", createdAt.Format("2006-01-02"), exposed)
	}
	return "skill looks stale"
}

func skillCreatedAt(entry *SkillEntry) time.Time {
	if entry == nil {
		return time.Time{}
	}
	if strings.TrimSpace(entry.Meta.CreatedAt) == "" {
		return time.Time{}
	}
	createdAt, err := time.Parse(time.RFC3339, strings.TrimSpace(entry.Meta.CreatedAt))
	if err != nil {
		return time.Time{}
	}
	return createdAt
}

func addSkillRecommendation(dst map[string]SkillActionRecommendation, rec SkillActionRecommendation) {
	rec.Ref = strings.TrimSpace(rec.Ref)
	rec.Action = strings.TrimSpace(rec.Action)
	if rec.Ref == "" || rec.Action == "" {
		return
	}
	if strings.TrimSpace(rec.Priority) == "" {
		rec.Priority = "medium"
	}
	key := rec.Action + "|" + rec.Ref
	if existing, ok := dst[key]; ok {
		if priorityRank(rec.Priority) > priorityRank(existing.Priority) {
			existing.Priority = rec.Priority
		}
		if strings.TrimSpace(rec.Reason) != "" && !strings.Contains(existing.Reason, rec.Reason) {
			if strings.TrimSpace(existing.Reason) == "" {
				existing.Reason = rec.Reason
			} else {
				existing.Reason += "; " + rec.Reason
			}
		}
		if existing.Command == "" {
			existing.Command = rec.Command
		}
		dst[key] = existing
		return
	}
	dst[key] = rec
}

func priorityForRejectCount(count int) string {
	if count >= 2 {
		return "high"
	}
	return "medium"
}

func priorityRank(priority string) int {
	switch strings.ToLower(strings.TrimSpace(priority)) {
	case "high":
		return 3
	case "medium":
		return 2
	case "low":
		return 1
	default:
		return 0
	}
}
