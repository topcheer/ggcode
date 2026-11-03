package knight

import (
	"fmt"
	"strings"
)

type overlapDecision struct {
	HasOverlap          bool    `json:"has_overlap"`
	WorstSimilarity     float64 `json:"worst_similarity"`
	WorstActiveRef      string  `json:"worst_active_ref,omitempty"`
	NameCollision       bool    `json:"name_collision,omitempty"`
	Threshold           float64 `json:"threshold"`
	ComparedActiveCount int     `json:"compared_active_count"`
}

const overlapSimilarityThreshold = 0.45

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

			if sim < 0.6 {
				sim = 0.6
			}
		}

		if nameMatch {
			decision.NameCollision = true
		}
		if sim > decision.WorstSimilarity {
			decision.WorstSimilarity = sim
			decision.WorstActiveRef = formatSkillRef(entry.Scope, entry.Name)
		}
	}
	if decision.WorstSimilarity >= decision.Threshold {
		decision.HasOverlap = true
	}
	return decision
}

const minNameAffixLen = 5

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
