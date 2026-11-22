package agent

import (
	"strings"
	"testing"
)

// claimVerify5878OversizedBody returns a synthetic regex body one byte
// above the compile cap.
func claimVerify5878OversizedBody() string {
	return strings.Repeat("x", claimVerifyMaxRegexBody+1)
}

// TestClaimVerify5878OversizedBodyNotCompiled pins issue #5878 phase 2:
// a body larger than claimVerifyMaxRegexBody must never be handed to
// regexp.Compile, and an "re:" pattern built from such a body must never
// gain a claimVerifyReCache entry.
func TestClaimVerify5878OversizedBodyNotCompiled(t *testing.T) {
	body := claimVerify5878OversizedBody()
	if claimVerifyCompileRegex(body) != nil {
		t.Fatalf("oversized body (%d bytes) must not be compiled", len(body))
	}
	if claimVerifyCompileRegex(body[:claimVerifyMaxRegexBody]) == nil {
		t.Fatal("body exactly at the cap must still compile")
	}
	if re := claimVerifyReCache["re:"+body]; re != nil {
		t.Fatal("oversized pattern must not gain a cache entry")
	}
}

// TestClaimVerify5878OversizedBodyFallbackMatch verifies that oversized
// (never-compiled) bodies still verify correctly through the non-regex
// fallback: plain containment for unanchored patterns, line-prefix for
// anchored ones.
func TestClaimVerify5878OversizedBodyFallbackMatch(t *testing.T) {
	body := claimVerify5878OversizedBody()
	pattern := "re:" + body

	if !claimVerifyMatch("prefix "+body+" suffix", pattern) {
		t.Fatal("oversized body present in content must match via containment fallback")
	}
	if claimVerifyMatch("content without the needle", pattern) {
		t.Fatal("oversized body absent from content must not match")
	}
	if !claimVerifyMatchAnchored(body+"\nsecond line", pattern) {
		t.Fatal("anchored fallback must match a line starting with the oversized body")
	}
	if claimVerifyMatchAnchored("  indented "+body, pattern) {
		t.Fatal("anchored fallback must not match an indented oversized body")
	}
}

// TestClaimVerify5878SmallBodyUnchanged verifies the cap does not alter
// existing small `re:` pattern behavior: they stay compiled in the cache
// and match as before (#5878 must be a pure guard, no regression).
func TestClaimVerify5878SmallBodyUnchanged(t *testing.T) {
	if claimVerifyReCache["re:fail:[1-9]"] == nil {
		t.Fatal("small re: pattern must remain compiled in the cache")
	}
	if claimVerifyMatch("pass:120 fail:0 skipped:0", "re:fail:[1-9]") {
		t.Fatal("small pattern: fail:0 must not match (regression guard)")
	}
	if !claimVerifyMatch("pass:12 fail:3 skipped:0", "re:fail:[1-9]") {
		t.Fatal("small pattern: fail:3 must match (regression guard)")
	}
	if !claimVerifyMatchAnchored("--- fail:2\nok", "re:fail:[1-9]") {
		t.Fatal("small anchored pattern must still match a column-0 verdict line")
	}
	if claimVerifyMatchAnchored("prefixfail:2\nok", "re:fail:[1-9]") {
		t.Fatal("small anchored pattern must not match a body embedded in a word")
	}
}

// TestClaimVerify5878ReCacheBounded documents the retention check from
// issue #5878: the matcher cache is bounded by construction (built once
// from the static pattern list, every body within the cap), and the
// per-session claimVerifyState keeps no matchers - so there is no
// per-claim retention and no release path is needed.
func TestClaimVerify5878ReCacheBounded(t *testing.T) {
	if len(claimVerifyReCache) > 2*len(claimVerifyPatterns) {
		t.Fatalf("re cache grew beyond static patterns: %d entries for %d patterns",
			len(claimVerifyReCache), len(claimVerifyPatterns))
	}
	for key, re := range claimVerifyReCache {
		pattern := strings.TrimSuffix(key, "|anchored")
		if !strings.HasPrefix(pattern, "re:") {
			t.Fatalf("unexpected cache key %q", key)
		}
		if body := strings.TrimPrefix(pattern, "re:"); len(body) > claimVerifyMaxRegexBody {
			t.Fatalf("cached body %q exceeds cap (%d > %d)", key, len(body), claimVerifyMaxRegexBody)
		}
		if re == nil {
			t.Fatalf("nil matcher retained for key %q", key)
		}
	}
}
