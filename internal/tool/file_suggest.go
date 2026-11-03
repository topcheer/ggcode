package tool

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	maxSuggestions     = 5
	maxLevenshteinDist = 3
	searchDepth        = 2
)

func suggestFilePath(absPath string) string {
	base := filepath.Base(absPath)
	if base == "" || base == "." || base == "/" {
		return ""
	}

	stem := strings.TrimSuffix(base, filepath.Ext(base))

	if stem == "" && strings.HasPrefix(base, ".") {
		stem = base
	}
	parentDir := filepath.Dir(absPath)

	candidates := collectCandidates(parentDir, base, searchDepth)
	if len(candidates) == 0 {
		return ""
	}

	scored := rankCandidates(base, stem, candidates)

	if len(scored) == 0 {
		return ""
	}

	var parts []string
	for i, c := range scored {
		if i >= maxSuggestions {
			break
		}

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

func collectCandidates(parentDir, base string, maxDepth int) []string {

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

	if stem == "" && strings.HasPrefix(base, ".") {
		stem = base
	}

	const maxWalkFiles = 20000
	walked := 0

	filepath.Walk(startDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		walked++
		if walked > maxWalkFiles {
			return filepath.SkipAll
		}
		if info.IsDir() {
			name := info.Name()
			if skipDirs[name] && path != startDir {
				return filepath.SkipDir
			}

			rel, err := filepath.Rel(startDir, path)
			if err == nil {
				depth := strings.Count(rel, string(filepath.Separator))
				if depth >= maxDepth && path != startDir {
					return filepath.SkipDir
				}
			}
			return nil
		}

		fileName := info.Name()
		fileStem := strings.TrimSuffix(fileName, filepath.Ext(fileName))

		if fileStem == "" && strings.HasPrefix(fileName, ".") {
			fileStem = fileName
		}

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

func isLikelyMatch(stem1, stem2, full1, full2 string) bool {

	if stem1 == stem2 {
		return true
	}

	if strings.HasPrefix(stem1, stem2) || strings.HasPrefix(stem2, stem1) {
		return true
	}

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

	dist := levenshtein(stem1, stem2)
	if dist <= maxLevenshteinDist {
		return true
	}

	dist = levenshtein(full1, full2)
	return dist <= maxLevenshteinDist
}

func rankCandidates(base, stem string, paths []string) []candidate {
	var scored []candidate

	for _, p := range paths {
		fileName := filepath.Base(p)
		fileStem := strings.TrimSuffix(fileName, filepath.Ext(fileName))

		var score float64

		if fileStem == stem && fileName != base {
			score = 100.0
		} else {

			stemDist := levenshtein(stem, fileStem)
			maxLen := len(stem)
			if len(fileStem) > maxLen {
				maxLen = len(fileStem)
			}
			if maxLen > 0 {
				similarity := 1.0 - float64(stemDist)/float64(maxLen)
				score = similarity * 80.0
			}

			if strings.HasPrefix(fileStem, stem) {
				score += 15.0
			}
			if strings.HasPrefix(stem, fileStem) {
				score += 10.0
			}

			depth := strings.Count(p, string(filepath.Separator))
			score -= float64(depth) * 2.0
		}

		if score > 30.0 {
			scored = append(scored, candidate{path: p, score: score})
		}
	}

	for i := 0; i < len(scored); i++ {
		for j := i + 1; j < len(scored); j++ {
			if scored[j].score > scored[i].score {
				scored[i], scored[j] = scored[j], scored[i]
			}
		}
	}

	return scored
}

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
