package a2a

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"testing"
)

// fake introspector with all five faces
type fakeInst struct{ mode string }

func (f *fakeInst) RuntimeSnapshot(mode string) (*RuntimeSnapshotV2, error) {
	if mode == IntrospectionMetadata {
		return &RuntimeSnapshotV2{Schema: "v2", Mode: mode, TakenAt: "t"}, nil
	}
	return &RuntimeSnapshotV2{
		Schema: "v2", Mode: mode, TakenAt: "t",
		Busy:         &RuntimeBusy{Busy: true, BusySince: "s", InFlightTool: "edit_file"},
		Todos:        []RuntimeTodo{{ID: "1", Subject: "sub", Status: "in_progress"}},
		Crons:        []RuntimeCron{{ID: "c1", Cron: "0 * * * *"}},
		Recent:       []RuntimeToolTrace{{TS: "ts", Tool: "read_file", Summary: "r"}},
		Conversation: []RuntimeConversationTurn{{Role: "user", Summary: "q"}},
	}, nil
}

func newIntrospectServer(t *testing.T, key []byte, mode string) *httptest.Server {
	t.Helper()
	srv := &Server{domainKey: key, introspectionMode: mode, introspector: &fakeInst{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/", srv.a2aMiddleware(srv.handleRPC))
	return httptest.NewServer(mux)
}

func TestIssue5593_SignVerifySymmetry(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	nonce, _ := NewIntrospectionNonce()
	sig := SignIntrospectionProof(key, "runtime/snapshot", nonce)
	if !VerifyIntrospectionProof(key, "runtime/snapshot", nonce, sig) {
		t.Fatal("sign/verify asymmetry")
	}
	if VerifyIntrospectionProof([]byte("wrong-key-0123456789abcdef01234567"), "runtime/snapshot", nonce, sig) {
		t.Fatal("wrong key accepted")
	}
}

func TestIssue5593_PeerHandshakeFullAndDenied(t *testing.T) {
	// the domain key resolves via .git - a bare TempDir has none and
	// LoadOrCreateDomainKey fails (the earlier diag swallowed that error and
	// produced an empty key, hence the denial). Use a real git dir.
	dir := t.TempDir()
	if err := exec.Command("git", "-C", dir, "init", "-q").Run(); err != nil {
		t.Skipf("git unavailable: %v", err)
	}
	key, err := LoadOrCreateDomainKey(dir)
	if err != nil || len(key) < 32 {
		t.Fatalf("domain key: %v len=%d", err, len(key))
	}
	ts := newIntrospectServer(t, key, IntrospectionFull)
	defer ts.Close()

	// full handshake over the real HTTP path
	snap, err := IntrospectPeer(context.Background(), ts.URL, "", key, IntrospectionFull)
	if err != nil {
		t.Fatal(err)
	}
	if !snap.Busy.Busy || snap.Busy.InFlightTool != "edit_file" || len(snap.Todos) != 1 || len(snap.Crons) != 1 || len(snap.Recent) != 1 || len(snap.Conversation) != 1 {
		t.Fatalf("five faces not delivered: %+v", snap)
	}

	// wrong domain key fails closed
	badKey := []byte("ffffffffffffffffffffffffffffffff")
	if _, err := IntrospectPeer(context.Background(), ts.URL, "", badKey, IntrospectionFull); err == nil {
		t.Fatal("wrong-key request must fail")
	}
}

func TestIssue5593_MetadataModeDownshift(t *testing.T) {
	dir := t.TempDir()
	if err := exec.Command("git", "-C", dir, "init", "-q").Run(); err != nil {
		t.Skipf("git unavailable: %v", err)
	}
	key, _ := LoadOrCreateDomainKey(dir)
	ts := newIntrospectServer(t, key, IntrospectionMetadata)
	defer ts.Close()
	snap, err := IntrospectPeer(context.Background(), ts.URL, "", key, IntrospectionMetadata)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Mode != IntrospectionMetadata || len(snap.Todos) != 0 || snap.Busy != nil {
		t.Fatalf("metadata downshift leaked content faces: %+v", snap)
	}
}
