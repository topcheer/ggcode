package agent

import (
	"strings"
	"testing"
)

func TestCheckContextKeyMisuse_StringKey(t *testing.T) {
	src := `package main

import "context"

func handler(ctx context.Context) {
	ctx = context.WithValue(ctx, "userID", 42)
}
`
	warnings := checkContextKeyMisuse("test.go", "", src)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d: %v", len(warnings), warnings)
	}
	if !strings.Contains(warnings[0], "string literal key") {
		t.Errorf("warning should mention string literal key: %s", warnings[0])
	}
	if !strings.Contains(warnings[0], "L6") {
		t.Errorf("warning should reference line 6: %s", warnings[0])
	}
}

func TestCheckContextKeyMisuse_IntKey(t *testing.T) {
	src := `package main

import "context"

func handler(ctx context.Context) {
	ctx = context.WithValue(ctx, 1, "value")
}
`
	warnings := checkContextKeyMisuse("test.go", "", src)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d: %v", len(warnings), warnings)
	}
	if !strings.Contains(warnings[0], "int literal key") {
		t.Errorf("warning should mention int literal key: %s", warnings[0])
	}
}

func TestCheckContextKeyMisuse_CustomTypeKey_Pass(t *testing.T) {
	src := `package main

import "context"

type ctxKey int

const userIDKey ctxKey = 0

func handler(ctx context.Context) {
	ctx = context.WithValue(ctx, userIDKey, 42)
}
`
	warnings := checkContextKeyMisuse("test.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings for custom type key, got %d: %v", len(warnings), warnings)
	}
}

func TestCheckContextKeyMisuse_DeltaAware(t *testing.T) {
	oldSrc := `package main

import "context"

func handler(ctx context.Context) {
	ctx = context.WithValue(ctx, "oldKey", 1)
}
`
	newSrc := `package main

import "context"

func handler(ctx context.Context) {
	ctx = context.WithValue(ctx, "oldKey", 1)
	ctx = context.WithValue(ctx, "newKey", 2)
}
`
	warnings := checkContextKeyMisuse("test.go", oldSrc, newSrc)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 NEW warning (delta-aware), got %d: %v", len(warnings), warnings)
	}
	if !strings.Contains(warnings[0], "L7") {
		t.Errorf("should flag only the new key at line 7: %s", warnings[0])
	}
}

func TestCheckContextKeyMisuse_TestFile_Skipped(t *testing.T) {
	src := `package main

import "context"

func handler(ctx context.Context) {
	ctx = context.WithValue(ctx, "userID", 42)
}
`
	warnings := checkContextKeyMisuse("handler_test.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("test files should be skipped, got %d warnings", len(warnings))
	}
}

func TestCheckContextKeyMisuse_NonGo_Skipped(t *testing.T) {
	src := `context.WithValue(ctx, "key", val)`
	warnings := checkContextKeyMisuse("handler.py", "", src)
	if len(warnings) != 0 {
		t.Fatalf("non-Go files should be skipped, got %d warnings", len(warnings))
	}
}

func TestCheckContextKeyMisuse_MultipleWarnings(t *testing.T) {
	src := `package main

import "context"

func handler(ctx context.Context) {
	ctx = context.WithValue(ctx, "a", 1)
	ctx = context.WithValue(ctx, "b", 2)
	ctx = context.WithValue(ctx, "c", 3)
	ctx = context.WithValue(ctx, "d", 4)
	ctx = context.WithValue(ctx, "e", 5)
}
`
	warnings := checkContextKeyMisuse("test.go", "", src)
	if len(warnings) != maxCtxKeyWarnings+1 {
		t.Fatalf("expected %d warnings (cap + truncation), got %d", maxCtxKeyWarnings+1, len(warnings))
	}
	if !strings.Contains(warnings[len(warnings)-1], "more") {
		t.Errorf("last warning should mention more occurrences: %s", warnings[len(warnings)-1])
	}
}

func TestCheckContextKeyMisuse_NoWithContext_Pass(t *testing.T) {
	src := `package main

func handler() {
	x := "hello"
	_ = x
}
`
	warnings := checkContextKeyMisuse("test.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings without WithValue, got %d", len(warnings))
	}
}

func TestCheckContextKeyMisuse_EmptyContent(t *testing.T) {
	warnings := checkContextKeyMisuse("test.go", "", "")
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings for empty content, got %d", len(warnings))
	}
}

func TestCheckContextKeyMisuse_FloatKey(t *testing.T) {
	src := `package main

import "context"

func handler(ctx context.Context) {
	ctx = context.WithValue(ctx, 3.14, "pi")
}
`
	warnings := checkContextKeyMisuse("test.go", "", src)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning for float key, got %d: %v", len(warnings), warnings)
	}
	if !strings.Contains(warnings[0], "float literal key") {
		t.Errorf("warning should mention float literal key: %s", warnings[0])
	}
}

func TestCheckContextKeyMisuse_ConstRefKey_Pass(t *testing.T) {
	src := `package main

import "context"

const myKey = "userID"

func handler(ctx context.Context) {
	ctx = context.WithValue(ctx, myKey, 42)
}
`
	// A variable reference (even a string const) is not a BasicLit, so it
	// passes - the check only catches inline literal keys.
	warnings := checkContextKeyMisuse("test.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings for variable key reference, got %d: %v", len(warnings), warnings)
	}
}

func TestCheckContextKeyMisuse_AliasedContext(t *testing.T) {
	src := `package main

import (
	ctx2 "context"
)

func handler(ctx ctx2.Context) {
	ctx = ctx2.WithValue(ctx, "userID", 42)
}
`
	// Aliased context package: sel.X is ctx2, not "context".
	// This should NOT trigger because we only check literal "context".
	warnings := checkContextKeyMisuse("test.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("aliased context should not trigger (only 'context' prefix), got %d", len(warnings))
	}
}
