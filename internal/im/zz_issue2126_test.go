package im

// #2126 regression: openPersistentCryptoStore passed the dialect string
// "sqlite3" as the database/sql driver name, but the dependency tree links
// modernc.org/sqlite (registered as "sqlite") - every open failed with
// 'sql: unknown driver "sqlite3"' and silently degraded to MemoryStore,
// rotating the Olm identity on every restart (the exact #1404-A symptom
// the store was built to fix). This test actually OPENS the store - the
// pre-existing tests only checked path/accountID derivation and never
// opened, which is why the dead code survived.

import (
	"maunium.net/go/mautrix"
	"path/filepath"
	"testing"
)

func TestOpenPersistentCryptoStoreOpens(t *testing.T) {
	// The store lives under config.ConfigDir() - isolate HOME so the test
	// never touches the real user config directory.
	t.Setenv("HOME", t.TempDir())
	// runOnce always builds the mautrix client before setupCrypto - the
	// store reads DeviceID from it.
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
	// The handle must be registered for lifecycle close on re-entry (#2126).
	a.mu.RLock()
	closer := a.cryptoDBCloser
	a.mu.RUnlock()
	if closer == nil {
		t.Fatal("crypto db handle must be registered for close on runOnce re-entry")
	}

	// A second open (reconnect re-entry) must close the first handle, not
	// leak it.
	if _, err := a.openPersistentCryptoStore(); err != nil {
		t.Fatalf("re-open failed: %v", err)
	}

	// Cleanup.
	a.mu.RLock()
	closer = a.cryptoDBCloser
	a.mu.RUnlock()
	if closer != nil {
		_ = closer.Close()
	}
}

// The DSN must be modernc-compatible: bare file: form, no sqlite3:// scheme.
func TestCryptoStoreDSNShape(t *testing.T) {
	a := &matrixAdapter{name: "dsn", userID: "@u:e.org"}
	dir := filepath.Join(t.TempDir(), "matrix-crypto")
	// Reuse the helper to confirm the path derivation does not embed the
	// old scheme (the URI itself is asserted implicitly by opening above;
	// here we pin the file naming contract used by the DSN).
	p := newCryptoDBForAdapter(dir, a.name, a.userID)
	if !filepath.IsAbs(p) || filepath.Ext(p) != ".db" {
		t.Fatalf("unexpected crypto db path shape: %q", p)
	}
}
