package tool

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

// r86: tests for tools.subquery - Recursive Language Model sub-queries
// inside the code_execution sandbox (arXiv:2512.24601).

func TestSubQuery_NotBoundWhenFnNil(t *testing.T) {
	ce := &CodeExecution{Registry: NewRegistry()}
	result, err := ce.Execute(context.Background(), json.RawMessage(
		`{"code": "console.log(typeof tools.subquery);"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result: %s", result.Content)
	}
	if !strings.Contains(result.Content, "undefined") {
		t.Errorf("expected tools.subquery to be absent when SubQueryFn is nil, got: %s", result.Content)
	}
}

func TestSubQuery_SuccessComposesPrompt(t *testing.T) {
	var gotPrompt string
	fn := func(ctx context.Context, prompt string) (string, error) {
		gotPrompt = prompt
		return "ANSWER: line 42 has the error", nil
	}
	ce := &CodeExecution{Registry: NewRegistry(), SubQueryFn: fn}
	code := `const ctx = "ERROR line 42: disk full\nline 43: ok";
console.log(await tools.subquery("which line has the error?", ctx));`
	result, err := ce.Execute(context.Background(), json.RawMessage(`{"code": `+quoteJS(code)+`}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result: %s", result.Content)
	}
	if !strings.Contains(result.Content, "ANSWER: line 42 has the error") {
		t.Errorf("expected subquery answer in output, got: %s", result.Content)
	}
	for _, want := range []string{"line 43: ok", "which line has the error?", "<context>"} {
		if !strings.Contains(gotPrompt, want) {
			t.Errorf("composed prompt missing %q", want)
		}
	}
	// Tool-call log should record the subquery call.
	if !strings.Contains(result.Content, "subquery#1") {
		t.Errorf("expected subquery#1 in tool call log, got: %s", result.Content)
	}
}

func TestSubQuery_BudgetCap(t *testing.T) {
	var calls int32
	fn := func(ctx context.Context, prompt string) (string, error) {
		atomic.AddInt32(&calls, 1)
		return "ok", nil
	}
	ce := &CodeExecution{Registry: NewRegistry(), SubQueryFn: fn}
	code := `for (let i = 0; i < ` + strconv.Itoa(maxSubQueriesPerRun+2) + `; i++) {
  try { await tools.subquery("task " + i, "ctx"); }
  catch (e) { console.log("call " + i + " rejected: " + e); break; }
}`
	result, err := ce.Execute(context.Background(), json.RawMessage(`{"code": `+quoteJS(code)+`}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result: %s", result.Content)
	}
	if got := atomic.LoadInt32(&calls); got != int32(maxSubQueriesPerRun) {
		t.Errorf("expected exactly %d sub-LLM calls, got %d", maxSubQueriesPerRun, got)
	}
	if !strings.Contains(result.Content, "budget exceeded") {
		t.Errorf("expected budget-exceeded rejection in output, got: %s", result.Content)
	}
}

func TestSubQuery_ContextTruncationMarked(t *testing.T) {
	var gotLen int
	var marked bool
	fn := func(ctx context.Context, prompt string) (string, error) {
		gotLen = len(prompt)
		marked = strings.Contains(prompt, "[context truncated:")
		return "ok", nil
	}
	ce := &CodeExecution{Registry: NewRegistry(), SubQueryFn: fn}
	// Build a context far beyond the 64KB cap in JS.
	code := `const ctx = "x".repeat(200 * 1024);
await tools.subquery("summarize", ctx);`
	result, err := ce.Execute(context.Background(), json.RawMessage(`{"code": `+quoteJS(code)+`}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result: %s", result.Content)
	}
	if !marked {
		t.Error("expected truncation marker in composed prompt")
	}
	// Prompt = framing + capped context + marker; well under 64KB + slack.
	if gotLen > maxSubQueryContextBytes+4096 {
		t.Errorf("composed prompt too large: %d bytes", gotLen)
	}
}

func TestSubQuery_ProviderErrorPropagates(t *testing.T) {
	fn := func(ctx context.Context, prompt string) (string, error) {
		return "", context.DeadlineExceeded
	}
	ce := &CodeExecution{Registry: NewRegistry(), SubQueryFn: fn}
	code := `try { await tools.subquery("t", "c"); console.log("NO-ERROR"); }
catch (e) { console.log("caught: " + e); }`
	result, err := ce.Execute(context.Background(), json.RawMessage(`{"code": `+quoteJS(code)+`}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result: %s", result.Content)
	}
	if !strings.Contains(result.Content, "caught: subquery failed") {
		t.Errorf("expected rejection catchable in JS, got: %s", result.Content)
	}
	if strings.Contains(result.Content, "NO-ERROR") {
		t.Error("subquery failure must not resolve")
	}
}

func TestSubQuery_EmptyArgsRejected(t *testing.T) {
	fn := func(ctx context.Context, prompt string) (string, error) { return "x", nil }
	ce := &CodeExecution{Registry: NewRegistry(), SubQueryFn: fn}
	for _, tc := range []struct {
		name string
		code string
	}{
		{"empty prompt", `await tools.subquery("  ", "ctx");`},
		{"empty context", `await tools.subquery("task", "");`},
		{"missing args", `await tools.subquery("task");`},
	} {
		code := "try { " + tc.code + " console.log('NO'); } catch (e) { console.log('rejected'); }"
		result, err := ce.Execute(context.Background(), json.RawMessage(
			`{"code": `+quoteJS(code)+`}`))
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", tc.name, err)
		}
		if result.IsError {
			t.Fatalf("%s: unexpected error result: %s", tc.name, result.Content)
		}
		if !strings.Contains(result.Content, "rejected") {
			t.Errorf("%s: expected rejection, got: %s", tc.name, result.Content)
		}
	}
}

func TestSubQuery_SetSubQueryFnNilNoop(t *testing.T) {
	ce := &CodeExecution{Registry: NewRegistry()}
	fn := func(ctx context.Context, prompt string) (string, error) { return "a", nil }
	ce.SetSubQueryFn(fn)
	ce.SetSubQueryFn(nil) // must be a no-op, not a clear
	result, err := ce.Execute(context.Background(), json.RawMessage(
		`{"code": "console.log(typeof tools.subquery);"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Content, "function") {
		t.Errorf("expected subquery still bound after nil set, got: %s", result.Content)
	}
}

// quoteJS embeds s as a JSON string literal (for building test code args).
func quoteJS(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func jsonNumber(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}
