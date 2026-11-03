package session

import (
	"sync"
	"testing"
)

func appendRecorded(ses *Session, ev TunnelEvent) {
	ses.TunnelEventsMu.Lock()
	defer ses.TunnelEventsMu.Unlock()
	ses.TunnelEvents = append(ses.TunnelEvents, ev)
	if len(ses.TunnelEvents) > MaxTunnelEvents {
		pruneIdx := len(ses.TunnelEvents) - MaxTunnelEvents
		ses.TunnelEvents = ses.TunnelEvents[pruneIdx:]
	}
}

func TestIssue2917TunnelEventsConcurrentSnapshot(t *testing.T) {
	ses := &Session{ID: "race-2917"}
	var wg sync.WaitGroup

	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				appendRecorded(ses, TunnelEvent{EventID: "e", Type: "msg"})
			}
		}()
	}

	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				evs := ses.SnapshotTunnelEvents()
				if len(evs) > MaxTunnelEvents {
					t.Errorf("snapshot len %d exceeds cap", len(evs))
					return
				}
				for _, ev := range evs {
					if ev.EventID != "e" || ev.Type != "msg" {
						t.Errorf("torn entry: %+v", ev)
						return
					}
				}
			}
		}()
	}
	wg.Wait()
	if got := len(ses.SnapshotTunnelEvents()); got != 2000 {
		t.Fatalf("expected 2000 events, got %d", got)
	}
}

func TestIssue2917SnapshotNilAndEmptySafe(t *testing.T) {
	var nilSes *Session
	if evs := nilSes.SnapshotTunnelEvents(); evs != nil {
		t.Fatalf("nil session snapshot = %v, want nil", evs)
	}
	s := &Session{ID: "x"}
	if evs := s.SnapshotTunnelEvents(); evs != nil {
		t.Fatalf("empty snapshot = %v, want nil", evs)
	}
	appendRecorded(s, TunnelEvent{EventID: "a"})
	snap := s.SnapshotTunnelEvents()
	if len(snap) != 1 || snap[0].EventID != "a" {
		t.Fatalf("snapshot = %+v, want one event a", snap)
	}

	snap[0].EventID = "mutated"
	if s.SnapshotTunnelEvents()[0].EventID != "a" {
		t.Fatalf("snapshot is not an independent copy")
	}
}
