package runfile

// #1624 case C: a recycled PID passed signal-0 liveness forever, so a
// dead owner's ghost port file kept pointing clients at a port nobody
// listens on. The identity pair (PID + kernel start time) detects the
// swap. On platforms without /proc (macOS/Windows) the identity check
// degrades to liveness-only and these probes skip.

import (
	"encoding/json"
	"os"
	"testing"
)

func TestIssue1624CRecycledPIDRemoved(t *testing.T) {
	self := procStartTime(os.Getpid())
	if self == "" {
		t.Skip("no /proc on this platform - identity check degrades to liveness")
	}
	p := path("sess-1624c")
	pf := PortFile{
		Addr: "127.0.0.1:1", Token: "t", PID: os.Getpid(),
		SessionID: "sess-1624c", Workspace: "/w", Mode: "auto",
		StartTime: "999999", // a start time this live pid certainly does NOT have
	}
	if err := os.WriteFile(p, mustMarshal1624c(pf), 0o600); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(p)
	got, err := Read("sess-1624c")
	if err == nil && got != nil {
		t.Fatalf("recycled-PID ghost must be removed, got %+v", got)
	}
	if _, statErr := os.Stat(p); !os.IsNotExist(statErr) {
		t.Fatal("ghost port file must be deleted")
	}
}

func TestIssue1624CRealIdentityKept(t *testing.T) {
	self := procStartTime(os.Getpid())
	if self == "" {
		t.Skip("no /proc on this platform")
	}
	p := path("sess-1624c2")
	pf := PortFile{
		Addr: "127.0.0.1:2", Token: "t", PID: os.Getpid(),
		SessionID: "sess-1624c2", Workspace: "/w", Mode: "auto",
		StartTime: self, // the TRUE start time of this live process
	}
	if err := os.WriteFile(p, mustMarshal1624c(pf), 0o600); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(p)
	if got, err := Read("sess-1624c2"); err != nil || got == nil {
		t.Fatalf("true owner must pass the identity check: %v", err)
	}
}

func mustMarshal1624c(pf PortFile) []byte {
	b, err := json.Marshal(pf)
	if err != nil {
		panic(err)
	}
	return b
}
