package main

import "testing"

// #4216: a half-set adhoc pair (base URL without model, or model without
// base URL, via flag or env) must be a hard error instead of silently
// falling back to the config-file provider. Auxiliary --llm-* values with
// no pair at all are tolerated but must not error.
func TestAdhocPartialPairRejected(t *testing.T) {
	f := &adhocLLMFlags{BaseURL: "https://relay.example/v1"}
	if err := f.checkPartialPair(); err == nil {
		t.Fatal("base-url without model must be rejected")
	}

	f = &adhocLLMFlags{Model: "gpt-5"}
	if err := f.checkPartialPair(); err == nil {
		t.Fatal("model without base-url must be rejected")
	}

	// Full pair: no error from the pair check.
	f = &adhocLLMFlags{BaseURL: "https://relay.example/v1", Model: "gpt-5"}
	if err := f.checkPartialPair(); err != nil {
		t.Fatalf("full pair must pass, got %v", err)
	}

	// Orphaned auxiliary flags (no pair at all): lenient, must not error.
	f = &adhocLLMFlags{RetryAlways: true, APIKey: "sk-x"}
	if err := f.checkPartialPair(); err != nil {
		t.Fatalf("orphaned aux flags must be tolerated, got %v", err)
	}

	// Nothing set at all: clean pass.
	f = &adhocLLMFlags{}
	if err := f.checkPartialPair(); err != nil {
		t.Fatalf("empty flags must pass, got %v", err)
	}
}
