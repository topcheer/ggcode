package tool

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestSplitCamelCase(t *testing.T) {
	tests := []struct {
		input string
		want  []string
	}{
		{"HttpRequest", []string{"Http", "Request"}},
		{"httpClient", []string{"http", "Client"}},
		{"BM25Index", []string{"BM25", "Index"}},
		{"HTTPRequest", []string{"HTTP", "Request"}},
		{"simple", []string{"simple"}},
		{"", nil},
		{"A", []string{"A"}},
		{"ParseJSONResponse", []string{"Parse", "JSON", "Response"}},
	}
	for _, tt := range tests {
		got := splitCamelCase(tt.input)
		if len(got) != len(tt.want) {
			t.Errorf("splitCamelCase(%q) = %v, want %v", tt.input, got, tt.want)
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("splitCamelCase(%q)[%d] = %q, want %q", tt.input, i, got[i], tt.want[i])
			}
		}
	}
}

func TestTokenizeForSearch(t *testing.T) {
	text := "func HandleHttpRequest(req *http.Request) error"
	terms := tokenizeForSearch(text)

	// "func", "error", "req", "request" are stopwords
	// "Handle" → "handle", "HttpRequest" → "http" + "request" (stopword)
	// "http" → "http", "Request" → "request" (stopword)
	termSet := make(map[string]bool)
	for _, term := range terms {
		termSet[term] = true
	}

	// Should contain "handle" and "http" (lowercased, non-stopword)
	if !termSet["handle"] {
		t.Errorf("expected 'handle' in tokens, got %v", terms)
	}
	if !termSet["http"] {
		t.Errorf("expected 'http' in tokens, got %v", terms)
	}

	// Stopwords should be filtered
	if termSet["func"] {
		t.Errorf("stopword 'func' should be filtered")
	}
	if termSet["error"] {
		t.Errorf("stopword 'error' should be filtered")
	}
	if termSet["request"] {
		t.Errorf("stopword 'request' should be filtered")
	}
	if termSet["req"] {
		t.Errorf("stopword 'req' should be filtered")
	}
}

func TestBuildBM25Index(t *testing.T) {
	contents := map[string]string{
		"auth.go":   "func authenticate user password token jwt secret key",
		"server.go": "func serve http handler router middleware listen port",
		"auth_test": "authenticate password token test",
	}

	idx := buildBM25Index(contents)
	if idx == nil {
		t.Fatal("expected non-nil index")
	}
	if len(idx.docs) != 3 {
		t.Errorf("expected 3 docs, got %d", len(idx.docs))
	}
	if idx.avgLength <= 0 {
		t.Error("expected positive avgLength")
	}

	// "authenticate" appears in 2 files
	if idx.df["authenticate"] != 2 {
		t.Errorf("expected df[authenticate]=2, got %d", idx.df["authenticate"])
	}
	// "router" appears in 1 file
	if idx.df["router"] != 1 {
		t.Errorf("expected df[router]=1, got %d", idx.df["router"])
	}
}

func TestBM25Score(t *testing.T) {
	contents := map[string]string{
		"auth.go":     "authentication password token login session cookie",
		"database.go": "database connection pool query transaction",
		"auth_test":   "authentication password token login",
		"main.go":     "main entry point initialization startup",
	}

	idx := buildBM25Index(contents)
	queryTerms := []string{"authentication", "password", "login"}

	results := idx.score(queryTerms, 10)
	if len(results) == 0 {
		t.Fatal("expected non-empty results")
	}

	// auth.go and auth_test should both rank high (contain all query terms).
	// BM25 length normalization means the shorter doc (auth_test) may rank
	// first — both are acceptable as top result.
	topPaths := make(map[string]bool)
	for _, r := range results[:min(2, len(results))] {
		topPaths[r.path] = true
	}
	if !topPaths["auth.go"] {
		t.Errorf("expected auth.go in top 2, got first: %s", results[0].path)
	}

	// database.go should not appear (no matching terms)
	for _, r := range results {
		if r.path == "database.go" {
			t.Error("database.go should not appear in results")
		}
	}

	// Results should be sorted by score descending
	for i := 1; i < len(results); i++ {
		if results[i].score > results[i-1].score {
			t.Error("results not sorted by score descending")
		}
	}
}

func TestBM25ScoreEmptyQuery(t *testing.T) {
	contents := map[string]string{
		"auth.go": "authentication password token",
	}
	idx := buildBM25Index(contents)
	results := idx.score(nil, 10)
	if results != nil {
		t.Error("expected nil results for empty query")
	}
}

func TestBM25ScoreTopK(t *testing.T) {
	contents := map[string]string{
		"a.go": "alpha beta gamma",
		"b.go": "alpha beta gamma",
		"c.go": "alpha beta gamma",
	}
	idx := buildBM25Index(contents)
	results := idx.score([]string{"alpha"}, 2)
	if len(results) != 2 {
		t.Errorf("expected 2 results (topK), got %d", len(results))
	}
}

func TestCodeSearchTool(t *testing.T) {
	// Create a temp directory with test files
	dir := t.TempDir()

	files := map[string]string{
		"auth.go":     "package auth\n\nfunc Authenticate(username, password string) (string, error) {\n\t// verify user credentials\n\treturn token, nil\n}",
		"database.go": "package db\n\nfunc Connect(dsn string) (*Pool, error) {\n\treturn pool, nil\n}",
		"server.go":   "package server\n\nfunc ListenAndServe(addr string) {\n\t// start http server\n}",
	}

	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	tool := CodeSearch{}
	input := []byte(`{"query": "authentication login password", "path": "` + dir + `", "max_results": 3}`)
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result: %s", result.Content)
	}

	// auth.go should be in the results and ranked highly
	if !csContains(result.Content, "auth.go") {
		t.Errorf("expected auth.go in results, got: %s", result.Content)
	}
}

func TestCodeSearchEmptyQuery(t *testing.T) {
	tool := CodeSearch{}
	input := []byte(`{"query": ""}`)
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error for empty query")
	}
}

func TestCodeSearchNoFiles(t *testing.T) {
	dir := t.TempDir()
	tool := CodeSearch{}
	input := []byte(`{"query": "anything", "path": "` + dir + `"}`)
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Content == "" {
		t.Error("expected non-empty content")
	}
}

func TestCodeSearchTypeFilter(t *testing.T) {
	dir := t.TempDir()

	// Create a .go file and a .txt file with same content
	content := "authentication password token login"
	os.WriteFile(filepath.Join(dir, "auth.go"), []byte(content), 0644)
	os.WriteFile(filepath.Join(dir, "auth.txt"), []byte(content), 0644)

	tool := CodeSearch{}
	input := []byte(`{"query": "authentication", "path": "` + dir + `", "type": "go"}`)
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should only find the .go file
	if !csContains(result.Content, "auth.go") {
		t.Errorf("expected auth.go in results: %s", result.Content)
	}
	if csContains(result.Content, "auth.txt") {
		t.Errorf("auth.txt should be filtered out: %s", result.Content)
	}
}

func TestCodeSearchLargeFileSkipped(t *testing.T) {
	dir := t.TempDir()

	// Create a file larger than 256KB
	largeContent := make([]byte, 257*1024)
	for i := range largeContent {
		largeContent[i] = 'x'
	}
	os.WriteFile(filepath.Join(dir, "large.go"), largeContent, 0644)

	// Create a normal file
	os.WriteFile(filepath.Join(dir, "small.go"), []byte("authentication password"), 0644)

	tool := CodeSearch{}
	input := []byte(`{"query": "authentication", "path": "` + dir + `"}`)
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// large.go should be skipped, small.go should be found
	if !csContains(result.Content, "small.go") {
		t.Errorf("expected small.go in results: %s", result.Content)
	}
}

func csContains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && csContainsStr(s, substr))
}

func csContainsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
