package util

import (
	"context"
	"testing"
)

func TestSessionIDContextInheritance(t *testing.T) {
	ctx := context.Background()
	if got := SessionIDFromContext(ctx); got != "" {
		t.Fatalf("empty context must carry no session id, got %q", got)
	}
	if got := WithSessionID(ctx, ""); got != ctx {
		t.Fatal("WithSessionID with an empty id must return the context unchanged")
	}

	root := WithSessionID(ctx, "sess-root")
	if got := SessionIDFromContext(root); got != "sess-root" {
		t.Fatalf("SessionIDFromContext = %q, want sess-root", got)
	}

	// A sub-agent run derived from the parent keeps the parent's id even when
	// it tries to stamp its own: EnsureSessionID never overrides.
	child := EnsureSessionID(root, "agent-7")
	if got := SessionIDFromContext(child); got != "sess-root" {
		t.Fatalf("inherited id must win, got %q", got)
	}

	// Without an inherited id the own id is stamped.
	fresh := EnsureSessionID(ctx, "agent-7")
	if got := SessionIDFromContext(fresh); got != "agent-7" {
		t.Fatalf("EnsureSessionID must stamp when absent, got %q", got)
	}
	if got := EnsureSessionID(ctx, ""); got != ctx {
		t.Fatal("EnsureSessionID with an empty id must return the context unchanged")
	}
}
