package auth

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// TestTokenCacheSaveConcurrentWritersNoCorruption pins the #1505-case-3-class
// fix in TokenCache.Save: concurrent Save calls must never interleave into a
// corrupted JSON document. The old fixed path+".tmp" scratch name let two
// writers O_TRUNC the same scratch file, mixing two JSON payloads before the
// rename; with per-writer CreateTemp scratch files every writer renames a
// complete document and the final file always parses.
func TestTokenCacheSaveConcurrentWritersNoCorruption(t *testing.T) {
	dir := t.TempDir()
	cache := NewTokenCache(dir)

	const writers = 8
	const rounds = 25
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for r := 0; r < rounds; r++ {
				token := &PKCEToken{
					AccessToken:  "tok-concurrent",
					RefreshToken: "refresh-concurrent",
					TokenType:    "bearer",
					Expiry:       time.Now().Add(time.Hour),
					Scope:        "read:user",
				}
				if err := cache.Save("provider-x", token, "client-x"); err != nil {
					t.Errorf("writer %d round %d: Save: %v", w, r, err)
					return
				}
			}
		}(w)
	}
	wg.Wait()

	// The final file must parse as exactly one valid tokenEntry - the old
	// interleaved-scratch corruption produced a mixed document here.
	data, err := os.ReadFile(filepath.Join(dir, "provider-x.json"))
	if err != nil {
		t.Fatal(err)
	}
	var entry tokenEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		t.Fatalf("cached token file is corrupted (mixed writers): %v\ncontent: %q", err, string(data))
	}
	if entry.AccessToken != "tok-concurrent" {
		t.Fatalf("unexpected access token: %q", entry.AccessToken)
	}
	// No scratch files may be left behind on the success path.
	remaining, err := filepath.Glob(filepath.Join(dir, "provider-x.json.tmp-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) > 0 {
		t.Errorf("scratch files leaked after successful saves: %v", remaining)
	}
}
