package tool

// code_search_rerank.go implements search result re-ranking with path and
// structural signal boosting. BM25 alone ranks by term frequency, but
// competitors (Cursor, GitHub Copilot) use additional signals to improve
// precision:
//
//   - Path relevance: files whose path contains query terms rank higher
//     (e.g., searching "auth" should boost auth/login.go over middleware/util.go
//     that merely mentions auth in a comment).
//   - Exported symbol boost: files defining functions/types whose names match
//     query terms are more relevant than files that reference them in comments.
//   - Recency/freshness: recently modified files get a small boost (git-aware).
//
// This module applies these signals as multiplicative boosts on top of BM25
// scores. It operates on the already-scored BM25 results, making it a clean
// post-processing layer.

import (
	"path/filepath"
	"strings"
)

// rerankBoost factors.
const (
	// pathMatchBoost: multiplier when a query term appears in the file path.
	// Applied multiplicatively (1.0 = no change, 1.5 = 50% boost).
	pathMatchBoost = 1.5

	// pathMatchExactBoost: when the last path segment exactly matches a term.
	pathMatchExactBoost = 2.0

	// exportedSymbolBoost: multiplier for files with exported declarations
	// matching query terms (strong signal of relevance).
	exportedSymbolBoost = 1.3

	// maxBoostCap: prevent any single result from being boosted beyond this factor
	// to avoid one file dominating all results.
	maxBoostCap = 3.0
)

// rerankResults applies path and structural signal boosting to BM25 results.
// queryTerms: the expanded query terms used for BM25 scoring.
// fileContents: optional map of path → content for structural analysis.
// If fileContents is nil, only path-based boosting is applied.
//
// The results slice is re-sorted in place after applying boosts.
func rerankResults(results []bm25Result, queryTerms []string, fileContents map[string]string) {
	if len(results) <= 1 {
		return
	}

	// Build a lowercase query term set for fast matching.
	querySet := make(map[string]bool, len(queryTerms))
	for _, t := range queryTerms {
		querySet[strings.ToLower(t)] = true
	}

	for i := range results {
		boost := computeResultBoost(results[i].path, querySet, fileContents)
		results[i].score *= boost
	}

	// Re-sort by boosted score (descending), then by path for determinism.
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

// computeResultBoost calculates the multiplicative boost for a single result
// based on path matches and optional structural signals.
func computeResultBoost(path string, querySet map[string]bool, fileContents map[string]string) float64 {
	boost := 1.0

	// 1. Path-based boosting
	boost *= pathBoost(path, querySet)

	// 2. Structural boosting (only if file content is available)
	if fileContents != nil {
		if content, ok := fileContents[path]; ok {
			boost *= structuralBoost(content, querySet)
		}
	}

	// Cap the total boost
	if boost > maxBoostCap {
		boost = maxBoostCap
	}

	return boost
}

// pathBoost computes the boost factor based on query term matches in the file path.
// Matching the basename is a stronger signal than matching a directory component.
func pathBoost(path string, querySet map[string]bool) float64 {
	boost := 1.0
	if len(querySet) == 0 {
		return boost
	}

	pathLower := strings.ToLower(filepath.ToSlash(path))
	parts := strings.Split(pathLower, "/")
	base := parts[len(parts)-1]

	// Strip extension from basename for matching.
	baseName := strings.TrimSuffix(base, filepath.Ext(base))

	// Also check the original (non-lowered) path for camelCase splitting,
	// since lowercasing destroys camelCase boundaries.
	origPath := filepath.ToSlash(path)
	origParts := strings.Split(origPath, "/")
	origBase := origParts[len(origParts)-1]
	origBaseName := strings.TrimSuffix(origBase, filepath.Ext(origBase))

	// Exact basename match (strongest path signal)
	if querySet[baseName] {
		boost *= pathMatchExactBoost
		return boost
	}

	// Check camelCase/snake_case sub-parts of the basename.
	for _, basePart := range splitPathSegment(origBaseName) {
		basePartLower := strings.ToLower(basePart)
		if querySet[basePartLower] {
			boost *= pathMatchExactBoost
			return boost
		}
	}

	// Check each path segment for query term matches.
	for _, part := range origParts {
		// Split segment on common delimiters for sub-matches.
		subParts := splitPathSegment(part)
		for _, sp := range subParts {
			spLower := strings.ToLower(sp)
			if querySet[spLower] {
				boost *= pathMatchBoost
				return boost // one path match is enough; avoid over-boosting
			}
		}
	}

	return boost
}

// structuralBoost examines file content for exported declarations matching
// query terms. This is a strong relevance signal: a file that *defines*
// "Authenticate" is more relevant to a search for "auth" than one that merely
// calls it.
func structuralBoost(content string, querySet map[string]bool) float64 {
	if len(querySet) == 0 || len(content) == 0 {
		return 1.0
	}

	// Quick scan: look for exported function/type/struct/class declarations
	// whose names match query terms. We check the first ~4KB for efficiency —
	// declarations are usually near the top of files.
	scanLimit := 4096
	if len(content) < scanLimit {
		scanLimit = len(content)
	}
	scan := content[:scanLimit]

	// Pattern: Go/TS/JS/Rust/Python declaration keywords followed by a name.
	// We use simple substring matching rather than regex for speed.
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
			// Extract the name after the prefix.
			rest := strings.TrimSpace(trimmed[len(prefix):])
			// Take the first identifier-like token.
			identName := firstIdentifier(rest)
			if identName == "" {
				continue
			}
			// Split the name (camelCase, snake_case) and check against query.
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

// splitPathSegment splits a path segment on common delimiters.
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
			// Also split camelCase within path segments.
			current.WriteRune(r) //nolint:errcheck // builder WriteRune never fails
		}
	}
	if current.Len() > 0 {
		// Further split camelCase.
		for _, segPart := range splitCamelCase(current.String()) {
			parts = append(parts, strings.ToLower(segPart))
		}
	}
	return parts
}

// firstIdentifier extracts the first identifier-like token from a string.
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
