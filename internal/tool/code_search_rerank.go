package tool

import (
	"path/filepath"
	"strings"
)

const (
	pathMatchBoost = 1.5

	pathMatchExactBoost = 2.0

	exportedSymbolBoost = 1.3

	maxBoostCap = 3.0
)

func rerankResults(results []bm25Result, queryTerms []string, fileContents map[string]string) {
	if len(results) <= 1 {
		return
	}

	querySet := make(map[string]bool, len(queryTerms))
	for _, t := range queryTerms {
		querySet[strings.ToLower(t)] = true
	}

	for i := range results {
		boost := computeResultBoost(results[i].path, querySet, fileContents)
		results[i].score *= boost
	}

	for i := 1; i < len(results); i++ {
		for j := i; j > 0; j-- {
			if results[j].score > results[j-1].score ||
				(results[j].score == results[j-1].score && results[j].path < results[j-1].path) {
				results[j], results[j-1] = results[j-1], results[j]
			} else {
				break
			}
		}
	}
}

func computeResultBoost(path string, querySet map[string]bool, fileContents map[string]string) float64 {
	boost := 1.0

	boost *= pathBoost(path, querySet)

	if fileContents != nil {
		if content, ok := fileContents[path]; ok {
			boost *= structuralBoost(content, querySet)
		}
	}

	if boost > maxBoostCap {
		boost = maxBoostCap
	}

	return boost
}

func pathBoost(path string, querySet map[string]bool) float64 {
	boost := 1.0
	if len(querySet) == 0 {
		return boost
	}

	pathLower := strings.ToLower(filepath.ToSlash(path))
	parts := strings.Split(pathLower, "/")
	base := parts[len(parts)-1]

	baseName := strings.TrimSuffix(base, filepath.Ext(base))

	origPath := filepath.ToSlash(path)
	origParts := strings.Split(origPath, "/")
	origBase := origParts[len(origParts)-1]
	origBaseName := strings.TrimSuffix(origBase, filepath.Ext(origBase))

	if querySet[baseName] {
		boost *= pathMatchExactBoost
		return boost
	}

	for _, basePart := range splitPathSegment(origBaseName) {
		basePartLower := strings.ToLower(basePart)
		if querySet[basePartLower] {
			boost *= pathMatchExactBoost
			return boost
		}
	}

	for _, part := range origParts {

		subParts := splitPathSegment(part)
		for _, sp := range subParts {
			spLower := strings.ToLower(sp)
			if querySet[spLower] {
				boost *= pathMatchBoost
				return boost
			}
		}
	}

	return boost
}

func structuralBoost(content string, querySet map[string]bool) float64 {
	if len(querySet) == 0 || len(content) == 0 {
		return 1.0
	}

	scanLimit := 4096
	if len(content) < scanLimit {
		scanLimit = len(content)
	}
	scan := content[:scanLimit]

	declPrefixes := []string{
		"func ", "type ", "struct ",
		"function ", "class ", "export function", "export class",
		"def ", "async def ",
		"pub fn ", "fn ", "pub struct ",
		"interface ",
	}

	lines := strings.Split(scan, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		for _, prefix := range declPrefixes {
			if !strings.HasPrefix(trimmed, prefix) {
				continue
			}

			rest := strings.TrimSpace(trimmed[len(prefix):])

			identName := firstIdentifier(rest)
			if identName == "" {
				continue
			}

			for _, namePart := range splitPathSegment(identName) {
				partLower := strings.ToLower(namePart)
				if len(partLower) >= 2 && querySet[partLower] {
					return exportedSymbolBoost
				}
			}
		}
	}

	return 1.0
}

func splitPathSegment(segment string) []string {
	var parts []string
	var current strings.Builder
	for _, r := range segment {
		if r == '-' || r == '_' || r == '.' {
			if current.Len() > 0 {
				parts = append(parts, current.String())
				current.Reset()
			}
		} else {

			current.WriteRune(r) //nolint:errcheck // builder WriteRune never fails
		}
	}
	if current.Len() > 0 {

		for _, segPart := range splitCamelCase(current.String()) {
			parts = append(parts, strings.ToLower(segPart))
		}
	}
	return parts
}

func firstIdentifier(s string) string {
	var result strings.Builder
	for _, ch := range s {
		if ch == '_' || ch == '$' || (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') {
			result.WriteRune(ch)
		} else {
			break
		}
	}
	return result.String()
}
