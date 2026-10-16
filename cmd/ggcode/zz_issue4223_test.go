package main

import (
	"io"
	"os"
	"strings"
	"testing"
)

// captureAdhocStderr runs fn with os.Stderr swapped to a pipe and returns
// what was written.
func captureAdhocStderr(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stderr = w
	fn()
	w.Close()
	os.Stderr = old
	out, _ := io.ReadAll(r)
	return string(out)
}

// TestCheckPartialPairRetryAlwaysNoteTruthful (#4223): on a non-adhoc run
// the stderr diagnostics must not claim an ACTIVE retry-always setting is
// being ignored.
func TestCheckPartialPairRetryAlwaysNoteTruthful(t *testing.T) {
	t.Run("retry-only flag prints ACTIVE not ignoring", func(t *testing.T) {
		t.Setenv("GGCODE_LLM_BASE_URL", "")
		t.Setenv("GGCODE_LLM_MODEL", "")
		t.Setenv("GGCODE_LLM_API_KEY", "")
		t.Setenv("GGCODE_LLM_PROTOCOL", "")
		t.Setenv("GGCODE_LLM_CONTEXT_WINDOW", "")
		t.Setenv("GGCODE_LLM_MAX_TOKENS", "")
		t.Setenv("GGCODE_LLM_RETRY_ALWAYS", "")
		f := &adhocLLMFlags{RetryAlways: true}
		out := captureAdhocStderr(t, func() {
			if err := f.checkPartialPair(); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
		if !strings.Contains(out, "--llm-retry-always is ACTIVE") {
			t.Fatalf("expected ACTIVE note, got: %q", out)
		}
		if strings.Contains(out, "ignoring --llm-*") {
			t.Fatalf("must not print 'ignoring' when only retry-always is set: %q", out)
		}
	})

	t.Run("retry-only env prints ACTIVE not ignoring", func(t *testing.T) {
		t.Setenv("GGCODE_LLM_BASE_URL", "")
		t.Setenv("GGCODE_LLM_MODEL", "")
		t.Setenv("GGCODE_LLM_API_KEY", "")
		t.Setenv("GGCODE_LLM_PROTOCOL", "")
		t.Setenv("GGCODE_LLM_CONTEXT_WINDOW", "")
		t.Setenv("GGCODE_LLM_MAX_TOKENS", "")
		t.Setenv("GGCODE_LLM_RETRY_ALWAYS", "1")
		f := &adhocLLMFlags{}
		out := captureAdhocStderr(t, func() {
			if err := f.checkPartialPair(); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
		if !strings.Contains(out, "--llm-retry-always is ACTIVE") {
			t.Fatalf("expected ACTIVE note for env-only activation, got: %q", out)
		}
		if strings.Contains(out, "ignoring --llm-*") {
			t.Fatalf("retry-always env alone must not trigger 'ignoring' note: %q", out)
		}
	})

	t.Run("real orphan flag still prints ignoring", func(t *testing.T) {
		t.Setenv("GGCODE_LLM_BASE_URL", "")
		t.Setenv("GGCODE_LLM_MODEL", "")
		t.Setenv("GGCODE_LLM_API_KEY", "")
		t.Setenv("GGCODE_LLM_PROTOCOL", "")
		t.Setenv("GGCODE_LLM_CONTEXT_WINDOW", "")
		t.Setenv("GGCODE_LLM_MAX_TOKENS", "")
		t.Setenv("GGCODE_LLM_RETRY_ALWAYS", "")
		f := &adhocLLMFlags{APIKey: "sk-x"}
		out := captureAdhocStderr(t, func() {
			if err := f.checkPartialPair(); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
		if !strings.Contains(out, "ignoring --llm-*") {
			t.Fatalf("orphan api-key must still print the ignoring note: %q", out)
		}
	})

	t.Run("retry plus orphan prints both", func(t *testing.T) {
		t.Setenv("GGCODE_LLM_BASE_URL", "")
		t.Setenv("GGCODE_LLM_MODEL", "")
		t.Setenv("GGCODE_LLM_API_KEY", "")
		t.Setenv("GGCODE_LLM_PROTOCOL", "")
		t.Setenv("GGCODE_LLM_CONTEXT_WINDOW", "")
		t.Setenv("GGCODE_LLM_MAX_TOKENS", "")
		t.Setenv("GGCODE_LLM_RETRY_ALWAYS", "")
		f := &adhocLLMFlags{RetryAlways: true, MaxTokens: 100}
		out := captureAdhocStderr(t, func() {
			if err := f.checkPartialPair(); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
		if !strings.Contains(out, "is ACTIVE") || !strings.Contains(out, "ignoring --llm-*") {
			t.Fatalf("expected both ACTIVE and ignoring notes, got: %q", out)
		}
	})
}
