package knight

import (
	"fmt"
	"strings"
)

// overlapDecision is the result of a deterministic, rule-based comparison
// between a staged candidate and the current set of active skills. It runs
// alongside the LLM-driven BASELINE_REPLAY check so a single self-judging
// model failure cannot silently auto-promote an overlapping skill.
type overlapDecision struct {
	HasOverlap          bool    `json:"has_overlap"`
	WorstSimilarity     float64 `json:"worst_similarity"`
	WorstActiveRef      string  `json:"worst_active_ref,omitempty"`
	NameCollision       bool    `json:"name_collision,omitempty"`
	Threshold           float64 `json:"threshold"`
	ComparedActiveCount int     `json:"compared_active_count"`
}

// overlapSimilarityThreshold is the Jaccard similarity above which two skills
// are considered to cover the same trigger/workflow. Empirically, fingerprints
// in the 0.5–1.0 range describe the same intent in this repo's existing
// skills; below 0.4 they tend to describe distinct rules.
const overlapSimilarityThreshold = 0.45

// computeRuleBasedOverlap fingerprints the candidate plus every active skill
// and returns the worst (highest) Jaccard similarity along with a deterministic
// has-overlap flag. The check is intentionally cheap and does not call any
// LLM — it acts as a hard floor on top of the LLM gate.
func computeRuleBasedOverlap(candidate *SkillEntry, candidateBody string, active []*SkillEntry, readBody func(*SkillEntry) string) overlapDecision {
	decision := overlapDecision{Threshold: overlapSimilarityThreshold}
	if candidate == nil {
		return decision
	}
	candFP := skillSimilarityFingerprint(candidate.Name, candidate.Meta.Description, candidateBody)
	for _, entry := range active {
		if entry == nil || entry.Staging {
			continue
		}
		// Skip self-comparison when revising an existing active skill.
		if entry.Scope == candidate.Scope && strings.EqualFold(entry.Name, candidate.Name) {
			continue
		}
		decision.ComparedActiveCount++
		body := ""
		if readBody != nil {
			body = readBody(entry)
		}
		fp := skillSimilarityFingerprint(entry.Name, entry.Meta.Description, body)
		sim := jaccardSimilarity(candFP, fp)
		nameMatch := nameSimilar(candidate.Name, entry.Name)
		if nameMatch {
			// A direct or near name collision is itself a strong overlap signal,
			// even when descriptions diverge.
			if sim < 0.6 {
				sim = 0.6
			}
		}
		if sim > decision.WorstSimilarity {
			decision.WorstSimilarity = sim
			decision.WorstActiveRef = formatSkillRef(entry.Scope, entry.Name)
			decision.NameCollision = nameMatch
		}
	}
	if decision.WorstSimilarity >= decision.Threshold {
		decision.HasOverlap = true
	}
	return decision
}

// minNameAffixLen is the shortest shared prefix/suffix (slug chars) that
// still counts as a name similarity (#1264): "go" vs "golang" share only 2
// and "test" vs "latest" share only 4 - both used to flip nameMatch and
// hard-floor Jaccard at 0.6, unconditionally blocking auto-promotion of
// unrelated skills. Genuine revisions share near-complete names
// ("golang-ci" vs "golang" share 6, "go-verify" vs "verify" share 6).
const minNameAffixLen = 5

// nameSimilar returns true if two skill names match exactly (case-insensitive)
// or share a substantial slug-level prefix/suffix (#1264: at least
// minNameAffixLen chars - short affixes like go/golang or test/latest are
// coincidental, not similarity).
func nameSimilar(a, b string) bool {
	a = strings.ToLower(strings.TrimSpace(a))
	b = strings.ToLower(strings.TrimSpace(b))
	if a == "" || b == "" {
		return false
	}
	if a == b {
		return true
	}
	shorter := len(a)
	if len(b) < shorter {
		shorter = len(b)
	}
	if shorter >= minNameAffixLen {
		if strings.HasPrefix(a, b) || strings.HasPrefix(b, a) {
			return true
		}
		if strings.HasSuffix(a, b) || strings.HasSuffix(b, a) {
			return true
		}
	}
	return false
}

// formatOverlapRationale renders an overlapDecision as a short human-readable
// string for inclusion in eval logs and user-facing notifications.
func formatOverlapRationale(decision overlapDecision) string {
	if !decision.HasOverlap {
		return ""
	}
	parts := []string{
		fmt.Sprintf("rule-based overlap with %s (similarity %.2f ≥ %.2f)", decision.WorstActiveRef, decision.WorstSimilarity, decision.Threshold),
	}
	if decision.NameCollision {
		parts = append(parts, "name collides with existing active skill")
	}
	return strings.Join(parts, "; ")
}
