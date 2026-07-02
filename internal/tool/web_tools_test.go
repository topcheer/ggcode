package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// --- web_fetch tests ---

func TestWebFetchDescriptionClarifiesNonInteractiveUse(t *testing.T) {
	desc := WebFetch{}.Description()
	for _, want := range []string{"does not summarize or transform", "interactive or login-required", "browser automation"} {
		if !strings.Contains(desc, want) {
			t.Fatalf("web_fetch description should mention %q, got %q", want, desc)
		}
	}
}

func TestWebFetch_Success(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "<html><body><h1>Hello</h1><p>World</p></body></html>")
	}))
	defer ts.Close()

	wf := WebFetch{AllowPrivate: true}
	input := json.RawMessage(fmt.Sprintf(`{"url": "%s"}`, ts.URL))
	result, err := wf.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", result.Content)
	}
	if result.Content != "Hello World" {
		t.Errorf("expected 'Hello World', got %q", result.Content)
	}
}

func TestWebFetch_InvalidURL(t *testing.T) {
	wf := WebFetch{AllowPrivate: true}
	result, err := wf.Execute(context.Background(), json.RawMessage(`{"url": "not a url"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error for invalid URL")
	}
}

func TestWebFetch_HTTPError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	wf := WebFetch{AllowPrivate: true}
	input := json.RawMessage(fmt.Sprintf(`{"url": "%s"}`, ts.URL))
	result, err := wf.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error for 404")
	}
}

func TestWebFetch_MissingURL(t *testing.T) {
	wf := WebFetch{AllowPrivate: true}
	result, err := wf.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error for missing url")
	}
}

func TestWebFetch_Truncation(t *testing.T) {
	longContent := "<p>" + string(make([]byte, 60000)) + "</p>"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, longContent)
	}))
	defer ts.Close()

	wf := WebFetch{AllowPrivate: true}
	input := json.RawMessage(fmt.Sprintf(`{"url": "%s"}`, ts.URL))
	result, err := wf.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", result.Content)
	}
	if len(result.Content) > 51000 {
		t.Errorf("expected truncation, got length %d", len(result.Content))
	}
}

func TestWebFetch_PrivateIPBlocked(t *testing.T) {
	wf := WebFetch{AllowPrivate: false}

	privateURLs := []string{
		"http://127.0.0.1:8080",
		"http://192.168.1.1",
		"http://10.0.0.1",
		"http://172.16.0.1",
		"http://169.254.169.254",
		"http://[::1]",
	}

	for _, url := range privateURLs {
		t.Run(url, func(t *testing.T) {
			input := json.RawMessage(fmt.Sprintf(`{"url": %q}`, url))
			result, err := wf.Execute(context.Background(), input)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !result.IsError {
				t.Errorf("expected error for private URL %s, got: %s", url, result.Content)
			}
		})
	}
}

func TestResolvePublicDialAddress_EmptyLookup(t *testing.T) {
	_, err := resolvePublicDialAddress(context.Background(), "example.com", "443", func(context.Context, string) ([]net.IPAddr, error) {
		return nil, nil
	})
	if err == nil || !strings.Contains(err.Error(), "no IP addresses found") {
		t.Fatalf("expected no-IP error, got %v", err)
	}
}

func TestResolvePublicDialAddress_PrivateIPBlocked(t *testing.T) {
	_, err := resolvePublicDialAddress(context.Background(), "example.com", "443", func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
	})
	if err == nil || !strings.Contains(err.Error(), "private/internal IP") {
		t.Fatalf("expected private IP error, got %v", err)
	}
}

func TestParsePrivateNetworks_InvalidCIDR(t *testing.T) {
	_, err := parsePrivateNetworks([]string{"not-a-cidr"})
	if err == nil {
		t.Fatal("expected parse error")
	}
}

func TestStripHTML(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"<p>Hello</p>", "Hello"},
		{"<h1>Title</h1><p>Body</p>", "Title Body"},
		{"A &amp; B &lt; C", "A & B < C"},
		{"<script>alert('x')</script>Text", "Text"},
	}
	for _, tc := range tests {
		got := stripHTML(tc.input)
		if got != tc.want {
			t.Errorf("stripHTML(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestWebFetch_PromptIsPrependedNotExecuted(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "<html><body><h1>Hello</h1><p>World</p></body></html>")
	}))
	defer ts.Close()

	wf := WebFetch{AllowPrivate: true}
	input := json.RawMessage(fmt.Sprintf(`{"url": %q, "prompt": "Return only title"}`, ts.URL))
	result, err := wf.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", result.Content)
	}
	if !strings.Contains(result.Content, "Prompt: Return only title") || !strings.Contains(result.Content, "Hello World") {
		t.Fatalf("expected prompt to be prepended to raw fetched text, got %q", result.Content)
	}
}

func TestWebToolDescriptions_ClarifyLLMResponsibilities(t *testing.T) {
	if !strings.Contains(WebFetch{}.Description(), "does not summarize") {
		t.Fatalf("web_fetch description should clarify prompt is not executed, got %q", WebFetch{}.Description())
	}
	if !strings.Contains(WebSearch{}.Description(), "not full page contents") || !strings.Contains(WebSearch{}.Description(), "web_fetch") {
		t.Fatalf("web_search description should direct full-page reads to web_fetch, got %q", WebSearch{}.Description())
	}
}

// --- web_search tests ---

func TestWebSearch_InvalidInput(t *testing.T) {
	ws := WebSearch{}
	result, err := ws.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error for missing query")
	}
}

func TestWebSearch_DDGMock(t *testing.T) {
	html := `<div class="result">
<a class="result__a" href="https://example.com">Example Domain</a>
<a class="result__snippet">This domain is for use in illustrative examples</a>
</div>
<div class="result">
<a class="result__a" href="https://golang.org">Go Programming Language</a>
<a class="result__snippet">An open-source programming language</a>
</div>`

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, html)
	}))
	defer ts.Close()

	// We can't easily redirect to the test server, so test the parser directly
	results := parseDDGResults(html, 2)
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].Title != "Example Domain" {
		t.Errorf("expected 'Example Domain', got %q", results[0].Title)
	}
	if results[0].URL != "https://example.com" {
		t.Errorf("expected 'https://example.com', got %q", results[0].URL)
	}
	if results[0].Snippet != "This domain is for use in illustrative examples" {
		t.Errorf("unexpected snippet: %q", results[0].Snippet)
	}
}

func TestWebSearch_DDGRedirectURLNormalization(t *testing.T) {
	html := `<div class="result">
	<a class="result__a" href="/l/?uddg=https%3A%2F%2Fexample.com%2Fdocs%3Fa%3D1%26b%3D2">Example Docs</a>
	<a class="result__snippet">Example snippet</a>
	</div>`

	results := parseDDGResults(html, 1)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].URL != "https://example.com/docs?a=1&b=2" {
		t.Fatalf("expected normalized uddg target URL, got %q", results[0].URL)
	}
}

func TestWebSearchDomainFiltersMatchSubdomains(t *testing.T) {
	results := []searchResult{
		{Title: "Docs", URL: "https://docs.example.com/page"},
		{Title: "API", URL: "https://api.example.com/page"},
		{Title: "Other", URL: "https://other.test/page"},
	}

	allowed := filterByDomain(results, []string{"https://example.com/"}, nil)
	if len(allowed) != 2 || allowed[0].Title != "Docs" || allowed[1].Title != "API" {
		t.Fatalf("expected example.com and subdomains to be allowed, got %+v", allowed)
	}

	blocked := filterByDomain(results, nil, []string{"example.com"})
	if len(blocked) != 1 || blocked[0].Title != "Other" {
		t.Fatalf("expected example.com and subdomains to be blocked, got %+v", blocked)
	}
}

// --- todo_write tests ---

func TestTodoWrite_Basic(t *testing.T) {
	withTestHome(t)
	sessionID := "test-session-basic"
	tw := NewTodoWrite(sessionID)

	input := json.RawMessage(`{
		"todos": [
			{"id": "1", "content": "Write tests", "status": "done"},
			{"id": "2", "content": "Write code", "status": "in_progress"},
			{"id": "3", "content": "Deploy", "status": "pending"}
		]
	}`)
	result, err := tw.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", result.Content)
	}

	// Verify file
	data, err := os.ReadFile(TodoFilePath(sessionID))
	if err != nil {
		t.Fatalf("failed to read todo file: %v", err)
	}
	var todos []Todo
	if err := json.Unmarshal(data, &todos); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	if len(todos) != 3 {
		t.Fatalf("expected 3 todos, got %d", len(todos))
	}
	if todos[0].Status != "done" {
		t.Errorf("expected status 'done', got %q", todos[0].Status)
	}

	// Verify summary
	if result.Content == "" {
		t.Error("expected non-empty summary")
	}
}

func TestTodoWrite_ListTodos(t *testing.T) {
	withTestHome(t)
	sessionID := "test-session-list"
	tw := NewTodoWrite(sessionID)

	input := json.RawMessage(`{"todos": [{"id": "1", "content": "Task 1", "status": "pending"}]}`)
	_, err := tw.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}

	todos, err := tw.ListTodos()
	if err != nil {
		t.Fatalf("ListTodos failed: %v", err)
	}
	if len(todos) != 1 || todos[0].ID != "1" {
		t.Errorf("unexpected todos: %+v", todos)
	}
}

func TestTodoWrite_EmptyList(t *testing.T) {
	withTestHome(t)
	sessionID := "test-session-empty"
	tw := NewTodoWrite(sessionID)

	todos, err := tw.ListTodos()
	if err != nil {
		t.Fatalf("ListTodos failed: %v", err)
	}
	if todos != nil {
		t.Errorf("expected nil for missing file, got %+v", todos)
	}
}

func TestTodoWrite_ClearsFileOnEmptyUpdate(t *testing.T) {
	withTestHome(t)
	sessionID := "test-session-clear"
	tw := NewTodoWrite(sessionID)

	if _, err := tw.Execute(context.Background(), json.RawMessage(`{"todos":[{"id":"1","content":"Task 1","status":"pending"}]}`)); err != nil {
		t.Fatalf("seed execute failed: %v", err)
	}
	if _, err := tw.Execute(context.Background(), json.RawMessage(`{"todos":[]}`)); err != nil {
		t.Fatalf("clear execute failed: %v", err)
	}
	if _, err := os.Stat(TodoFilePath(sessionID)); !os.IsNotExist(err) {
		t.Fatalf("expected todo file to be removed, err=%v", err)
	}
}

func TestTodoWrite_InvalidStatus(t *testing.T) {
	withTestHome(t)
	sessionID := "test-session-invalid"
	tw := NewTodoWrite(sessionID)

	input := json.RawMessage(`{"todos": [{"id": "1", "content": "Bad", "status": "invalid"}]}`)
	result, err := tw.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error for invalid status")
	}
}

func TestTodoWrite_MissingID(t *testing.T) {
	withTestHome(t)
	sessionID := "test-session-missing"
	tw := NewTodoWrite(sessionID)

	input := json.RawMessage(`{"todos": [{"content": "No ID", "status": "pending"}]}`)
	result, err := tw.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error for missing id")
	}
}

func TestTodoWrite_RejectsDuplicateIDs(t *testing.T) {
	withTestHome(t)
	sessionID := "test-session-dup"
	tw := NewTodoWrite(sessionID)

	input := json.RawMessage(`{"todos":[{"id":"1","content":"Task 1","status":"pending"},{"id":"1","content":"Task 2","status":"done"}]}`)
	result, err := tw.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected duplicate id error")
	}
}

func TestTodoWrite_AllowsMultipleInProgress(t *testing.T) {
	withTestHome(t)
	sessionID := "test-session-multi"
	tw := NewTodoWrite(sessionID)

	input := json.RawMessage(`{"todos":[{"id":"1","content":"Task 1","status":"in_progress"},{"id":"2","content":"Task 2","status":"in_progress"}]}`)
	result, err := tw.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("multiple in_progress should be allowed, got error: %s", result.Content)
	}
}
