package agent

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
