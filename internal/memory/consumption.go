package memory

import (
	"os"
	"path/filepath"
	"strings"
)

const (
	budgetFloorRatio = 0.25

	minBudgetUses = 8

	minConsumedSamples = 2

	fingerprintMinLen = 12

	scanKeyMinLen = 6

	scanFingerprintMax = 64
)

func containsStandaloneKey(text, key string) bool {
	for from := 0; ; {
		i := strings.Index(text[from:], key)
		if i < 0 {
			return false
		}
		i += from
		if boundaryRune(text, i-1) && boundaryRune(text, i+len(key)) {
			return true
		}
		from = i + 1
	}
}

func boundaryRune(text string, i int) bool {
	if i < 0 || i >= len(text) {
		return true
	}
	c := text[i]
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_':
		return false
	}
	return true
}

func (am *AutoMemory) ScanConsumption(text string) int {
	if am == nil || len(text) < fingerprintMinLen {
		return 0
	}
	lower := strings.ToLower(text)

	am.mu.Lock()
	idx := am.loadUsage()
	keys := make([]string, 0, len(idx.Entries))
	for key, rec := range idx.Entries {
		if rec != nil && rec.Uses > 0 {
			keys = append(keys, key)
		}
	}
	am.mu.Unlock()

	var matched []string
	fpReads := 0
	for _, key := range keys {

		if len(key) >= scanKeyMinLen && containsStandaloneKey(lower, strings.ToLower(key)) {
			matched = append(matched, key)
			continue
		}
		if fpReads >= scanFingerprintMax {
			continue
		}
		fpReads++
		if fp := am.entryFingerprint(key); fp != "" && strings.Contains(lower, fp) {
			matched = append(matched, key)
		}
	}
	am.RecordConsumption(matched)
	return len(matched)
}

func (am *AutoMemory) entryFingerprint(key string) string {
	data, err := os.ReadFile(filepath.Join(am.dir, key+".md"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.ToLower(strings.TrimSpace(line))
		line = strings.TrimLeft(line, "#> -")
		line = strings.TrimSpace(line)
		if len(line) >= fingerprintMinLen {
			return line
		}
	}
	return ""
}

func (am *AutoMemory) EffectiveInlineBudget() int {
	if am == nil {
		return maxTotalInlineBytes
	}
	am.mu.Lock()
	defer am.mu.Unlock()
	idx := am.loadUsage()
	var uses, consumed int
	for _, rec := range idx.Entries {
		if rec == nil {
			continue
		}
		uses += rec.Uses
		consumed += rec.Consumed
	}
	if consumed < minConsumedSamples || uses < minBudgetUses {
		return maxTotalInlineBytes
	}
	rate := float64(consumed) / float64(uses)
	if rate < budgetFloorRatio {
		rate = budgetFloorRatio
	}

	if rate > 1.0 {
		rate = 1.0
	}
	return int(float64(maxTotalInlineBytes) * rate)
}
