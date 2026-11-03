package provider

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
)

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

	if age := failedAge(t, u, key); age > time.Second {
		t.Fatalf("failed entry age = %v after retry, want fresh (upload re-attempted)", age)
	}
}

func TestIssue2568_FreshTransientFailureShortCircuits(t *testing.T) {
	u := newFileUploader(&anthropic.Client{}, "")
	data, _ := base64.StdEncoding.DecodeString(testTinyPNG)
	sum := sha256.Sum256(data)
	key := hex.EncodeToString(sum[:])

	if _, ok := u.resolve(context.Background(), "image/png", data); ok {
		t.Fatal("expected first resolve to fail (no network)")
	}

	backdateFailed(t, u, key, 30*time.Second)

	if _, ok := u.resolve(context.Background(), "image/png", data); ok {
		t.Fatal("expected short-circuit")
	}
	if age := failedAge(t, u, key); age < 25*time.Second {
		t.Fatalf("failed entry age = %v, want unchanged (~30s): entry was rewritten despite TTL short-circuit", age)
	}
}

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

	if _, ok := u.resolve(context.Background(), "image/png", []byte("irrelevant")); ok {
		t.Fatal("resolve must be disabled once endpoint is broken")
	}
}

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
