package tool

import (
	"testing"
	"time"
)

func TestCloseStopsProfileGC(t *testing.T) {
	b := NewBrowser()
	b.startBrowserProfileGC()

	time.Sleep(50 * time.Millisecond)

	if err := b.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case <-b.gcStop:

	default:

		select {
		case <-b.gcStop:
		case <-time.After(2 * time.Second):
			t.Fatal("Close did not close the GC stop signal - goroutine leaks (#2096 bug A)")
		}
	}

	if err := b.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestCloseNilGCStopSafe(t *testing.T) {
	b := &Browser{}
	if err := b.Close(); err != nil {
		t.Fatalf("Close on zero-value Browser: %v", err)
	}
}
