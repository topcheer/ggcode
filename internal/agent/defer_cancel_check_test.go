package agent

import (
	"testing"
)

func TestCheckLostCancel_NoLeak(t *testing.T) {
	src := `package main

import "context"

func good(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	_ = ctx
}
`
	warnings := checkLostCancel("test.go", "", src)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings, got %d: %v", len(warnings), warnings)
	}
}

func TestCheckLostCancel_WithCancelLeak(t *testing.T) {
	src := `package main

import "context"

func bad(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	_ = ctx
	_ = cancel
}
`
	warnings := checkLostCancel("test.go", "", src)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d: %v", len(warnings), warnings)
	}
}

func TestCheckLostCancel_WithTimeoutLeak(t *testing.T) {
	src := `package main

import (
	"context"
	"time"
)

func bad(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	_ = ctx
	_ = cancel
}
`
	warnings := checkLostCancel("test.go", "", src)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d: %v", len(warnings), warnings)
	}
}

func TestCheckLostCancel_WithDeadlineLeak(t *testing.T) {
	src := `package main

import (
	"context"
	"time"
)

func bad(ctx context.Context) {
	ctx, cancel := context.WithDeadline(ctx, time.Now())
	_ = ctx
	_ = cancel
}
`
	warnings := checkLostCancel("test.go", "", src)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d", len(warnings))
	}
}

func TestCheckLostCancel_WithCancelDeferCalled(t *testing.T) {
	src := `package main

import "context"

func good(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 0)
	defer cancel()
	_ = ctx
}
`
	warnings := checkLostCancel("test.go", "", src)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings when defer cancel() present, got %d", len(warnings))
	}
}

func TestCheckLostCancel_WithCancelDirectCall(t *testing.T) {
	src := `package main

import "context"

func good(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	if err := doSomething(ctx); err != nil {
		cancel()
		return
	}
	cancel()
	_ = ctx
}

func doSomething(context.Context) error { return nil }
`
	warnings := checkLostCancel("test.go", "", src)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings when cancel() is called directly, got %d", len(warnings))
	}
}

func TestCheckLostCancel_BlankIdentifierWithTimeout(t *testing.T) {
	src := `package main

import (
	"context"
	"time"
)

func bad(ctx context.Context) {
	ctx, _ := context.WithTimeout(ctx, 5*time.Second)
	_ = ctx
}
`
	warnings := checkLostCancel("test.go", "", src)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning for blank-id WithTimeout, got %d: %v", len(warnings), warnings)
	}
}

func TestCheckLostCancel_BlankIdentifierWithCancel(t *testing.T) {
	src := `package main

import "context"

func ok(ctx context.Context) {
	ctx, _ := context.WithCancel(ctx)
	_ = ctx
}
`
	warnings := checkLostCancel("test.go", "", src)
	if len(warnings) != 0 {
		t.Errorf("expected no warning for blank-id WithCancel (no timer), got %d", len(warnings))
	}
}

func TestCheckLostCancel_DeltaAware(t *testing.T) {
	oldSrc := `package main

import "context"

func bad(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	_ = ctx
	_ = cancel
}
`
	newSrc := oldSrc + "\n// just a comment\n"
	warnings := checkLostCancel("test.go", oldSrc, newSrc)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings for pre-existing issue (delta-aware), got %d", len(warnings))
	}
}

func TestCheckLostCancel_NonGoFile(t *testing.T) {
	src := `console.log("hello")`
	warnings := checkLostCancel("test.js", "", src)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings for non-Go file, got %d", len(warnings))
	}
}

func TestCheckLostCancel_EmptyContent(t *testing.T) {
	warnings := checkLostCancel("test.go", "", "")
	if len(warnings) != 0 {
		t.Errorf("expected no warnings for empty content, got %d", len(warnings))
	}
}

func TestCheckLostCancel_MultipleLeaks(t *testing.T) {
	src := `package main

import "context"

func bad(ctx context.Context) {
	c1, cancel1 := context.WithCancel(ctx)
	c2, cancel2 := context.WithCancel(ctx)
	c3, cancel3 := context.WithCancel(ctx)
	_ = c1
	_ = c2
	_ = c3
	_ = cancel1
	_ = cancel2
	_ = cancel3
}
`
	warnings := checkLostCancel("test.go", "", src)
	if len(warnings) != 3 {
		t.Errorf("expected 3 warnings, got %d: %v", len(warnings), warnings)
	}
}

func TestCheckLostCancel_ParseError(t *testing.T) {
	src := `package main
import "context"
func broken(ctx context.Context {
	// syntax error
}
`
	warnings := checkLostCancel("test.go", "", src)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings on parse error, got %d", len(warnings))
	}
}
