package provider

// Regression tests for #2568: fileUploader's transient-failure negative cache
// must expire (filesRetryTTL) instead of poisoning a content hash for the
// provider lifetime, while permanent 4xx still disables the whole endpoint.

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
)

// backdateFailed ages the failed entry for key by d, simulating the passage
// of time without waiting.
func backdateFailed(t *testing.T, u *fileUploader, key string, d time.Duration) {
	t.Helper()
	u.mu.Lock()
	u.failed[key] = time.Now().Add(-d)
	u.mu.Unlock()
}

func failedAge(t *testing.T, u *fileUploader, key string) time.Duration {
	t.Helper()
	u.mu.Lock()
	defer u.mu.Unlock()
	ts, ok := u.failed[key]
	if !ok {
		t.Fatalf("expected failed entry for %s", key)
	}
	return time.Since(ts)
}

// Transient failure whose TTL has elapsed must be retried: the second
// resolve actually re-attempts the upload (noteFailure rewrites the
// timestamp), proving the short-circuit was bypassed.
func TestIssue2568_ExpiredTransientFailureRetries(t *testing.T) {
	u := newFileUploader(&anthropic.Client{}, "")
	data, _ := base64.StdEncoding.DecodeString(testTinyPNG)
	sum := sha256.Sum256(data)
	key := hex.EncodeToString(sum[:])

	if _, ok := u.resolve(context.Background(), "image/png", data); ok {
		t.Fatal("expected first resolve to fail (no network)")
	}

	backdateFailed(t, u, key, filesRetryTTL+time.Minute)

	if _, ok := u.resolve(context.Background(), "image/png", data); ok {
		t.Fatal("expected retried resolve to fail again (no network)")
	}
	// Retry happened iff the failure timestamp is fresh, not the backdated one.
	if age := failedAge(t, u, key); age > time.Second {
		t.Fatalf("failed entry age = %v after retry, want fresh (upload re-attempted)", age)
	}
}

// Transient failure younger than the TTL must still short-circuit: the
// backdated timestamp must survive the resolve call untouched.
func TestIssue2568_FreshTransientFailureShortCircuits(t *testing.T) {
	u := newFileUploader(&anthropic.Client{}, "")
	data, _ := base64.StdEncoding.DecodeString(testTinyPNG)
	sum := sha256.Sum256(data)
	key := hex.EncodeToString(sum[:])

	if _, ok := u.resolve(context.Background(), "image/png", data); ok {
		t.Fatal("expected first resolve to fail (no network)")
	}

	backdateFailed(t, u, key, 30*time.Second) // within 2min TTL

	if _, ok := u.resolve(context.Background(), "image/png", data); ok {
		t.Fatal("expected short-circuit")
	}
	if age := failedAge(t, u, key); age < 25*time.Second {
		t.Fatalf("failed entry age = %v, want unchanged (~30s): entry was rewritten despite TTL short-circuit", age)
	}
}

// Permanent non-retryable 4xx must keep disabling the endpoint entirely
// (unchanged #2568-adjacent behavior); the failed map must stay empty.
func TestIssue2568_Permanent4xxStillBreaksEndpoint(t *testing.T) {
	u := newFileUploader(&anthropic.Client{}, "")
	u.noteFailure("k1", &anthropic.Error{StatusCode: 404})

	u.mu.Lock()
	broken := u.endpointBroken
	n := len(u.failed)
	u.mu.Unlock()
	if !broken {
		t.Fatal("expected endpointBroken=true for permanent 4xx")
	}
	if n != 0 {
		t.Fatalf("failed entries = %d, want 0 (permanent path must not poison per-hash map)", n)
	}
	// Any later resolve must be disabled regardless of hash/TTL state.
	if _, ok := u.resolve(context.Background(), "image/png", []byte("irrelevant")); ok {
		t.Fatal("resolve must be disabled once endpoint is broken")
	}
}

// Transient 429 must take the per-hash path (not endpoint-wide), keeping the
// classification semantics the fix relies on.
func TestIssue2568_Transient429IsPerHash(t *testing.T) {
	u := newFileUploader(&anthropic.Client{}, "")
	u.noteFailure("k2", &anthropic.Error{StatusCode: 429})

	u.mu.Lock()
	broken := u.endpointBroken
	n := len(u.failed)
	u.mu.Unlock()
	if broken {
		t.Fatal("429 is transient: must not set endpointBroken")
	}
	if n != 1 {
		t.Fatalf("failed entries = %d, want 1", n)
	}
}
