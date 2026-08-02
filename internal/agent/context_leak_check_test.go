package agent

import (
	"strings"
	"testing"
)

func TestCheckContextLeak_TODO(t *testing.T) {
	old := `package main

import "context"

func handler(w http.ResponseWriter, r *http.Request) {
	doWork(context.TODO())
}
`
	new := `package main

import "context"

func handler(ctx context.Context, r *http.Request) {
	doWork(context.TODO())
}
`
	result := checkContextLeak("test.go", old, new)
	if result == "" {
		t.Fatal("expected context leak warning for context.TODO() in function with ctx param")
	}
	if !strings.Contains(result, "TODO") {
		t.Errorf("expected 'TODO' in warning, got: %s", result)
	}
	if !strings.Contains(result, "ctx") {
		t.Errorf("expected 'ctx' in warning, got: %s", result)
	}
}

func TestCheckContextLeak_Background(t *testing.T) {
	old := `package main

import "context"

func process(req *Request) {
	run(context.Background())
}
`
	new := `package main

import "context"

func process(ctx context.Context, req *Request) {
	run(context.Background())
}
`
	result := checkContextLeak("test.go", old, new)
	if result == "" {
		t.Fatal("expected context leak warning for context.Background()")
	}
	if !strings.Contains(result, "Background") {
		t.Errorf("expected 'Background' in warning, got: %s", result)
	}
}

func TestCheckContextLeak_NoCtxParam(t *testing.T) {
	// Function without ctx param should not trigger.
	new := `package main

import "context"

func start() {
	doWork(context.Background())
}
`
	result := checkContextLeak("test.go", "", new)
	if result != "" {
		t.Errorf("expected no warning for function without ctx param, got: %s", result)
	}
}

func TestCheckContextLeak_CtxPropagated(t *testing.T) {
	// context.WithTimeout(ctx, ...) is fine - ctx is propagated.
	new := `package main

import (
	"context"
	"time"
)

func handler(ctx context.Context) {
	ctx2, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	doWork(ctx2)
}
`
	result := checkContextLeak("test.go", "", new)
	if result != "" {
		t.Errorf("expected no warning when ctx is propagated, got: %s", result)
	}
}

func TestCheckContextLeak_DeltaAware(t *testing.T) {
	// Pre-existing context.TODO should NOT be flagged.
	old := `package main

import "context"

func handler(ctx context.Context) {
	doWork(context.TODO())
}
`
	new := old // unchanged
	result := checkContextLeak("test.go", old, new)
	if result != "" {
		t.Errorf("expected no warning for pre-existing context.TODO, got: %s", result)
	}
}

func TestCheckContextLeak_NewLeakInExistingFunc(t *testing.T) {
	// New context.TODO introduced in a function that already had ctx param.
	old := `package main

import "context"

func handler(ctx context.Context) {
	doWork(ctx)
}
`
	new := `package main

import "context"

func handler(ctx context.Context) {
	doWork(ctx)
	otherWork(context.TODO())
}
`
	result := checkContextLeak("test.go", old, new)
	if result == "" {
		t.Fatal("expected context leak warning for newly introduced context.TODO()")
	}
}

func TestCheckContextLeak_NonGoFile(t *testing.T) {
	result := checkContextLeak("test.py", "", "some content")
	if result != "" {
		t.Errorf("expected empty result for non-Go file, got: %s", result)
	}
}

func TestCheckContextLeak_MethodReceiver(t *testing.T) {
	// Method with ctx param should also be detected.
	new := `package main

import "context"

type Server struct{}

func (s *Server) Handle(ctx context.Context, req *Request) {
	s.process(context.Background())
}
`
	result := checkContextLeak("test.go", "", new)
	if result == "" {
		t.Fatal("expected context leak warning in method")
	}
}

func TestCheckContextLeak_EmptyContent(t *testing.T) {
	result := checkContextLeak("test.go", "", "")
	if result != "" {
		t.Errorf("expected empty result for empty content, got: %s", result)
	}
}

func TestCheckContextLeak_SyntaxError(t *testing.T) {
	// File with syntax errors should not crash.
	new := `package main

import "context"

func handler(ctx context.Context {
	doWork(context.TODO())
}
`
	result := checkContextLeak("test.go", "", new)
	if result != "" {
		t.Errorf("expected empty result for file with syntax errors, got: %s", result)
	}
}

func TestCheckContextLeak_MultipleLeaks(t *testing.T) {
	new := `package main

import "context"

func handler(ctx context.Context) {
	a(context.TODO())
	b(context.Background())
}
`
	result := checkContextLeak("test.go", "", new)
	if result == "" {
		t.Fatal("expected context leak warnings")
	}
	// Should mention both TODO and Background.
	if !strings.Contains(result, "TODO") {
		t.Errorf("expected TODO in warnings, got: %s", result)
	}
	if !strings.Contains(result, "Background") {
		t.Errorf("expected Background in warnings, got: %s", result)
	}
}
