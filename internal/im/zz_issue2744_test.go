package im

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

	a.handleUpdate(context.Background(), upd)
	a.mu.RLock()
	_, recorded := a.seen[1001]
	a.mu.RUnlock()
	if !recorded {
		t.Fatalf("callback_query update was not recorded by seenUpdate dedup")
	}

	a.handleUpdate(context.Background(), upd)
	time.Sleep(300 * time.Millisecond)
	if got := atomic.LoadInt32(&answers); got != 1 {
		t.Fatalf("redelivered callback re-executed handler: answerCallbackQuery called %d times, want 1", got)
	}
}

func TestIssue2744PublishStateConcurrentBotUsername(t *testing.T) {

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

	for i := 0; i < 500; i++ {
		a.publishState(true, "connected", "")
	}
	wg.Wait()

	a.publishState(true, "connected", "")

	mgr.mu.RLock()
	st, ok := mgr.adapters["probe"]
	mgr.mu.RUnlock()
	if !ok || st.ContactURI != "https://t.me/bot499" {
		t.Fatalf("publishState did not record final state: %+v (ok=%v)", st, ok)
	}
}
