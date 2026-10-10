package agent

// #3777 companion tests.
// A: Pattern 1's fingerprint must not embed the occurrence count - a
//    partial fix (2 bare Done -> 1) must NOT re-report the remaining
//    pre-existing issue as new.
// B: the *Server suffix gate removal - a *Server method using a LOCAL
//    wg variable with bare Done and no Add must now be flagged; the
//    struct-field form (`s.wg.Done()`) must stay exempt.

import (
	"strings"
	"testing"
)

func TestIssue3777_PartialFixDoesNotReReportRemaining(t *testing.T) {
	old := `package p
import "sync"
func work(wg *sync.WaitGroup) {
	wg.Add(2)
	wg.Done()
	wg.Done()
}
`
	// Agent fixes ONE of the two bare Done calls (adds defer). The
	// remaining single bare Done is a PRE-EXISTING issue, not newly
	// introduced: delta must stay silent. Pre-fix, the embedded count
	// (2 -> 1) changed the fingerprint and re-reported it.
	new := `package p
import "sync"
func work(wg *sync.WaitGroup) {
	wg.Add(2)
	defer wg.Done()
	wg.Done()
}
`
	warnings := checkWaitGroupMisuse("work.go", old, new)
	for _, w := range warnings {
		if strings.Contains(w, "without defer") {
			t.Errorf("partial fix re-reported the remaining pre-existing issue: %q", w)
		}
	}
}

func TestIssue3777_FullyNewMisuseStillReported(t *testing.T) {
	// A brand-new bare Done in a previously clean function must still
	// fire - the fingerprint stability must not mute genuinely new issues.
	old := `package p
import "sync"
func work(wg *sync.WaitGroup) {
	wg.Add(1)
	defer wg.Done()
}
`
	new := `package p
import "sync"
func work(wg *sync.WaitGroup) {
	wg.Add(1)
	wg.Done()
}
`
	warnings := checkWaitGroupMisuse("work.go", old, new)
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "without defer") {
			found = true
		}
	}
	if !found {
		t.Error("newly introduced bare Done must still be reported")
	}
}

func TestIssue3777_CountRenderedNotFingerprinted(t *testing.T) {
	// Two fresh bare Done occurrences in a new file: one warning, with
	// the count rendered in the message.
	new := `package p
import "sync"
func work(wg *sync.WaitGroup) {
	wg.Add(2)
	wg.Done()
	wg.Done()
}
`
	warnings := checkWaitGroupMisuse("work.go", "", new)
	if len(warnings) != 1 {
		t.Fatalf("want exactly 1 warning, got %d: %v", len(warnings), warnings)
	}
	if !strings.Contains(warnings[0], "2 occurrences") {
		t.Errorf("rendered warning should carry the count, got %q", warnings[0])
	}
}

func TestIssue3777_ServerReceiverLocalWGNowChecked(t *testing.T) {
	// B: a *fooServer method using a LOCAL wg with bare Done and no Add
	// is a genuine misuse (Done panics: negative counter). Pre-fix, the
	// *Server suffix gate exempted the whole class before the shape
	// check could run; it must now be flagged.
	new := `package p
import "sync"
type fooServer struct{}
func (s *fooServer) run() {
	var wg sync.WaitGroup
	wg.Done()
}
`
	warnings := checkWaitGroupMisuse("work.go", "", new)
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "never called") || strings.Contains(w, "without defer") {
			found = true
		}
	}
	if !found {
		t.Errorf("*Server local-wg misuse must be flagged, got %v", warnings)
	}
}

func TestIssue3777_ServerReceiverStructFieldWGStillExempt(t *testing.T) {
	// B guard: the canonical spawner-splits-Add shape through the
	// receiver's struct field (`s.wg.Done()` in a *Server method) must
	// remain exempt from the Add() check - recvFieldWGDone covers it
	// without the suffix gate. (Pattern 1's defer advice still applies to
	// a bare s.wg.Done() and is intentionally out of scope here.)
	new := `package p
import "sync"
type fooServer struct{ wg sync.WaitGroup }
func (s *fooServer) worker() {
	s.wg.Done()
}
`
	warnings := checkWaitGroupMisuse("work.go", "", new)
	for _, w := range warnings {
		if strings.Contains(w, "never called") {
			t.Errorf("struct-field wg on *Server receiver must stay exempt from the Add() check, got %q", w)
		}
	}
}

func TestIssue3777_NonServerReceiverStructFieldWGStillExempt(t *testing.T) {
	// Same shape on a non-Server receiver name (#2987's original point):
	// the shape check, not the type name, is the gate.
	new := `package p
import "sync"
type pool struct{ wg sync.WaitGroup }
func (p *pool) worker() {
	p.wg.Done()
}
`
	warnings := checkWaitGroupMisuse("work.go", "", new)
	for _, w := range warnings {
		if strings.Contains(w, "never called") {
			t.Errorf("struct-field wg on non-Server receiver must stay exempt from the Add() check, got %q", w)
		}
	}
}
