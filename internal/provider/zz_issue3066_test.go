package provider

// Regression probes for #3066 (internal/provider, 4-point package):
//   V1: CloneWithModel dropped samplingOverride (atomic.Pointer zero-state
//       copy trap, same as contextEditing #2567) - clones silently lost
//       MaxTokens/StopSequences/Temperature overrides.
//   V2: the files cache never expired - a file_id whose server-side file
//       had been deleted 400'd every /v1/messages request with no eviction
//       path (noteFailure only classifies upload errors).
//   V3: noteFailure treated any non-429 4xx as endpoint-dead - a single
//       413/400 image disabled Files API for the provider lifetime.
//   V4: OnRejected stepped cur below lo when parsedLimit<=0 and lo==cur,
//       breaking the documented cur-in-[lo,hi] invariant.

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"testing"
	"time"

	anthropic "github.com/anthropics/anthropic-sdk-go"
)

// V1: a clone must carry the parent's sampling override through.
func TestIssue3066_CloneWithModelKeepsSamplingOverride(t *testing.T) {
	p := &AnthropicProvider{}
	p.SetSamplingOverride(&SamplingOverride{MaxTokens: 1234, StopSequences: []string{"END"}, Temperature: 0.7})

	clone, ok := p.CloneWithModel("claude-other").(*AnthropicProvider)
	if !ok {
		t.Fatal("clone is not *AnthropicProvider")
	}
	ov := clone.SamplingOverride()
	if ov == nil {
		t.Fatal("clone dropped samplingOverride (V1): nil after clone")
	}
	if ov.MaxTokens != 1234 || len(ov.StopSequences) != 1 || ov.StopSequences[0] != "END" || ov.Temperature != 0.7 {
		t.Fatalf("clone samplingOverride corrupted: %+v", ov)
	}
	// Parent must be untouched.
	if pov := p.SamplingOverride(); pov == nil || pov.MaxTokens != 1234 {
		t.Fatal("clone leaked back into parent override")
	}
}

// V2: an expired cache entry must NOT be served and must be evicted so the
// next resolve re-uploads (observed here as a cache miss that attempts
// upload, which fails without network and leaves the cache empty).
func TestIssue3066_FilesCacheEntryExpires(t *testing.T) {
	u := newFileUploader(&anthropic.Client{}, "")
	data, _ := base64.StdEncoding.DecodeString(testTinyPNG)
	sum := sha256.Sum256(data)
	key := hex.EncodeToString(sum[:])

	// Inject an entry uploaded beyond the TTL.
	u.mu.Lock()
	u.cache[key] = filesCacheEntry{fileID: "file_stale", uploadedAt: time.Now().Add(-filesCacheTTL - time.Minute)}
	u.mu.Unlock()

	if id, ok := u.resolve(context.Background(), "image/png", data); ok {
		t.Fatalf("expired cache entry served: file_id %q (V2)", id)
	}
	u.mu.Lock()
	n := len(u.cache)
	u.mu.Unlock()
	if n != 0 {
		t.Fatalf("expired entry not evicted: cache size %d (V2)", n)
	}

	// A fresh entry still hits: expiry does not break the dedupe path.
	u.mu.Lock()
	u.cache[key] = filesCacheEntry{fileID: "file_fresh", uploadedAt: time.Now()}
	u.mu.Unlock()
	if id, ok := u.resolve(context.Background(), "image/png", data); !ok || id != "file_fresh" {
		t.Fatalf("fresh cache entry missed: (%q, %v)", id, ok)
	}
}

// V3: 413 is per-image, not endpoint-fatal. 404 remains endpoint-fatal.
func TestIssue3066_NoteFailure413PerHashNotEndpoint(t *testing.T) {
	u := newFileUploader(&anthropic.Client{}, "")
	u.noteFailure("k1", &anthropic.Error{StatusCode: 413})

	u.mu.Lock()
	broken := u.endpointBroken
	failedN := len(u.failed)
	u.mu.Unlock()
	if broken {
		t.Fatal("413 marked endpoint broken for provider lifetime (V3)")
	}
	if failedN != 1 {
		t.Fatalf("413 not recorded as per-hash transient: failed=%d (V3)", failedN)
	}

	// A different content hash is unaffected: resolve attempts the upload
	// (fails without network) rather than short-circuiting on endpointBroken.
	other := []byte("zz-other-content-bytes")
	if _, ok := u.resolve(context.Background(), "image/png", other); ok {
		t.Fatal("unexpected upload success without network")
	}
	u.mu.Lock()
	broken = u.endpointBroken
	u.mu.Unlock()
	if broken {
		t.Fatal("resolve path treated 413-poisoned uploader as endpoint-dead (V3)")
	}
}

// V3 companion: 404 on the first upload stays permanent (capability signal).
func TestIssue3066_NoteFailure404Permanent(t *testing.T) {
	u := newFileUploader(&anthropic.Client{}, "")
	u.noteFailure("k1", &anthropic.Error{StatusCode: 404})
	u.mu.Lock()
	broken := u.endpointBroken
	u.mu.Unlock()
	if !broken {
		t.Fatal("404 (no /v1/files route) must stay permanent (V3)")
	}
}

// V3 companion: 400 stays permanent only before any successful upload.
func TestIssue3066_NoteFailure400AfterSuccessTransient(t *testing.T) {
	u := newFileUploader(&anthropic.Client{}, "")
	u.mu.Lock()
	u.uploads = 1 // a prior success makes later 400s content-level
	u.mu.Unlock()
	u.noteFailure("k2", &anthropic.Error{StatusCode: 400})
	u.mu.Lock()
	broken := u.endpointBroken
	failedN := len(u.failed)
	u.mu.Unlock()
	if broken {
		t.Fatal("content-level 400 after a success disabled the endpoint (V3)")
	}
	if failedN != 1 {
		t.Fatalf("content-level 400 not per-hash: failed=%d (V3)", failedN)
	}
}

// V4: OnRejected with parsedLimit<=0 at lo==cur must hold the invariant
// cur >= lo (previously cur dropped to lo-1).
func TestIssue3066_OnRejectedHoldsInvariant(t *testing.T) {
	c := &adaptiveCap{key: "zz-issue3066", lo: 4096}
	c.cur.Store(4096)
	c.OnRejected(0)
	if got := c.cur.Load(); got < c.lo {
		t.Fatalf("invariant broken: cur=%d < lo=%d (V4)", got, c.lo)
	}
	// The parsed-limit path must still make real downward progress.
	c2 := &adaptiveCap{key: "zz-issue3066b", lo: 100}
	c2.cur.Store(10000)
	c2.OnRejected(4096)
	if got := c2.cur.Load(); got != 4096 {
		t.Fatalf("parsed-limit path regressed: cur=%d, want 4096", got)
	}
}
