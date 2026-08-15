package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"sync"
	"testing"
	"time"
)

// TestWriteMessageConcurrentNoInterleave (#480): server-request responses
// (writeMessage, formerly lock-free) and request sends (under c.mu) write
// the same stdin. Concurrent writers must never interleave — every line
// on the pipe must parse as exactly one JSON-RPC frame.
func TestWriteMessageConcurrentNoInterleave(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	c := &Client{name: "test", stdin: w, transport: "stdio"}

	const writers = 4
	const perWriter = 50
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			<-start
			for j := 0; j < perWriter; j++ {
				// Alternate the two entry paths that must share c.mu.
				if (id+j)%2 == 0 {
					_ = c.writeMessage(map[string]interface{}{
						"jsonrpc": "2.0", "id": id, "result": map[string]string{"w": "resp"},
					})
				} else {
					_ = c.sendNotification(context.Background(), Notification{Method: "notify", Params: json.RawMessage(`{"n":1}`)})
				}
			}
		}(i)
	}
	close(start)
	wg.Wait()
	w.Close()

	// Every line must be a complete, parseable JSON object — an interleaved
	// frame would fail json.Unmarshal or produce glued/duplicated content.
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	lines := 0
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			continue
		}
		var obj map[string]interface{}
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			t.Fatalf("interleaved/corrupt frame (#480 regression) at line %d: %v — %q", lines+1, err, line)
		}
		lines++
	}
	if lines != writers*perWriter {
		t.Fatalf("expected %d frames, got %d (frames lost or glued)", writers*perWriter, lines)
	}
}

// TestWriteMessageUnlockedRequiresLock (structural): the Unlocked variant is
// for callers already holding c.mu — verify sendNotification (which takes
// c.mu then calls the Unlocked variant) works end-to-end, proving the split
// did not double-lock.
func TestSendNotificationNoDeadlock(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	c := &Client{name: "test", stdin: w, transport: "stdio"}

	done := make(chan error, 1)
	go func() { done <- c.sendNotification(context.Background(), Notification{Method: "ping"}) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("sendNotification failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("sendNotification deadlocked — writeMessageUnlocked double-lock regression")
	}
	w.Close()
	sc := bufio.NewScanner(r)
	if !sc.Scan() {
		t.Fatal("no frame written")
	}
	var obj map[string]interface{}
	if err := json.Unmarshal(sc.Bytes(), &obj); err != nil {
		t.Fatalf("frame corrupt: %v", err)
	}
	if obj["method"] != "ping" {
		t.Fatalf("wrong frame: %v", obj)
	}
}
