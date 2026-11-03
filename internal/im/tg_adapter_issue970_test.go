package im

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/util"
)

func newTGTestAdapter(apiBase, parseMode string) *tgAdapter {
	a := &tgAdapter{
		name:       "test",
		httpClient: util.NewInsecureAwareClient(5 * time.Second),
		botToken:   "TESTTOKEN",
		apiBase:    apiBase,
		parseMode:  parseMode,
		seen:       make(map[int]time.Time),
	}
	a.mu.Lock()
	a.connected = true
	a.mu.Unlock()
	return a
}

func TestTGSendInteractiveReturnsMessageID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/botTESTTOKEN/sendMessage") {
			http.Error(w, "unexpected path: "+r.URL.Path, http.StatusNotFound)
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if _, ok := body["reply_markup"]; !ok {
			t.Errorf("sendMessage body missing reply_markup (inline keyboard)")
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"ok":true,"result":{"message_id":4242,"chat":{"id":1},"text":"hi"}}`)
	}))
	defer srv.Close()

	a := newTGTestAdapter(srv.URL, "")
	msgID, err := a.SendInteractive(context.Background(), ChannelBinding{ChannelID: "100"}, InteractiveMessage{
		Text: "pick one",
		Buttons: []InteractiveButton{
			{Label: "A", Value: "a"},
			{Label: "B", Value: "b"},
		},
	})
	if err != nil {
		t.Fatalf("SendInteractive: %v", err)
	}
	if msgID == "" {
		t.Fatalf("SendInteractive returned empty message_id; interactive edit/update flows would silently break")
	}
	if msgID != "4242" {
		t.Fatalf("SendInteractive message_id = %q, want 4242", msgID)
	}
}

func TestTGDownloadFileUsesAPIBase(t *testing.T) {
	getFileHit := false
	downloadHit := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/botTESTTOKEN/getFile":
			getFileHit = true
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"ok":true,"result":{"file_id":"f1","file_path":"private/server/file.ogg"}}`)
		case r.URL.Path == "/file/botTESTTOKEN/private/server/file.ogg":
			downloadHit = true
			w.Header().Set("Content-Type", "audio/ogg")
			w.Write([]byte("OGGDATABYTES"))
		default:
			t.Errorf("unexpected request to fake server: %s", r.URL.Path)
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	defer srv.Close()

	a := newTGTestAdapter(srv.URL, "")
	data, mimeType, err := a.downloadTGFile(context.Background(), "f1")
	if err != nil {
		t.Fatalf("downloadTGFile: %v", err)
	}
	if !getFileHit {
		t.Error("getFile was not routed to the fake apiBase server")
	}
	if !downloadHit {
		t.Fatal("file download did not hit <apiBase>/file/bot<token>/<path>; it would have gone to api.telegram.org and 404'd for private Bot API servers")
	}
	if string(data) != "OGGDATABYTES" {
		t.Fatalf("downloaded data = %q", data)
	}
	if mimeType != "audio/ogg" {
		t.Fatalf("mimeType = %q", mimeType)
	}
}

func TestTGFormatMessagesEscapeBeforeSplit(t *testing.T) {
	a := newTGTestAdapter("", "MarkdownV2")

	text := strings.Repeat(".", 4000) + " tail"
	msgs, err := a.formatMessages(text)
	if err != nil {
		t.Fatalf("formatMessages: %v", err)
	}
	if len(msgs) < 2 {
		t.Fatalf("expected multiple chunks for oversized text, got %d", len(msgs))
	}
	var total int
	for i, m := range msgs {
		n := len([]rune(m.Text))
		if n > tgMaxTextLen {
			t.Fatalf("chunk %d has %d runes, exceeds Telegram limit %d (400 'message is too long')", i, n, tgMaxTextLen)
		}
		total += n
	}
	if total < 4000 {
		t.Fatalf("escaped content lost: total %d runes for 4000-char input", total)
	}
}

func TestTGTruncateRunesDanglingEscape(t *testing.T) {
	if got := truncateTGRunes("abc\\", 10); got != "abc\\" {
		t.Fatalf("no-truncation case changed input: %q", got)
	}

	if got := truncateTGRunes("a\\*b", 3); got != "a\\*" {
		t.Fatalf("dangling backslash not dropped: %q", got)
	}
}

func TestTGParseIntOverflow(t *testing.T) {
	if _, err := parseInt("9223372036854775808"); err == nil {
		t.Fatal("parseInt accepted int64 overflow value 2^63")
	}
	if _, err := parseInt("123"); err != nil {
		t.Fatalf("parseInt(123) error: %v", err)
	}
}
