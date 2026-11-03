package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/topcheer/ggcode/internal/debug"
)

type DuplicateCheck struct {
	SimilarTo       string
	Similarity      float64
	ExistingContent string
}

func (dc DuplicateCheck) IsDuplicate() bool {
	return dc.Similarity >= 0.6
}

func (am *AutoMemory) CheckDuplicate(key, content string) DuplicateCheck {
	metas, err := am.collectMetas()
	if err != nil {
		debug.Log("memory", "duplicate check: failed to read dir %s: %v", am.dir, err)
		return DuplicateCheck{}
	}

	newTokens := tokenizeOrdered(key)
	if len(newTokens) == 0 {
		return DuplicateCheck{}
	}

	var best DuplicateCheck

	for _, m := range metas {
		existingTokens := tokenizeOrdered(m.Key)
		sim := keySimilarity(newTokens, existingTokens)

		if sim > best.Similarity {
			best = DuplicateCheck{
				SimilarTo:       m.Key,
				Similarity:      sim,
				ExistingContent: readExistingSnippet(am.dir, m.Key),
			}
		}

		if m.Key == disambiguateKey(key, sanitizeKey(key)) {

			return DuplicateCheck{
				SimilarTo:       m.Key,
				Similarity:      1.0,
				ExistingContent: readExistingSnippet(am.dir, m.Key),
			}
		}
	}

	return best
}

func tokenizeOrdered(s string) []string {
	var tokens []string
	var current strings.Builder
	flush := func() {
		if current.Len() > 0 {
			tokens = append(tokens, current.String())
			current.Reset()
		}
	}
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			current.WriteRune(unicode.ToLower(r))
		} else {
			flush()
		}
	}
	flush()
	return tokens
}

func keySimilarity(a, b []string) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	if a[0] == b[0] {
		return jaccardSimilarity(sliceToSet(a), sliceToSet(b))
	}
	if len(a) < 2 || len(b) < 2 {
		return jaccardSimilarity(sliceToSet(a), sliceToSet(b))
	}
	return jaccardSimilarity(bigramSet(a), bigramSet(b))
}

func bigramSet(tokens []string) map[string]struct{} {
	out := make(map[string]struct{}, len(tokens)-1)
	for i := 0; i+1 < len(tokens); i++ {
		out[tokens[i]+"\x00"+tokens[i+1]] = struct{}{}
	}
	return out
}

func sliceToSet(tokens []string) map[string]struct{} {
	out := make(map[string]struct{}, len(tokens))
	for _, t := range tokens {
		out[t] = struct{}{}
	}
	return out
}

func tokenize(s string) map[string]struct{} {
	tokens := make(map[string]struct{})
	var current strings.Builder

	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			current.WriteRune(unicode.ToLower(r))
		} else {
			if current.Len() > 0 {
				tokens[current.String()] = struct{}{}
				current.Reset()
			}
		}
	}
	if current.Len() > 0 {
		tokens[current.String()] = struct{}{}
	}
	return tokens
}

func jaccardSimilarity(a, b map[string]struct{}) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 0
	}

	intersection := 0
	for token := range a {
		if _, ok := b[token]; ok {
			intersection++
		}
	}

	union := len(a) + len(b) - intersection
	if union == 0 {
		return 0
	}
	return float64(intersection) / float64(union)
}

func (dc DuplicateCheck) FormatDuplicateWarning(newKey string) string {
	if !dc.IsDuplicate() {
		return ""
	}
	if dc.Similarity >= 1.0 {
		return fmt.Sprintf("Warning: memory %q already exists. Consider updating the existing entry instead of creating a duplicate.", dc.SimilarTo)
	}
	return fmt.Sprintf("Warning: memory %q is very similar to existing %q (similarity: %.0f%%). Consider updating the existing entry or using a more distinct key.",
		newKey, dc.SimilarTo, dc.Similarity*100)
}

func readExistingSnippet(dir, key string) string {
	data, err := os.ReadFile(filepath.Join(dir, key+".md"))
	if err != nil {
		return ""
	}
	s := string(data)
	if len([]rune(s)) > 200 {
		return string([]rune(s)[:200]) + "..."
	}
	return s
}
