package im

import (
	"maunium.net/go/mautrix"
	"path/filepath"
	"testing"
)

func TestOpenPersistentCryptoStoreOpens(t *testing.T) {

	t.Setenv("HOME", t.TempDir())

	a := &matrixAdapter{
		name:       "e2ee-test",
		homeserver: "https://example.org",
		userID:     "@alice:example.org",
		client:     &mautrix.Client{},
	}
	store, err := a.openPersistentCryptoStore()
	if err != nil {
		t.Fatalf("persistent crypto store must open (was: driver-name mismatch -> MemoryStore fallback): %v", err)
	}
	if store == nil {
		t.Fatal("nil store without error")
	}

	a.mu.RLock()
	closer := a.cryptoDBCloser
	a.mu.RUnlock()
	if closer == nil {
		t.Fatal("crypto db handle must be registered for close on runOnce re-entry")
	}

	if _, err := a.openPersistentCryptoStore(); err != nil {
		t.Fatalf("re-open failed: %v", err)
	}

	a.mu.RLock()
	closer = a.cryptoDBCloser
	a.mu.RUnlock()
	if closer != nil {
		_ = closer.Close()
	}
}

func TestCryptoStoreDSNShape(t *testing.T) {
	a := &matrixAdapter{name: "dsn", userID: "@u:e.org"}
	dir := filepath.Join(t.TempDir(), "matrix-crypto")

	p := newCryptoDBForAdapter(dir, a.name, a.userID)
	if !filepath.IsAbs(p) || filepath.Ext(p) != ".db" {
		t.Fatalf("unexpected crypto db path shape: %q", p)
	}
}
