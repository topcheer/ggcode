package im

// #1851 case 2 regression: the matrix sync-token store is keyed by adapter
// name AND user ID (was name-only), with a one-time migration from the
// legacy name-only path so an existing token survives the upgrade.

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"maunium.net/go/mautrix"
)

func TestSyncStoreKeyedByNameAndUser(t *testing.T) {
	dir := t.TempDir()
	a := newSyncStoreForAdapter(dir, "work", "@alice:example.org")
	b := newSyncStoreForAdapter(dir, "work", "@bob:example.org")
	if a.path == b.path {
		t.Fatalf("same-named adapters with different users must get different store files: %q", a.path)
	}
	// Write through one; the other must not see it (no cross-account
	// overwrite - the #1851 case 2 split-brain).
	if err := a.SaveNextBatch(context.Background(), "@alice:example.org", "tok-alice"); err != nil {
		t.Fatal(err)
	}
	if got, _ := b.LoadNextBatch(context.Background(), "@bob:example.org"); got != "" {
		t.Fatalf("bob's store leaked alice's token: %q", got)
	}
}

func TestSyncStoreMigratesLegacyNameOnlyPath(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, "work.json")
	if err := os.WriteFile(legacy, []byte(`{"user_id":"@alice:example.org","next_batch":"kept-token"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	store := newSyncStoreForAdapter(dir, "work", "@alice:example.org")
	if store.path == legacy {
		t.Fatal("user-scoped path expected")
	}
	if got, _ := store.LoadNextBatch(context.Background(), "@alice:example.org"); got != "kept-token" {
		t.Fatalf("legacy token must survive the migration, got %q", got)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatalf("legacy file must be renamed away, stat err = %v", err)
	}
	// Second call must NOT recreate or duplicate anything.
	store2 := newSyncStoreForAdapter(dir, "work", "@alice:example.org")
	if got, _ := store2.LoadNextBatch(context.Background(), "@alice:example.org"); got != "kept-token" {
		t.Fatalf("token must remain after repeated opens, got %q", got)
	}
}

func TestSyncStoreEmptyUserFallsBackToNameOnly(t *testing.T) {
	dir := t.TempDir()
	store := newSyncStoreForAdapter(dir, "work", "")
	want := filepath.Join(dir, "work.json")
	if store.path != want {
		t.Fatalf("empty userID must keep the name-only path: got %q want %q", store.path, want)
	}
}

// #1851 case 2 sibling: the Olm crypto db must be keyed by adapter name AND
// user ID (was name-only) - two same-named adapters opening one SQLite file
// meant the second user's OlmMachine loaded the first user's account.
func TestCryptoDBKeyedByNameAndUser(t *testing.T) {
	dir := t.TempDir()
	a := newCryptoDBForAdapter(dir, "work", "@alice:example.org")
	b := newCryptoDBForAdapter(dir, "work", "@bob:example.org")
	if a == b {
		t.Fatalf("same-named adapters with different users must get different crypto dbs: %q", a)
	}
}

func TestCryptoDBMigratesLegacyNameOnlyPath(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, "work.db")
	if err := os.WriteFile(legacy, []byte("legacy-db"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := newCryptoDBForAdapter(dir, "work", "@alice:example.org")
	if got == legacy {
		t.Fatal("user-scoped crypto db path expected")
	}
	data, err := os.ReadFile(got)
	if err != nil {
		t.Fatalf("migrated db must exist: %v", err)
	}
	if string(data) != "legacy-db" {
		t.Fatalf("legacy crypto db content must survive the rename, got %q", data)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatalf("legacy db must be renamed away, stat err = %v", err)
	}
}

func TestCryptoDBEmptyUserFallsBackToNameOnly(t *testing.T) {
	dir := t.TempDir()
	got := newCryptoDBForAdapter(dir, "work", "")
	want := filepath.Join(dir, "work.db")
	if got != want {
		t.Fatalf("empty userID must keep the name-only path: got %q want %q", got, want)
	}
}

// #2112: the M_UNKNOWN_POS self-heal must reset the didFirstSync gate via
// onTokenReset - the in-process restart performs an initial sync whose
// replayed timeline must be dropped, exactly like a process restart (the
// gate existed precisely for that; 37e9de3b left it open and replayed
// events were processed twice).
func TestSelfHealingSyncerFiresTokenResetCallback(t *testing.T) {
	dir := t.TempDir()
	store := &fileSyncStore{path: filepath.Join(dir, "m.json")}
	var reset int32
	wrapped := &selfHealingSyncer{
		DefaultSyncer: mautrix.NewDefaultSyncer(),
		store:         store,
		onTokenReset:  func() { atomic.AddInt32(&reset, 1) },
	}
	_, err := wrapped.OnFailedSync(nil, &mautrix.HTTPError{
		RespError: &mautrix.RespError{ErrCode: "M_UNKNOWN_POS", StatusCode: http.StatusBadRequest},
	})
	if err == nil {
		t.Fatal("M_UNKNOWN_POS must return an error (Sync exits, outer loop reconnects)")
	}
	if atomic.LoadInt32(&reset) != 1 {
		t.Fatal("onTokenReset must fire exactly once on M_UNKNOWN_POS (didFirstSync gate reset #2112)")
	}
	// Non-M_UNKNOWN_POS must NOT reset the gate (no initial sync follows).
	_, _ = wrapped.OnFailedSync(nil, &mautrix.HTTPError{
		RespError: &mautrix.RespError{ErrCode: "M_LIMIT_EXCEEDED", StatusCode: http.StatusTooManyRequests},
	})
	if atomic.LoadInt32(&reset) != 1 {
		t.Fatal("onTokenReset must not fire for unrelated sync errors")
	}
}
