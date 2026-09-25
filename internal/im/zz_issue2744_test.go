package im

// #2744 probes.
//
// 1. handleUpdate's callback_query branch returned BEFORE seenUpdate ran, so
//    a re-delivered callback (Telegram re-sends until the next successful
//    getUpdates carries the new offset; timeout/network failure/process
//    restart in between re-delivers the same update_id) executed the handler
//    twice -- flipping multi-select choices and re-answering the callback.
//    The dedup must now cover BOTH update kinds.
// 2. publishState read a.botUsername bare while connectAndServe writes it
//    under a.mu -- a string-header data race during reconnect. The read must
//    take the same RLock as the group-mention read in handleUpdate.

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/util"
)

func TestIssue2744CallbackQueryDeduplicatedOnRedelivery(t *testing.T) {
	var answers int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/botT/answerCallbackQuery" {
			atomic.AddInt32(&answers, 1)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	a := &tgAdapter{
		name:       "probe",
		httpClient: util.NewInsecureAwareClient(5 * time.Second),
		botToken:   "T",
		apiBase:    srv.URL,
		seen:       make(map[int]time.Time),
	}
	upd := map[string]any{
		"update_id":      1001,
		"callback_query": map[string]any{"id": "cb1"},
	}

	// First delivery: handler runs and the update MUST land in the seen map.
	// Pre-fix, the callback branch returned ahead of seenUpdate and the seen
	// map stayed empty forever.
	a.handleUpdate(context.Background(), upd)
	a.mu.RLock()
	_, recorded := a.seen[1001]
	a.mu.RUnlock()
	if !recorded {
		t.Fatalf("callback_query update was not recorded by seenUpdate dedup")
	}

	// Redelivery of the same update_id (reconnect window): the handler must
	// NOT run again -- exactly one answerCallbackQuery network request total.
	a.handleUpdate(context.Background(), upd)
	time.Sleep(300 * time.Millisecond) // answerCallbackQuery fires via safego.Go
	if got := atomic.LoadInt32(&answers); got != 1 {
		t.Fatalf("redelivered callback re-executed handler: answerCallbackQuery called %d times, want 1", got)
	}
}

func TestIssue2744PublishStateConcurrentBotUsername(t *testing.T) {
	// Manager with the maps PublishAdapterState writes, so the publish path
	// runs to completion instead of panicking on nil-map assignment.
	mgr := &Manager{
		currentBindings: make(map[string]*ChannelBinding),
		adapters:        make(map[string]AdapterState),
	}
	a := &tgAdapter{
		name:    "probe",
		manager: mgr,
		seen:    make(map[int]time.Time),
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 500; i++ {
			a.mu.Lock()
			a.botUsername = fmt.Sprintf("bot%d", i)
			a.mu.Unlock()
		}
	}()
	// Concurrent publishState reads: pre-fix this read a.botUsername bare
	// (string-header race with the writer); -race flags it immediately.
	for i := 0; i < 500; i++ {
		a.publishState(true, "connected", "")
	}
	wg.Wait()

	mgr.mu.RLock()
	st, ok := mgr.adapters["probe"]
	mgr.mu.RUnlock()
	if !ok || st.ContactURI != "https://t.me/bot499" {
		t.Fatalf("publishState did not record final state: %+v (ok=%v)", st, ok)
	}
}
