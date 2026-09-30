// Package similarity provides small set-similarity helpers shared across
// internal packages (agent detectors, tool fuzzy matching, memory duplicate
// checks, knight skill validation).
package similarity

// Jaccard returns the size of the intersection over the size of the union of
// two set-like maps, in the range [0, 1]: J(A,B) = |A ∩ B| / |A ∪ B|.
// It returns 0 when either set is empty (so two empty sets also yield 0).
// Map values encode set membership only; the concrete value type is ignored.
func Jaccard[V bool | struct{}](a, b map[string]V) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	intersection := 0
	for k := range a {
		if _, ok := b[k]; ok {
			intersection++
		}
	}
	union := len(a) + len(b) - intersection
	if union == 0 {
		return 0
	}
	return float64(intersection) / float64(union)
}
