package webui

import (
	"testing"
	"time"
)

// Every queue event must carry the full items snapshot (so a lost or
// text-mismatched event cannot leave the browser stale) and a strictly
// increasing seq (so out-of-order deliveries are dropped client-side), and
// must be delivered even when the consumer is slow to read.
func TestEmitQueueEvent_CarriesSnapshotAndSeq(t *testing.T) {
	s := NewServer(nil)
	items := []string{"a", "b"}
	s.SetQueueSnapshotFn(func() []string { return items })
	ch := make(chan interface{}, 8)
	unregister := s.registerQueueSink(ch)
	defer unregister()

	s.EmitQueueEvent("append", "b", 2)
	s.EmitQueueEvent("dequeue", "a", 1)
	items = []string{"c"}
	s.EmitQueueEvent("clear", "", 0)

	// Delivery goes through per-sink goroutines, so arrival order is not
	// guaranteed - exactly why every message carries a seq. Order by seq.
	type ev struct {
		event string
		items []string
		seq   int64
	}
	got := make([]ev, 0, 3)
	for i := 0; i < 3; i++ {
		select {
		case raw := <-ch:
			m := raw.(map[string]interface{})
			if m["type"] != "queue" {
				t.Fatalf("unexpected message type %v", m["type"])
			}
			items, _ := m["items"].([]string)
			seq, _ := m["seq"].(int64)
			if seq == 0 {
				t.Fatalf("event %d: seq must be set", i)
			}
			if m["count"].(int) != len(items) {
				t.Fatalf("event %d: count %v must equal the visible snapshot length %d", i, m["count"], len(items))
			}
			got = append(got, ev{m["event"].(string), items, seq})
		case <-time.After(2 * time.Second):
			t.Fatalf("event %d not delivered", i)
		}
	}
	for i := 0; i < len(got); i++ {
		for j := i + 1; j < len(got); j++ {
			if got[j].seq < got[i].seq {
				got[i], got[j] = got[j], got[i]
			}
		}
	}
	want := []ev{
		{"append", []string{"a", "b"}, 0},
		{"dequeue", []string{"a", "b"}, 0},
		{"clear", []string{"c"}, 0},
	}
	for i := range want {
		if got[i].event != want[i].event || len(got[i].items) != len(want[i].items) {
			t.Fatalf("event %d = %+v, want %+v", i, got[i], want[i])
		}
		for k := range want[i].items {
			if got[i].items[k] != want[i].items[k] {
				t.Fatalf("event %d items = %v, want %v", i, got[i].items, want[i].items)
			}
		}
	}
	for i := 1; i < len(got); i++ {
		if got[i].seq <= got[i-1].seq {
			t.Fatalf("seq must strictly increase with emission order: %+v", got)
		}
	}
	// connect-time snapshot carries the same shape
	snap := s.queueSnapshotMsg()
	if snap["event"] != "list" || snap["seq"] == nil {
		t.Fatalf("snapshot message must carry list + seq: %v", snap)
	}
}

// The connect-time snapshot is sent through the same non-blocking send as the
// old incremental events; a slow tab must not make EmitQueueEvent block the
// TUI Update loop: it waits for buffer space in a goroutine, bounded.
func TestEmitQueueEvent_DoesNotBlockCallerOnFullSink(t *testing.T) {
	s := NewServer(nil)
	s.SetQueueSnapshotFn(func() []string { return []string{"x"} })
	ch := make(chan interface{}, 1) // capacity 1, nobody reads
	unregister := s.registerQueueSink(ch)
	defer unregister()
	done := make(chan struct{})
	go func() {
		s.EmitQueueEvent("append", "x", 1)
		s.EmitQueueEvent("dequeue", "x", 0) // second send has no space
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("EmitQueueEvent blocked the caller on a full sink")
	}
}
