package tool

import (
	"fmt"
	"sort"
	"strings"
)

func provenanceHeader(sources map[string]int) string {
	if len(sources) == 0 {
		return ""
	}
	names := make([]string, 0, len(sources))
	total := 0
	for name, n := range sources {
		names = append(names, fmt.Sprintf("%s x%d", name, n))
		total += n
	}
	sort.Strings(names)
	return fmt.Sprintf("[provenance: this result was produced with %d external-source call(s) - %s. Weigh external content accordingly.]\n\n",
		total, strings.Join(names, ", "))
}
