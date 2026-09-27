package im

// r197 pin tests for the Slack interactive outbound seams extracted from
// slackAdapter.SendInteractive. All tests are offline: pure builders are
// asserted directly; the HTTP seam uses httptest with the package sleep
// hook stubbed (no real backoff sleeps).

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// stubSleepRetry overrides the package sleep hook so pin tests never really
// sleep, and records observed delays. Restored via t.Cleanup (same pattern
// as rate_limit_test.go).
func stubSleepRetry(t *testing.T) *[]time.Duration {
	t.Helper()
	orig := sleepRetryFn
	delays := &[]time.Duration{}
	sleepRetryFn = func(ctx context.Context, d time.Duration) error {
		*delays = append(*delays, d)
		return ctx.Err()
	}
	t.Cleanup(func() { sleepRetryFn = orig })
	return delays
}

func TestSlackInteractiveButtonElement(t *testing.T) {
	cases := []struct {
		name      string
		btn       InteractiveButton
		wantStyle any
	}{
		{"primary passthrough", InteractiveButton{Label: "Yes", Value: "y", Style: "primary"}, "primary"},
		{"danger passthrough", InteractiveButton{Label: "No", Value: "n", Style: "danger"}, "danger"},
		{"default collapses", InteractiveButton{Label: "Maybe", Value: "m", Style: "default"}, ""},
		{"unknown collapses", InteractiveButton{Label: "?", Value: "?", Style: "sparkle"}, ""},
		{"empty collapses", InteractiveButton{Label: "Plain", Value: "p"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			el := slackInteractiveButtonElement(tc.btn)
			if el["type"] != "button" {
				t.Errorf("type = %v, want button", el["type"])
			}
			if el["style"] != tc.wantStyle {
				t.Errorf("style = %v, want %q", el["style"], tc.wantStyle)
			}
			if el["value"] != tc.btn.Value {
				t.Errorf("value = %v, want %q", el["value"], tc.btn.Value)
			}
			text, _ := el["text"].(map[string]any)
			if text["type"] != "plain_text" || text["text"] != tc.btn.Label {
				t.Errorf("text = %v, want plain_text/%q", text, tc.btn.Label)
			}
		})
	}
}

func TestSlackInteractiveActionElements(t *testing.T) {
	msg := InteractiveMessage{
		Buttons: []InteractiveButton{
			{Label: "Yes", Value: "y", Style: "primary"},
			{Label: "No", Value: "n", Style: "danger"},
		},
	}
	els := slackInteractiveActionElements(msg)
	if len(els) != 2 {
		t.Fatalf("len = %d, want 2 (no done button without MultiSelect)", len(els))
	}
	if els[0]["style"] != "primary" || els[1]["style"] != "danger" {
		t.Errorf("styles = %v/%v, want primary/danger", els[0]["style"], els[1]["style"])
	}

	msg.MultiSelect = true
	els = slackInteractiveActionElements(msg)
	if len(els) != 3 {
		t.Fatalf("len = %d, want 3 (done appended for MultiSelect)", len(els))
	}
	done := els[2]
	if done["value"] != "__done__" || done["style"] != "primary" {
		t.Errorf("done button = %v, want __done__/primary", done)
	}
	if txt, _ := done["text"].(map[string]any); txt["text"] != "✅ Done" {
		t.Errorf("done label = %v, want ✅ Done", txt["text"])
	}

	if empty := slackInteractiveActionElements(InteractiveMessage{}); len(empty) != 0 {
		t.Errorf("empty msg: len = %d, want 0", len(empty))
	}
}

func TestSlackInteractiveBlocks(t *testing.T) {
	msg := InteractiveMessage{
		ID:      "q1",
		Text:    "Choose wisely:",
		Buttons: []InteractiveButton{{Label: "Yes", Value: "y", Style: "primary"}},
	}
	blocks := slackInteractiveBlocks(msg)
	if len(blocks) != 2 {
		t.Fatalf("len = %d, want 2 (section then actions)", len(blocks))
	}
	if blocks[0]["type"] != "section" {
		t.Errorf("block 0 type = %v, want section", blocks[0]["type"])
	}
	section, _ := blocks[0]["text"].(map[string]any)
	if section["type"] != "mrkdwn" {
		t.Errorf("section text type = %v, want mrkdwn", section["type"])
	}
	// Composition pin: the seam must route the text through markdownToMrkdwn.
	if section["text"] != markdownToMrkdwn("Choose wisely:") {
		t.Errorf("section text = %v, want markdownToMrkdwn passthrough", section["text"])
	}
	if blocks[1]["type"] != "actions" {
		t.Errorf("block 1 type = %v, want actions", blocks[1]["type"])
	}
	if els, _ := blocks[1]["elements"].([]map[string]any); len(els) != 1 {
		t.Errorf("actions elements len = %d, want 1", len(els))
	}
}

func TestSlackPostInteractiveMessageSuccess(t *testing.T) {
	stubSleepRetry(t)
	var gotPath, gotAuth string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true,"ts":"1700000000.000100"}`)
	}))
	defer srv.Close()

	a := &slackAdapter{name: "slack", httpClient: srv.Client(), apiBase: srv.URL, connected: true, botToken: "xoxb-pin"}
	ts, err := a.postInteractiveMessage(context.Background(), "ch-1", "", slackInteractiveBlocks(InteractiveMessage{
		Text:    "approve?",
		Buttons: []InteractiveButton{{Label: "Yes", Value: "y", Style: "primary"}},
	}))
	if err != nil {
		t.Fatalf("postInteractiveMessage: %v", err)
	}
	if ts != "1700000000.000100" {
		t.Errorf("ts = %q", ts)
	}
	if gotPath != "/chat.postMessage" {
		t.Errorf("path = %q", gotPath)
	}
	if gotAuth != "Bearer xoxb-pin" {
		t.Errorf("auth = %q", gotAuth)
	}
	if gotBody["channel"] != "ch-1" {
		t.Errorf("channel = %v", gotBody["channel"])
	}
	if _, has := gotBody["thread_ts"]; has {
		t.Errorf("thread_ts must be absent for empty thread ID")
	}
	blocks, _ := gotBody["blocks"].([]any)
	if len(blocks) != 2 {
		t.Errorf("blocks len = %d, want 2", len(blocks))
	}
}

func TestSlackPostInteractiveMessageThreadTS(t *testing.T) {
	stubSleepRetry(t)
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &gotBody)
		_, _ = io.WriteString(w, `{"ok":true,"ts":"1.2"}`)
	}))
	defer srv.Close()
	a := &slackAdapter{name: "slack", httpClient: srv.Client(), apiBase: srv.URL, connected: true, botToken: "xoxb-pin"}
	// Pin: the thread_ts check trims, but the assigned value stays raw.
	if _, err := a.postInteractiveMessage(context.Background(), "ch", " 1700000000.000100 ", nil); err != nil {
		t.Fatalf("postInteractiveMessage: %v", err)
	}
	if gotBody["thread_ts"] != " 1700000000.000100 " {
		t.Errorf("thread_ts = %q, want raw untrimmed value", gotBody["thread_ts"])
	}
}

func TestSlackPostInteractiveMessageAPIError(t *testing.T) {
	stubSleepRetry(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"ok":false,"error":"channel_not_found"}`)
	}))
	defer srv.Close()
	a := &slackAdapter{name: "slack", httpClient: srv.Client(), apiBase: srv.URL, connected: true, botToken: "xoxb-pin"}
	_, err := a.postInteractiveMessage(context.Background(), "ghost", "", nil)
	if err == nil || err.Error() != "Slack chat.postMessage: channel_not_found" {
		t.Errorf("err = %v, want Slack chat.postMessage: channel_not_found", err)
	}
}

func TestSlackPostInteractiveMessageRateLimitedBodyRetry(t *testing.T) {
	delays := stubSleepRetry(t)
	var requests int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		if requests == 1 {
			_, _ = io.WriteString(w, `{"ok":false,"error":"ratelimited"}`)
			return
		}
		_, _ = io.WriteString(w, `{"ok":true,"ts":"9.9"}`)
	}))
	defer srv.Close()
	a := &slackAdapter{name: "slack", httpClient: srv.Client(), apiBase: srv.URL, connected: true, botToken: "xoxb-pin"}
	ts, err := a.postInteractiveMessage(context.Background(), "ch", "", nil)
	if err != nil || ts != "9.9" {
		t.Fatalf("ts/err = %q/%v", ts, err)
	}
	if requests != 2 {
		t.Errorf("requests = %d, want 2", requests)
	}
	// #1237 shape: exactly one retry, delayed by defaultRetryDelay (no header).
	if len(*delays) != 1 || (*delays)[0] != defaultRetryDelay {
		t.Errorf("delays = %v, want [%v]", *delays, defaultRetryDelay)
	}
}

func TestSlackPostInteractiveMessageHTTP429Exhausts(t *testing.T) {
	delays := stubSleepRetry(t)
	var requests int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()
	a := &slackAdapter{name: "slack", httpClient: srv.Client(), apiBase: srv.URL, connected: true, botToken: "xoxb-pin"}
	_, err := a.postInteractiveMessage(context.Background(), "ch", "", nil)
	if err == nil || err.Error() != "Slack API rate limited: max retries (2) exceeded" {
		t.Errorf("err = %v, want rate limit exhausted", err)
	}
	if requests != maxRateLimitRetries+1 {
		t.Errorf("requests = %d, want %d", requests, maxRateLimitRetries+1)
	}
	if len(*delays) != maxRateLimitRetries {
		t.Errorf("sleeps = %d, want %d", len(*delays), maxRateLimitRetries)
	}
}

func TestSlackSendInteractiveGates(t *testing.T) {
	offline := &slackAdapter{name: "slack-bot"}
	_, err := offline.SendInteractive(context.Background(), ChannelBinding{ChannelID: "ch"}, InteractiveMessage{Text: "hi"})
	if err == nil || err.Error() != `Slack bot "slack-bot" is not online` {
		t.Errorf("offline gate err = %v", err)
	}

	online := &slackAdapter{name: "slack-bot", connected: true}
	_, err = online.SendInteractive(context.Background(), ChannelBinding{ChannelID: "   "}, InteractiveMessage{Text: "hi"})
	if err == nil || err.Error() != "Slack channel is not configured" {
		t.Errorf("channel gate err = %v", err)
	}
}
