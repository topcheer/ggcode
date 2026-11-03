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

	termSet := make(map[string]bool)
	for _, term := range terms {
		termSet[term] = true
	}

	if !termSet["handle"] {
		t.Errorf("expected 'handle' in tokens, got %v", terms)
	}
	if !termSet["http"] {
		t.Errorf("expected 'http' in tokens, got %v", terms)
	}

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

	if idx.df["authenticate"] != 2 {
		t.Errorf("expected df[authenticate]=2, got %d", idx.df["authenticate"])
	}

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

	topPaths := make(map[string]bool)
	for _, r := range results[:min(2, len(results))] {
		topPaths[r.path] = true
	}
	if !topPaths["auth.go"] {
		t.Errorf("expected auth.go in top 2, got first: %s", results[0].path)
	}

	for _, r := range results {
		if r.path == "database.go" {
			t.Error("database.go should not appear in results")
		}
	}

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

	content := "authentication password token login"
	os.WriteFile(filepath.Join(dir, "auth.go"), []byte(content), 0644)
	os.WriteFile(filepath.Join(dir, "auth.txt"), []byte(content), 0644)

	tool := CodeSearch{}
	input := []byte(`{"query": "authentication", "path": "` + dir + `", "type": "go"}`)
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !csContains(result.Content, "auth.go") {
		t.Errorf("expected auth.go in results: %s", result.Content)
	}
	if csContains(result.Content, "auth.txt") {
		t.Errorf("auth.txt should be filtered out: %s", result.Content)
	}
}

func TestCodeSearchLargeFileSkipped(t *testing.T) {
	dir := t.TempDir()

	largeContent := make([]byte, 257*1024)
	for i := range largeContent {
		largeContent[i] = 'x'
	}
	os.WriteFile(filepath.Join(dir, "large.go"), largeContent, 0644)

	os.WriteFile(filepath.Join(dir, "small.go"), []byte("authentication password"), 0644)

	tool := CodeSearch{}
	input := []byte(`{"query": "authentication", "path": "` + dir + `"}`)
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

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

func TestExpandTerm(t *testing.T) {

	variants := expandTerm("auth")
	termSet := make(map[string]bool)
	for _, v := range variants {
		termSet[v] = true
	}

	if !termSet["authentication"] {
		t.Errorf("expected 'authentication' in expansions of 'auth', got %v", variants)
	}
	if !termSet["authenticate"] {
		t.Errorf("expected 'authenticate' in expansions of 'auth', got %v", variants)
	}
	if !termSet["authorize"] {
		t.Errorf("expected 'authorize' in expansions of 'auth', got %v", variants)
	}
}

func TestExpandTermReverse(t *testing.T) {

	variants := expandTerm("authentication")
	termSet := make(map[string]bool)
	for _, v := range variants {
		termSet[v] = true
	}
	if !termSet["auth"] {
		t.Errorf("expected 'auth' in reverse expansion of 'authentication', got %v", variants)
	}
}

func TestStem(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"authentication", "authenticate"},
		{"configs", "config"},
		{"running", "runn"},
		{"created", "creat"},
		{"config", "config"},
		{"ab", "ab"},
	}
	for _, tt := range tests {
		got := stem(tt.input)

		if len(got) > len(tt.input) {
			t.Errorf("stem(%q) = %q, should not be longer", tt.input, got)
		}
	}
}

func TestQueryExpansionFindsAbbreviatedFiles(t *testing.T) {

	contents := map[string]string{
		"auth_handler.go": "func authenticate(user string) error { return validateCredentials(user) }",
		"router.go":       "func serve http handler router listen port",
	}

	idx := buildBM25Index(contents)

	results := idx.score(tokenizeForSearch("auth"), 10)
	if len(results) == 0 {
		t.Fatal("expected results for 'auth' query with expansion")
	}
	if results[0].path != "auth_handler.go" {
		t.Errorf("expected auth_handler.go first, got %s", results[0].path)
	}
}

func TestQueryExpansionFindsExpandedFiles(t *testing.T) {

	contents := map[string]string{
		"connection.go": "func connect db pool query transaction",
		"unrelated.go":  "main entry point startup initialization",
	}

	idx := buildBM25Index(contents)

	results := idx.score(tokenizeForSearch("database"), 10)
	if len(results) == 0 {
		t.Fatal("expected results for 'database' query finding 'db' in files")
	}
	found := false
	for _, r := range results {
		if r.path == "connection.go" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected connection.go in results for 'database' query, got: %v", results)
	}
}

func TestIndexDoesNotExpand(t *testing.T) {

	contents := map[string]string{
		"file.go": "auth handler",
	}

	idx := buildBM25Index(contents)

	if _, exists := idx.df["authentication"]; exists {
		t.Error("index should not contain expansion term 'authentication'")
	}

	if _, exists := idx.df["auth"]; !exists {
		t.Error("index should contain 'auth'")
	}
}
