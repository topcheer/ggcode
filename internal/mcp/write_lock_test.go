package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"
)

func TestWriteMessageConcurrentNoInterleave(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	c := &Client{name: "test", stdin: w, transport: "stdio"}

	type readResult struct {
		lines int
		err   error
	}
	resCh := make(chan readResult, 1)
	go func() {
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
				r.Close()
				resCh <- readResult{err: fmt.Errorf("interleaved/corrupt frame (#480 regression) at line %d: %v — %q", lines+1, err, line)}
				return
			}
			lines++
		}
		resCh <- readResult{lines: lines}
	}()

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

	select {
	case res := <-resCh:
		if res.err != nil {
			t.Fatal(res.err)
		}
		if res.lines != writers*perWriter {
			t.Fatalf("expected %d frames, got %d (frames lost or glued)", writers*perWriter, res.lines)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("reader did not finish within 30s — writers done and pipe closed, but EOF never observed")
	}
}

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
