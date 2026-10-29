package tui

import "testing"

// #4209: the stream frameLoop goroutine reads terminal size via
// getSize()/setSize() under the shared mutex instead of the Bubble Tea
// model's plain width/height fields (data race, -race-stable).

func TestStreamViewStateSizeRoundtrip(t *testing.T) {
	s := &streamViewStateData{}
	if w, h := s.getSize(); w != 0 || h != 0 {
		t.Fatalf("zero-value size = %dx%d, want 0x0", w, h)
	}
	s.setSize(120, 40)
	if w, h := s.getSize(); w != 120 || h != 40 {
		t.Fatalf("size = %dx%d, want 120x40", w, h)
	}
	// Snapshot API still works independently.
	s.setSnapshot("frame")
	if snap, _ := s.getSnapshot(); snap != "frame" {
		t.Fatalf("snapshot = %q, want %q", snap, "frame")
	}
}

// Race probe: concurrent handleResize-shaped writers vs frameLoop-shaped
// readers. Run under -race this is the exact interleaving that used to trip
// the detector (bare read of m.width/m.height from the stream goroutine).
func TestStreamViewStateSizeRace(t *testing.T) {
	s := &streamViewStateData{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 10000; i++ {
			s.getSize()
			s.getSnapshot()
		}
	}()
	for i := 1; i <= 10000; i++ {
		s.setSize(i, i+1)
	}
	<-done
}
