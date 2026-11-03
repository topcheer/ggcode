package im

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func TestIssue3353_SendFileConnectedReadUnderLock(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	discord := &discordAdapter{name: "dc", apiBase: srv.URL, httpClient: srv.Client(), connected: true}
	slack := &slackAdapter{name: "sl", apiBase: srv.URL, httpClient: srv.Client(), botToken: "x", connected: true}
	file := OutboundFile{Filename: "a.txt", MIME: "text/plain", Data: []byte("X")}
	binding := ChannelBinding{ChannelID: "c1"}

	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			discord.mu.Lock()
			discord.connected = i%2 == 0
			discord.mu.Unlock()
		}
		discord.mu.Lock()
		discord.connected = true
		discord.mu.Unlock()
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			slack.mu.Lock()
			slack.connected = i%2 == 0
			slack.mu.Unlock()
		}
		slack.mu.Lock()
		slack.connected = true
		slack.mu.Unlock()
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			_ = discord.SendFile(context.Background(), binding, file, "")
			_ = slack.SendFile(context.Background(), binding, file, "")
		}
	}()
	wg.Wait()

	if err := discord.SendFile(context.Background(), binding, file, ""); err != nil {
		t.Fatalf("discord SendFile final send failed: %v", err)
	}
	if err := slack.SendFile(context.Background(), binding, file, ""); err != nil {
		t.Fatalf("slack SendFile final send failed: %v", err)
	}
}
