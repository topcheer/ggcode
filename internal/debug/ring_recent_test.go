package debug

import "testing"

// TestRingHistoryReturnsMostRecent locks the documented semantics of
// RingHistory: "read recent entries" must return the NEWEST n entries in
// chronological order, not the oldest n (regression guard for the ring
// slice-direction fix).
func TestRingHistoryReturnsMostRecent(t *testing.T) {
	// Use a private ring via the same append path as Log, then restore.
	ringMu.Lock()
	savedBuf, savedHead, savedCount := ringBuf, ringHead, ringCount
	ringBuf = make([]RingEntry, ringCap)
	ringHead, ringCount = 0, 0
	ringMu.Unlock()
	defer func() {
		ringMu.Lock()
		ringBuf, ringHead, ringCount = savedBuf, savedHead, savedCount
		ringMu.Unlock()
	}()

	for i := 0; i < 5; i++ {
		ringAppend("test", entryMsg(i))
	}

	got := RingHistory(3, "")
	if len(got) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(got))
	}
	for k, e := range got {
		if want := entryMsg(2 + k); e.Message != want {
			t.Fatalf("entry %d: expected newest-window msg %q, got %q", k, want, e.Message)
		}
	}

	// Filtering must also take the newest matching window.
	got = RingHistory(2, "test")
	if len(got) != 2 || got[0].Message != entryMsg(3) || got[1].Message != entryMsg(4) {
		t.Fatalf("filtered recent window wrong: %+v", got)
	}
}

func entryMsg(i int) string {
	return "msg-" + string(rune('0'+i))
}
