package agent

func derivedEditTools(extra map[string]bool) map[string]bool {
	m := make(map[string]bool, len(sourceMutatingTools)+len(extra))
	for t := range sourceMutatingTools {
		m[t] = true
	}
	for t := range extra {
		m[t] = true
	}
	return m
}
