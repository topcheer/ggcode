package auth

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

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
	remaining, err := filepath.Glob(filepath.Join(dir, "provider-x.json.tmp-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) > 0 {
		t.Errorf("scratch files leaked after successful saves: %v", remaining)
	}
}
