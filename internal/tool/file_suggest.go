package tool

// Smart file path suggestions for non-existent paths.
//
// Research basis: When AI coding agents guess a file path incorrectly (wrong
// extension, typo, wrong directory), they get a generic "no such file" error.
// Claude Code, Cursor, and Aider all have some form of fuzzy path correction
// or "did you mean?" suggestion. Without this, the agent wastes 1-3 iterations
// searching (list_directory, glob, grep) before finding the right file.
//
// ggcode's approach: on any file-not-found error from read_file/edit_file,
// scan the parent directory and nearby directories for files with similar
// names using:
//   1. Levenshtein distance (catches typos like "agent_runtim.go")
//   2. Prefix/stem matching (catches "agent" → "agent_runtime.go")
//   3. Extension correction (catches "agent.go" → "agent_runtime.go")
//
// Zero-LLM-cost, deterministic, and saves 1-3 agent iterations per wrong path.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	// maxSuggestions limits how many alternatives to show.
	maxSuggestions = 5
	// maxLevenshteinDist is the maximum edit distance for typo detection.
	maxLevenshteinDist = 3
	// searchDepth controls how deep we walk nearby directories.
	searchDepth = 2
)

// suggestFilePath returns a human-readable suggestion string for a non-existent
// file path. Returns empty string if no good suggestions are found.
//
// The algorithm:
//  1. Get the basename and stem (filename without extension)
//  2. Walk the parent directory tree up to searchDepth levels
//  3. Score each candidate file by Levenshtein distance and stem overlap
//  4. Return the top matches
func suggestFilePath(absPath string) string {
	base := filepath.Base(absPath)
	if base == "" || base == "." || base == "/" {
		return ""
	}

	stem := strings.TrimSuffix(base, filepath.Ext(base))
	parentDir := filepath.Dir(absPath)

	candidates := collectCandidates(parentDir, base, searchDepth)
	if len(candidates) == 0 {
		return ""
	}

	// Score and rank candidates.
	scored := rankCandidates(base, stem, candidates)

	if len(scored) == 0 {
		return ""
	}

	// Format suggestions
	var parts []string
	for i, c := range scored {
		if i >= maxSuggestions {
			break
		}
		// Show relative to the input path's parent for readability.
		relPath := c.path
		if rel, err := filepath.Rel(parentDir, c.path); err == nil {
			relPath = rel
		}
		parts = append(parts, relPath)
	}

	header := "Did you mean"
	if len(parts) > 1 {
		header = "Similar files found"
	}

	// Show the full relative-from-working-dir path for better context.
	var lines []string
	for i, c := range scored {
		if i >= maxSuggestions {
			break
		}
		lines = append(lines, fmt.Sprintf("  %d. %s", i+1, c.path))
	}

	return fmt.Sprintf("\n%s:\n%s", header, strings.Join(lines, "\n"))
}

type candidate struct {
	path  string
	score float64
}

// collectCandidates walks the directory tree starting from parentDir up to
// maxDepth levels, collecting files that could be relevant. It skips hidden
// directories (.git, .svn, node_modules, vendor, etc.) and binary-looking files.
func collectCandidates(parentDir, base string, maxDepth int) []string {
	// If the parent directory doesn't exist, try walking from the first
	// existing ancestor.
	startDir := parentDir
	for startDir != "" && startDir != "/" && startDir != "." {
		if _, err := os.Stat(startDir); err == nil {
			break
		}
		startDir = filepath.Dir(startDir)
	}
	if startDir == "" || startDir == "/" {
		return nil
	}

	var results []string
	seen := make(map[string]bool)

	skipDirs := map[string]bool{
		".git": true, ".svn": true, "node_modules": true, "vendor": true,
		".hg": true, "__pycache__": true, ".tox": true, "dist": true,
		"build": true, "target": true, ".next": true, ".cache": true,
	}

	stem := strings.TrimSuffix(base, filepath.Ext(base))

	filepath.Walk(startDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			name := info.Name()
			if skipDirs[name] && path != startDir {
				return filepath.SkipDir
			}
			// Check depth
			rel, err := filepath.Rel(startDir, path)
			if err == nil {
				depth := strings.Count(rel, string(filepath.Separator))
				if depth >= maxDepth && path != startDir {
					return filepath.SkipDir
				}
			}
			return nil
		}

		// Skip files with very different names.
		fileName := info.Name()
		fileStem := strings.TrimSuffix(fileName, filepath.Ext(fileName))

		// Quick filter: the stems must share at least 2 chars or have
		// Levenshtein distance <= maxLevenshteinDist.
		if !isLikelyMatch(stem, fileStem, base, fileName) {
			return nil
		}

		if !seen[path] {
			seen[path] = true
			results = append(results, path)
		}
		return nil
	})

	return results
}

// isLikelyMatch does a quick check to see if two filenames could be related.
func isLikelyMatch(stem1, stem2, full1, full2 string) bool {
	// Same stem (different extension)
	if stem1 == stem2 {
		return true
	}
	// One is a prefix of the other
	if strings.HasPrefix(stem1, stem2) || strings.HasPrefix(stem2, stem1) {
		return true
	}
	// Shared prefix of at least 3 chars
	commonPrefix := 0
	minLen := len(stem1)
	if len(stem2) < minLen {
		minLen = len(stem2)
	}
	for i := 0; i < minLen; i++ {
		if stem1[i] == stem2[i] {
			commonPrefix++
		} else {
			break
		}
	}
	if commonPrefix >= 3 {
		return true
	}
	// Levenshtein distance check
	dist := levenshtein(stem1, stem2)
	if dist <= maxLevenshteinDist {
		return true
	}
	// Check full filenames too (extension differences)
	dist = levenshtein(full1, full2)
	return dist <= maxLevenshteinDist
}

// rankCandidates scores and sorts candidates by relevance.
func rankCandidates(base, stem string, paths []string) []candidate {
	var scored []candidate

	for _, p := range paths {
		fileName := filepath.Base(p)
		fileStem := strings.TrimSuffix(fileName, filepath.Ext(fileName))

		var score float64

		// Exact match on stem with different extension (highest priority)
		if fileStem == stem && fileName != base {
			score = 100.0
		} else {
			// Levenshtein-based scoring
			stemDist := levenshtein(stem, fileStem)
			maxLen := len(stem)
			if len(fileStem) > maxLen {
				maxLen = len(fileStem)
			}
			if maxLen > 0 {
				similarity := 1.0 - float64(stemDist)/float64(maxLen)
				score = similarity * 80.0
			}

			// Boost for prefix matches
			if strings.HasPrefix(fileStem, stem) {
				score += 15.0
			}
			if strings.HasPrefix(stem, fileStem) {
				score += 10.0
			}

			// Penalty for path distance (deeper = worse)
			depth := strings.Count(p, string(filepath.Separator))
			score -= float64(depth) * 2.0
		}

		// Only include reasonable matches
		if score > 30.0 {
			scored = append(scored, candidate{path: p, score: score})
		}
	}

	// Sort by score descending
	for i := 0; i < len(scored); i++ {
		for j := i + 1; j < len(scored); j++ {
			if scored[j].score > scored[i].score {
				scored[i], scored[j] = scored[j], scored[i]
			}
		}
	}

	return scored
}

// levenshtein computes the edit distance between two strings (rune-safe).
// toolNameDistance in tool_suggest.go is byte-level; this variant handles
// Unicode filenames correctly.
func levenshtein(a, b string) int {
	if len(a) == 0 {
		return len(b)
	}
	if len(b) == 0 {
		return len(a)
	}

	ra := []rune(a)
	rb := []rune(b)

	prev := make([]int, len(rb)+1)
	curr := make([]int, len(rb)+1)

	for j := range prev {
		prev[j] = j
	}

	for i := 1; i <= len(ra); i++ {
		curr[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			curr[j] = min3(prev[j]+1, curr[j-1]+1, prev[j-1]+cost)
		}
		prev, curr = curr, prev
	}

	return prev[len(rb)]
}
