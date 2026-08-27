package provider

// Tests for issue #1168: the anthropic ChatStream message_start branch
// assigned cacheWriteTokens/cacheReadTokens unconditionally, so a
// duplicate or out-of-order message_start (retried / resumed stream,
// zeroed Usage) wiped cache counters already accumulated via
// message_delta. The #722 fix already guarded inputTokens the same way;
// this replicates that guard for the cache fields.
//
// Probes use real httptest SSE round trips, mirroring zz_issue722 Fold 4.

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// zz1168RunStream drives one ChatStream call against an SSE test server
// and returns the final Done event's TokenUsage.
func zz1168RunStream(t *testing.T, sse string) (input, output, cacheWrite, cacheRead int) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, sse)
	}))
	defer server.Close()

	p := NewAnthropicProviderWithBaseURL("test-key", "test-model", 9999, server.URL)
	ch, err := p.ChatStream(context.Background(), []Message{{Role: "user", Content: []ContentBlock{{Type: "text", Text: "hi"}}}}, nil)
	if err != nil {
		t.Fatalf("ChatStream returned error: %v", err)
	}

	var done *StreamEvent
	timeout := time.After(30 * time.Second)
loop:
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				break loop
			}
			if ev.Type == StreamEventError {
				t.Fatalf("unexpected stream error: %v", ev.Error)
			}
			if ev.Type == StreamEventDone {
				done = &ev
			}
		case <-timeout:
			t.Fatal("stream did not finish within 30s")
		}
	}
	if done == nil {
		t.Fatal("no Done event on stream")
	}
	if done.Usage == nil {
		t.Fatal("Done event has nil Usage")
	}
	return done.Usage.InputTokens, done.Usage.OutputTokens, done.Usage.CacheWrite, done.Usage.CacheRead
}

// TestZZIssue1168_DuplicateMessageStartDoesNotZeroCache: a mid-stream
// duplicate message_start carrying a zeroed Usage must not erase cache
// counters already reported by the first message_start.
func TestZZIssue1168_DuplicateMessageStartDoesNotZeroCache(t *testing.T) {
	sse := "" +
		"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"m\",\"content\":[],\"stop_reason\":null,\"usage\":{\"input_tokens\":100,\"output_tokens\":1,\"cache_creation_input_tokens\":10,\"cache_read_input_tokens\":20}}}\n\n" +
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hello\"}}\n\n" +
		"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"m\",\"content\":[],\"stop_reason\":null,\"usage\":{\"input_tokens\":0,\"output_tokens\":0,\"cache_creation_input_tokens\":0,\"cache_read_input_tokens\":0}}}\n\n" +
		"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":5}}\n\n" +
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"

	_, _, cacheWrite, cacheRead := zz1168RunStream(t, sse)
	if cacheWrite != 10 {
		t.Fatalf("CacheWrite = %d, want 10 (duplicate message_start with zeroed usage must not clobber)", cacheWrite)
	}
	if cacheRead != 20 {
		t.Fatalf("CacheRead = %d, want 20 (duplicate message_start with zeroed usage must not clobber)", cacheRead)
	}
}

// TestZZIssue1168_OutOfOrderDeltaBeforeMessageStart: cache tokens seen in
// a message_delta before a zeroed message_start must survive, mirroring
// the #722 Fold 4 input_tokens ordering case.
func TestZZIssue1168_OutOfOrderDeltaBeforeMessageStart(t *testing.T) {
	sse := "" +
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{},\"usage\":{\"input_tokens\":99,\"output_tokens\":2,\"cache_creation_input_tokens\":7,\"cache_read_input_tokens\":9}}\n\n" +
		"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"m\",\"content\":[],\"stop_reason\":null,\"usage\":{\"input_tokens\":0,\"output_tokens\":0,\"cache_creation_input_tokens\":0,\"cache_read_input_tokens\":0}}}\n\n" +
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hello\"}}\n\n" +
		"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":5}}\n\n" +
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"

	_, _, cacheWrite, cacheRead := zz1168RunStream(t, sse)
	if cacheWrite != 7 {
		t.Fatalf("CacheWrite = %d, want 7 (out-of-order zeroed message_start must not clobber)", cacheWrite)
	}
	if cacheRead != 9 {
		t.Fatalf("CacheRead = %d, want 9 (out-of-order zeroed message_start must not clobber)", cacheRead)
	}
}

// TestZZIssue1168_NonZeroMessageStartStillUpdates: the guard must only
// reject zero values - a legitimate later message_start with real
// non-zero cache numbers still overwrites the counters.
func TestZZIssue1168_NonZeroMessageStartStillUpdates(t *testing.T) {
	sse := "" +
		"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"m\",\"content\":[],\"stop_reason\":null,\"usage\":{\"input_tokens\":100,\"output_tokens\":1,\"cache_creation_input_tokens\":10,\"cache_read_input_tokens\":20}}}\n\n" +
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hello\"}}\n\n" +
		"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"m\",\"content\":[],\"stop_reason\":null,\"usage\":{\"input_tokens\":110,\"output_tokens\":1,\"cache_creation_input_tokens\":30,\"cache_read_input_tokens\":40}}}\n\n" +
		"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":5}}\n\n" +
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"

	input, _, cacheWrite, cacheRead := zz1168RunStream(t, sse)
	if cacheWrite != 30 {
		t.Fatalf("CacheWrite = %d, want 30 (non-zero message_start must still update)", cacheWrite)
	}
	if cacheRead != 40 {
		t.Fatalf("CacheRead = %d, want 40 (non-zero message_start must still update)", cacheRead)
	}
	if input != 100 {
		t.Fatalf("InputTokens = %d, want 100 (#722 guard keeps first non-zero input)", input)
	}
}
