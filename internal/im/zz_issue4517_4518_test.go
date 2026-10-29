//go:build goolm

package im

import (
	"os"
	"strings"
	"testing"
)

// fakeCloser records Close calls for the #4518 behavioral test.
type fakeCloser struct{ closed int }

func (f *fakeCloser) Close() error { f.closed++; return nil }

// #4518: Close() must close the crypto db handle - it used to only flip
// closed/connected and cancel, so a MuteBinding → stopAdapter → Close path
// (which never re-enters openPersistentCryptoStore) leaked the SQLite
// connection (and its _txlock=immediate file lock) until process exit.
func TestIssue4518_CloseClosesCryptoDB(t *testing.T) {
	a := &matrixAdapter{}
	fc := &fakeCloser{}
	a.cryptoDBCloser = fc
	if err := a.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if fc.closed != 1 {
		t.Fatalf("crypto db handle not closed exactly once: %d", fc.closed)
	}
	if a.cryptoDBCloser != nil {
		t.Fatal("cryptoDBCloser must be nil after Close (idempotent re-entry)")
	}
	// Idempotent: a second Close must not double-close.
	if err := a.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
	if fc.closed != 1 {
		t.Fatalf("double-close detected: %d", fc.closed)
	}
}

// #4517 pin: the #433-missed read sites must go through currentClient()
// (the RLock snapshot helper) instead of raw a.client / s.adapter.client
// reads, and the success-path mach write must be lock-guarded like the
// failure path already was.
func TestIssue4517_MissedSitesSnapshotUnderLock(t *testing.T) {
	b, err := os.ReadFile("matrix_adapter.go")
	if err != nil {
		t.Skip("source not readable in this environment")
	}
	s := string(b)
	for _, site := range []string{
		"func (a *matrixAdapter) fetchDMRooms",
		"func (a *matrixAdapter) checkIsDMViaAPI",
		"func (a *matrixAdapter) getDisplayName",
		"func (s *cryptoStateStore) GetHistoryVisibility",
		"func (s *cryptoStateStore) IsEncrypted",
		"func (s *cryptoStateStore) GetEncryptionEvent",
		"func (s *cryptoStateStore) GetRoomJoinedOrInvitedMembers",
	} {
		idx := strings.Index(s, site)
		if idx < 0 {
			t.Fatalf("site not found: %s", site)
		}
		end := strings.Index(s[idx+1:], "\nfunc ")
		if end < 0 {
			end = len(s) - idx - 1
		}
		body := s[idx : idx+1+end]
		if strings.Contains(body, ".client.") && !strings.Contains(body, "currentClient()") {
			t.Fatalf("#4517 regression: raw client read in %s", site)
		}
	}
	// The mach success-path write must be lock-guarded.
	machIdx := strings.LastIndex(s, "a.mach = mach")
	if machIdx < 0 {
		t.Fatal("mach write not found")
	}
	precede := s[max(0, machIdx-200):machIdx]
	if !strings.Contains(precede, "a.mu.Lock()") {
		t.Fatal("#4517 regression: success-path a.mach write is not lock-guarded")
	}
}
