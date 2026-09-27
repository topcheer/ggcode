package im

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// Behavior pins for the mattermost outbound send seams extracted from
// sendTextWithFiles (r198). These lock the wire-visible contract:
// chunk planning fallback, thread-root promotion, #963 file_ids placement,
// and the inter-message pacing cancellation path.

func TestMattermostChunkPlanPins(t *testing.T) {
	t.Run("plain text passes through SplitMarkdown", func(t *testing.T) {
		got := mattermostChunkPlan("hello", nil)
		if len(got) != 1 || got[0] != "hello" {
			t.Fatalf("chunkPlan(hello) = %v, want [hello]", got)
		}
	})

	t.Run("files only yields single empty chunk", func(t *testing.T) {
		got := mattermostChunkPlan("", []string{"f1"})
		if len(got) != 1 || got[0] != "" {
			t.Fatalf("chunkPlan(files-only) = %v, want [\"\"]", got)
		}
	})

	t.Run("no text no files yields no chunks", func(t *testing.T) {
		got := mattermostChunkPlan("", nil)
		want := SplitMarkdown("", mattermostDefaultMaxPostLen)
		if len(got) != len(want) {
			t.Fatalf("chunkPlan(empty) = %v, want passthrough of SplitMarkdown (%v)", got, want)
		}
	})
}

func TestMattermostPromoteRootIDPins(t *testing.T) {
	cases := []struct {
		name       string
		rootID     string
		result     map[string]any
		multiChunk bool
		isFirst    bool
		want       string
	}{
		{"promotes first chunk id", "", map[string]any{"id": "post-1"}, true, true, "post-1"},
		{"keeps explicit root", "root-0", map[string]any{"id": "post-1"}, true, true, "root-0"},
		{"single chunk no promote", "", map[string]any{"id": "post-1"}, false, true, ""},
		{"not first chunk no promote", "", map[string]any{"id": "post-2"}, true, false, ""},
		{"empty id no promote", "", map[string]any{"id": ""}, true, true, ""},
		{"non-string id no promote", "", map[string]any{"id": 42}, true, true, ""},
		{"missing id no promote", "", map[string]any{}, true, true, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := mattermostPromoteRootID(tc.rootID, tc.result, tc.multiChunk, tc.isFirst)
			if got != tc.want {
				t.Fatalf("promoteRootID = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestMattermostBuildChunkPayloadPins(t *testing.T) {
	t.Run("base keys only", func(t *testing.T) {
		a := &mattermostAdapter{replyMode: "thread"}
		got := a.buildChunkPayload("hi", "ch1", "", nil, false)
		if got["channel_id"] != "ch1" || got["message"] != "hi" {
			t.Fatalf("payload = %v, want channel_id/message keys", got)
		}
		if _, ok := got["root_id"]; ok {
			t.Fatalf("payload should not carry root_id without a root: %v", got)
		}
		if _, ok := got["file_ids"]; ok {
			t.Fatalf("payload should not carry file_ids when none given: %v", got)
		}
	})

	t.Run("thread mode attaches root_id", func(t *testing.T) {
		a := &mattermostAdapter{replyMode: "thread"}
		got := a.buildChunkPayload("hi", "ch1", "root-0", nil, false)
		if got["root_id"] != "root-0" {
			t.Fatalf("payload = %v, want root_id=root-0", got)
		}
	})

	t.Run("reply mode off drops root_id", func(t *testing.T) {
		a := &mattermostAdapter{replyMode: "off"}
		got := a.buildChunkPayload("hi", "ch1", "root-0", nil, true)
		if _, ok := got["root_id"]; ok {
			t.Fatalf("payload should drop root_id in off mode: %v", got)
		}
	})

	t.Run("file_ids only on first chunk (#963)", func(t *testing.T) {
		a := &mattermostAdapter{replyMode: "off"}
		files := []string{"f1", "f2"}
		first := a.buildChunkPayload("", "ch1", "", files, true)
		ids, ok := first["file_ids"].([]string)
		if !ok || len(ids) != 2 || ids[0] != "f1" || ids[1] != "f2" {
			t.Fatalf("first chunk payload = %v, want file_ids=[f1 f2]", first)
		}
		later := a.buildChunkPayload("more", "ch1", "", files, false)
		if _, ok := later["file_ids"]; ok {
			t.Fatalf("later chunk must not carry file_ids: %v", later)
		}
	})
}

func TestMattermostWaitInterMessageDelayCancelled(t *testing.T) {
	a := &mattermostAdapter{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan error, 1)
	go func() { done <- a.waitInterMessageDelay(ctx) }()
	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatalf("waitInterMessageDelay = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("waitInterMessageDelay did not observe cancellation")
	}
}

func newMattermostSendTestServer(t *testing.T, status int, resp func(i int) map[string]any) (*httptest.Server, *[][]byte) {
	t.Helper()
	var mu sync.Mutex
	var bodies [][]byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		i := len(bodies)
		bodies = append(bodies, body)
		mu.Unlock()
		if !strings.HasPrefix(r.URL.Path, "/api/v4/posts") {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer tok" {
			t.Errorf("missing bearer auth: %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(resp(i))
	}))
	t.Cleanup(srv.Close)
	mu.Lock()
	defer mu.Unlock()
	captured := &bodies
	return srv, captured
}

func decodeMattermostPayload(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	return m
}

func TestMattermostSendTextWithFilesEndToEnd(t *testing.T) {
	srv, bodies := newMattermostSendTestServer(t, http.StatusOK, func(i int) map[string]any {
		return map[string]any{"id": "post-" + string(rune('1'+i))}
	})
	a := &mattermostAdapter{name: "mm", baseURL: srv.URL, token: "tok", conn: srv.Client(), replyMode: "thread"}

	long := strings.Repeat("a", mattermostDefaultMaxPostLen+16) + "\n\n" + strings.Repeat("b", mattermostDefaultMaxPostLen+16)
	if err := a.sendTextWithFiles(context.Background(), "ch1", "", long, []string{"f1"}); err != nil {
		t.Fatalf("sendTextWithFiles: %v", err)
	}
	if len(*bodies) < 2 {
		t.Fatalf("expected multi-chunk send, got %d posts", len(*bodies))
	}
	first := decodeMattermostPayload(t, (*bodies)[0])
	ids, ok := first["file_ids"].([]any)
	if !ok || len(ids) != 1 || ids[0] != "f1" {
		t.Fatalf("first chunk = %v, want file_ids=[f1]", first)
	}
	for i, raw := range (*bodies)[1:] {
		p := decodeMattermostPayload(t, raw)
		if _, ok := p["file_ids"]; ok {
			t.Fatalf("chunk %d must not carry file_ids: %v", i+1, p)
		}
		if p["root_id"] != "post-1" {
			t.Fatalf("chunk %d root_id = %v, want post-1", i+1, p["root_id"])
		}
	}
}

func TestMattermostSendTextWithFilesGuardsAndFilesOnly(t *testing.T) {
	srv, bodies := newMattermostSendTestServer(t, http.StatusOK, func(int) map[string]any {
		return map[string]any{"id": "post-1"}
	})
	a := &mattermostAdapter{name: "mm", baseURL: srv.URL, token: "tok", conn: srv.Client(), replyMode: "off"}

	if err := a.sendTextWithFiles(context.Background(), "ch1", "", "", nil); err != nil {
		t.Fatalf("empty send: %v", err)
	}
	if err := a.sendTextWithFiles(context.Background(), "", "", "text", nil); err != nil {
		t.Fatalf("empty channel: %v", err)
	}
	if len(*bodies) != 0 {
		t.Fatalf("guards must not hit the wire, got %d posts", len(*bodies))
	}

	if err := a.sendTextWithFiles(context.Background(), "ch1", "", "", []string{"f1"}); err != nil {
		t.Fatalf("files-only send: %v", err)
	}
	if len(*bodies) != 1 {
		t.Fatalf("files-only send must post exactly once, got %d", len(*bodies))
	}
	p := decodeMattermostPayload(t, (*bodies)[0])
	if p["message"] != "" {
		t.Fatalf("files-only message = %v, want empty string", p["message"])
	}
	if ids, ok := p["file_ids"].([]any); !ok || len(ids) != 1 || ids[0] != "f1" {
		t.Fatalf("files-only payload = %v, want file_ids=[f1]", p)
	}
}

func TestMattermostSendChunkErrorPin(t *testing.T) {
	srv, _ := newMattermostSendTestServer(t, http.StatusInternalServerError, func(int) map[string]any {
		return map[string]any{"message": "boom"}
	})
	a := &mattermostAdapter{name: "mm", baseURL: srv.URL, token: "tok", conn: srv.Client(), replyMode: "off"}
	err := a.sendTextWithFiles(context.Background(), "ch1", "", "hi", nil)
	if err == nil || !strings.Contains(err.Error(), "Mattermost send chunk 1/1") {
		t.Fatalf("err = %v, want wrapped 'Mattermost send chunk 1/1'", err)
	}
}
