package tool

import (
	"testing"
)

func TestRerankResults_PathBoost(t *testing.T) {
	// Two results with equal BM25 scores; the one with "auth" in its path
	// should be boosted above the one without.
	results := []bm25Result{
		{path: "internal/middleware/util.go", score: 5.0},
		{path: "internal/auth/login.go", score: 5.0},
	}
	queryTerms := []string{"auth", "login"}
	rerankResults(results, queryTerms, nil)

	if results[0].path != "internal/auth/login.go" {
		t.Errorf("expected internal/auth/login.go first, got %s (score=%.2f)", results[0].path, results[0].score)
	}
}

func TestRerankResults_ExactBasenameMatch(t *testing.T) {
	// Exact basename match should be stronger than directory match.
	results := []bm25Result{
		{path: "src/database/connection.go", score: 5.0},
		{path: "src/database/database.go", score: 5.0},
	}
	queryTerms := []string{"database"}
	rerankResults(results, queryTerms, nil)

	// "database.go" has exact basename match, should rank first.
	if results[0].path != "src/database/database.go" {
		t.Errorf("expected src/database/database.go first, got %s", results[0].path)
	}
}

func TestRerankResults_StructuralBoost(t *testing.T) {
	// Two files with equal BM25, but one defines an exported function matching
	// the query term. Structural signal should boost it.
	fileContents := map[string]string{
		"a/caller.go": `package a
import "fmt"
func doSomething() {
	fmt.Println("cache")
}`,
		"a/cache.go": `package a
import "fmt"

// Cache stores key-value pairs
type Cache struct {
	data map[string]string
}

func NewCache() *Cache {
	return &Cache{}
}`,
	}
	results := []bm25Result{
		{path: "a/caller.go", score: 5.0},
		{path: "a/cache.go", score: 5.0},
	}
	queryTerms := []string{"cache"}
	rerankResults(results, queryTerms, fileContents)

	if results[0].path != "a/cache.go" {
		t.Errorf("expected a/cache.go first (has type Cache), got %s", results[0].path)
	}
}

func TestRerankResults_BoostCap(t *testing.T) {
	// A file matching both exact basename and structural should not be boosted
	// beyond maxBoostCap (3.0x).
	results := []bm25Result{
		{path: "config.go", score: 1.0},
		{path: "main.go", score: 0.9},
	}
	queryTerms := []string{"config"}
	rerankResults(results, queryTerms, nil)

	// config.go should be first, but score should be capped at 3.0.
	if results[0].path != "config.go" {
		t.Errorf("expected config.go first, got %s", results[0].path)
	}
	if results[0].score > 3.1 {
		t.Errorf("score should be capped at ~3.0, got %.2f", results[0].score)
	}
}

func TestRerankResults_SingleResult(t *testing.T) {
	// Single result should be returned unchanged.
	results := []bm25Result{
		{path: "a/b.go", score: 1.0},
	}
	rerankResults(results, []string{"foo"}, nil)
	if len(results) != 1 || results[0].path != "a/b.go" {
		t.Errorf("single result should be unchanged")
	}
}

func TestRerankResults_NoQueryTerms(t *testing.T) {
	// Empty query terms should not change ordering.
	results := []bm25Result{
		{path: "z.go", score: 5.0},
		{path: "a.go", score: 3.0},
	}
	rerankResults(results, []string{}, nil)
	if results[0].path != "z.go" {
		t.Errorf("expected z.go first (higher score), got %s", results[0].path)
	}
}

func TestPathBoost_CamelCasePath(t *testing.T) {
	// camelCase in path segments should be split for matching.
	querySet := map[string]bool{"http": true}
	boost := pathBoost("internal/httpClient.go", querySet)
	if boost <= 1.0 {
		t.Errorf("expected boost > 1.0 for http in httpClient path, got %.2f", boost)
	}
}

func TestPathBoost_NoMatch(t *testing.T) {
	querySet := map[string]bool{"auth": true}
	boost := pathBoost("internal/util.go", querySet)
	if boost != 1.0 {
		t.Errorf("expected boost 1.0 for no match, got %.2f", boost)
	}
}

func TestStructuralBoost_PythonDef(t *testing.T) {
	querySet := map[string]bool{"authenticate": true}
	content := `def authenticate_user(token):
    return validate(token)
`
	boost := structuralBoost(content, querySet)
	if boost <= 1.0 {
		t.Errorf("expected boost for Python def matching query, got %.2f", boost)
	}
}

func TestStructuralBoost_TypeScriptExport(t *testing.T) {
	querySet := map[string]bool{"scheduler": true}
	content := `export class Scheduler {
  constructor() {}
}`
	boost := structuralBoost(content, querySet)
	if boost <= 1.0 {
		t.Errorf("expected boost for TS class matching query, got %.2f", boost)
	}
}

func TestStructuralBoost_NoDeclaration(t *testing.T) {
	querySet := map[string]bool{"auth": true}
	content := `// This file calls auth functions
import { login } from "./auth"
console.log("auth module loaded")
`
	boost := structuralBoost(content, querySet)
	if boost != 1.0 {
		t.Errorf("expected no boost (no declaration), got %.2f", boost)
	}
}

func TestSplitPathSegment(t *testing.T) {
	tests := []struct {
		input    string
		expected []string
	}{
		{"auth-service", []string{"auth", "service"}},
		{"user_model", []string{"user", "model"}},
		{"httpClient", []string{"http", "client"}},
		{"simple", []string{"simple"}},
	}
	for _, tc := range tests {
		got := splitPathSegment(tc.input)
		if len(got) != len(tc.expected) {
			t.Errorf("splitPathSegment(%q): got %v, expected %v", tc.input, got, tc.expected)
			continue
		}
		for i := range got {
			if got[i] != tc.expected[i] {
				t.Errorf("splitPathSegment(%q)[%d]: got %q, expected %q", tc.input, i, got[i], tc.expected[i])
			}
		}
	}
}

func TestFirstIdentifier(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"Authenticate(token)", "Authenticate"},
		{"NewCache", "NewCache"},
		{"123abc", "123abc"},
		{"(x int)", ""},
		{"", ""},
	}
	for _, tc := range tests {
		got := firstIdentifier(tc.input)
		if got != tc.expected {
			t.Errorf("firstIdentifier(%q): got %q, expected %q", tc.input, got, tc.expected)
		}
	}
}
