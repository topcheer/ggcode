package a2a

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/provider"
)

func rawRPC(t *testing.T, url, method string, params interface{}) (string, int) {
	t.Helper()
	body, err := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  method,
		"params":  params,
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	resp, err := http.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("post %s: %v", method, err)
	}
	defer resp.Body.Close()
	buf := new(bytes.Buffer)
	if _, err := buf.ReadFrom(resp.Body); err != nil {
		t.Fatalf("read %s body: %v", method, err)
	}
	return buf.String(), resp.StatusCode
}

func TestIssue1115_DedupRetryStreamEmitsSingleFinal(t *testing.T) {
	reg := newStubRegistry()
	a := agent.NewAgent(&scriptProvider{script: [][]provider.StreamEvent{
		{{Type: provider.StreamEventText, Text: "done"}},
	}}, reg, "test", 5)
	h := newHandler(a, reg)
	s := NewServer(ServerConfig{}, h)
	tsURL := startTestServer(t, s)

	msgID := "msg-1115-dedup"
	params := map[string]interface{}{
		"message": map[string]interface{}{
			"role":      "user",
			"messageId": msgID,
			"parts":     []map[string]interface{}{{"kind": "text", "text": "hi"}},
		},
		"skill": SkillFullTask,
	}

	sendBody, code := rawRPC(t, tsURL, "message/send", params)
	if code != 200 || !strings.Contains(sendBody, "completed") {
		t.Fatalf("send: code=%d body=%s", code, sendBody)
	}

	streamBody, code := rawRPC(t, tsURL, "message/stream", params)
	if code != 200 {
		t.Fatalf("stream: code=%d body=%s", code, streamBody)
	}
	if n := strings.Count(streamBody, `"final":true`); n != 1 {
		t.Fatalf("dedup-retry stream must emit exactly ONE final:true, got %d in:\n%s", n, streamBody)
	}
}

func TestIssue1113_SweptTaskStreamEmitsTaskNotFound(t *testing.T) {
	s := NewServer(ServerConfig{}, newHandler(nil, newStubRegistry()))
	rec := httptest.NewRecorder()

	tk, ok := s.getTaskOrSSEError(rec, rec, json.RawMessage("1"), "no-such-task")
	if ok || tk != nil {
		t.Fatalf("swept task must report ok=false with nil task, got %v/%v", tk, ok)
	}
	if !strings.Contains(rec.Body.String(), "Task not found") {
		t.Fatalf("expected TaskNotFound SSE error, got: %s", rec.Body.String())
	}

	h := newHandler(nil, newStubRegistry())
	tsk := &Task{ID: "t-live"}
	tsk.Status.State = TaskStateCompleted
	h.mu.Lock()
	h.tasks[tsk.ID] = tsk
	h.mu.Unlock()
	s2 := NewServer(ServerConfig{}, h)
	rec2 := httptest.NewRecorder()
	tk2, ok2 := s2.getTaskOrSSEError(rec2, rec2, json.RawMessage("2"), "t-live")
	if !ok2 || tk2 == nil || tk2.ID != "t-live" {
		t.Fatalf("live task must be returned, got %v/%v", tk2, ok2)
	}
	if strings.Contains(rec2.Body.String(), "Task not found") {
		t.Fatalf("live task must not emit TaskNotFound, got: %s", rec2.Body.String())
	}
}

func TestIssue1114_ExtendedCardConcurrentReadWrite(t *testing.T) {
	s := NewServer(ServerConfig{}, newHandler(nil, newStubRegistry()))
	const writesPerWriter = 2000
	const reads = 4000
	var wg sync.WaitGroup
	var readCount int64

	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for j := 0; j < writesPerWriter; j++ {
				card := fmt.Sprintf(`{"extra":%d,%d}`, n, j)
				s.SetExtendedCard(json.RawMessage(card))
			}
		}(i)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < reads; i++ {
			w := httptest.NewRecorder()
			s.handleGetExtendedCard(w, &JSONRPCRequest{ID: json.RawMessage("1")})
			atomic.AddInt64(&readCount, 1)
			if w.Code != http.StatusOK && w.Code != 0 {
				t.Errorf("handleGetExtendedCard status = %d", w.Code)
			}
		}
	}()
	wg.Wait()
	if atomic.LoadInt64(&readCount) != reads {
		t.Fatalf("expected %d reads, got %d", reads, atomic.LoadInt64(&readCount))
	}
}

func startTestServer(t *testing.T, s *Server) string {
	t.Helper()
	ts := httptest.NewServer(s.Mux())
	t.Cleanup(ts.Close)
	return ts.URL
}
