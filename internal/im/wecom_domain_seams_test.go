package im

import (
	"reflect"
	"testing"
	"time"
)

// r182 pins for the WeCom inbound seams extracted from handleMessage /
// extractAttachments (behavior-preserving split; both were CC 30, the
// internal/im complexity tops at fork point c2e448d0).

func TestWecomMsgIDResolvesBodyThenReqID(t *testing.T) {
	if got := wecomMsgID(map[string]any{"msgid": "m1"}, nil); got != "m1" {
		t.Fatalf("msgid = %q, want m1", got)
	}
	payload := map[string]any{"headers": map[string]any{"req_id": "r1"}}
	if got := wecomMsgID(map[string]any{}, payload); got != "r1" {
		t.Fatalf("req_id fallback = %q, want r1", got)
	}
	if got := wecomMsgID(map[string]any{}, map[string]any{}); got != "" {
		t.Fatalf("empty ids = %q, want empty", got)
	}
}

func TestWecomImageBlockAttachments(t *testing.T) {
	if got := wecomImageBlockAttachments(nil); len(got) != 0 {
		t.Fatalf("nil block = %v, want none", got)
	}
	both := wecomImageBlockAttachments(map[string]any{
		"url":    "https://example.test/a.png",
		"base64": "aGVsbG8=",
	})
	if len(both) != 2 {
		t.Fatalf("both = %d attachments, want 2", len(both))
	}
	if both[0].Kind != AttachmentImage || both[0].URL != "https://example.test/a.png" || both[0].DataBase64 != "" {
		t.Fatalf("first = %+v, want url-only image", both[0])
	}
	if both[1].Kind != AttachmentImage || both[1].DataBase64 != "aGVsbG8=" || both[1].URL != "" {
		t.Fatalf("second = %+v, want base64-only image", both[1])
	}
	urlOnly := wecomImageBlockAttachments(map[string]any{"url": "u"})
	if len(urlOnly) != 1 || urlOnly[0].URL != "u" {
		t.Fatalf("urlOnly = %+v", urlOnly)
	}
	empty := wecomImageBlockAttachments(map[string]any{"url": "", "base64": ""})
	if len(empty) != 0 {
		t.Fatalf("empty fields = %v, want none", empty)
	}
}

func TestWecomMixedImageAttachmentsSkipsNonImageItems(t *testing.T) {
	mixed := map[string]any{"msg_item": []any{
		map[string]any{"msgtype": "text", "text": map[string]any{"content": "hi"}},
		map[string]any{"msgtype": "IMAGE", "image": map[string]any{"url": "https://e/x.png"}},
		nil,
		map[string]any{"msgtype": "image"},
	}}
	got := wecomMixedImageAttachments(mixed)
	if len(got) != 1 || got[0].URL != "https://e/x.png" {
		t.Fatalf("got = %+v, want single url image (case-insensitive, nil-safe)", got)
	}
	if got := wecomMixedImageAttachments(nil); len(got) != 0 {
		t.Fatalf("nil mixed = %v, want none", got)
	}
	if got := wecomMixedImageAttachments(map[string]any{}); len(got) != 0 {
		t.Fatalf("missing msg_item = %v, want none", got)
	}
}

func TestWecomFileURLAttachment(t *testing.T) {
	if got := wecomFileURLAttachment(nil); len(got) != 0 {
		t.Fatalf("nil = %v, want none", got)
	}
	got := wecomFileURLAttachment(map[string]any{"url": "https://e/f.pdf"})
	if len(got) != 1 || got[0].Kind != AttachmentFile || got[0].URL != "https://e/f.pdf" {
		t.Fatalf("got = %+v", got)
	}
	if got := wecomFileURLAttachment(map[string]any{"url": ""}); len(got) != 0 {
		t.Fatalf("empty url = %v, want none", got)
	}
}

func TestWecomAppmsgAttachmentsCarriesTitleAsName(t *testing.T) {
	got := wecomAppmsgAttachments(map[string]any{
		"title": "quarterly.pdf",
		"file":  map[string]any{"url": "https://e/q.pdf"},
		"image": map[string]any{"url": "https://e/cover.png"},
	})
	if len(got) != 2 {
		t.Fatalf("got = %d attachments, want 2", len(got))
	}
	if got[0].Kind != AttachmentFile || got[0].URL != "https://e/q.pdf" || got[0].Name != "quarterly.pdf" {
		t.Fatalf("file = %+v, want Name=quarterly.pdf", got[0])
	}
	if got[1].Kind != AttachmentImage || got[1].URL != "https://e/cover.png" || got[1].Name != "" {
		t.Fatalf("image = %+v, want plain image", got[1])
	}
	if got := wecomAppmsgAttachments(nil); len(got) != 0 {
		t.Fatalf("nil appmsg = %v, want none", got)
	}
}

func TestWecomQuoteAttachments(t *testing.T) {
	img := wecomQuoteAttachments(map[string]any{"msgtype": "image", "image": map[string]any{"url": "u1"}})
	if len(img) != 1 || img[0].Kind != AttachmentImage || img[0].URL != "u1" {
		t.Fatalf("image quote = %+v", img)
	}
	file := wecomQuoteAttachments(map[string]any{"msgtype": "file", "file": map[string]any{"url": "u2"}})
	if len(file) != 1 || file[0].Kind != AttachmentFile || file[0].URL != "u2" {
		t.Fatalf("file quote = %+v", file)
	}
	if got := wecomQuoteAttachments(map[string]any{"msgtype": "text"}); len(got) != 0 {
		t.Fatalf("text quote = %v, want none", got)
	}
	if got := wecomQuoteAttachments(nil); len(got) != 0 {
		t.Fatalf("nil quote = %v, want none", got)
	}
}

func TestWecomInboundSenderChatFallsBackToSender(t *testing.T) {
	sender, chat, isGroup := wecomInboundSender(map[string]any{
		"from":     map[string]any{"userid": "u1"},
		"chatid":   "c1",
		"chattype": "Group",
	})
	if sender != "u1" || chat != "c1" || !isGroup {
		t.Fatalf("got %q %q %v, want u1 c1 true", sender, chat, isGroup)
	}
	sender, chat, isGroup = wecomInboundSender(map[string]any{
		"from":     map[string]any{"userid": "u2"},
		"chattype": "single",
	})
	if sender != "u2" || chat != "u2" || isGroup {
		t.Fatalf("got %q %q %v, want u2 u2 false (DM fallback)", sender, chat, isGroup)
	}
}

func TestWecomInboundMessageEnvelope(t *testing.T) {
	att := []Attachment{{Kind: AttachmentImage, URL: "u"}}
	now := time.Unix(1700000000, 0)
	msg := wecomInboundMessage("adp", "c1", "s1", "hello", att, now)
	if msg.Envelope.Adapter != "adp" || msg.Envelope.Platform != PlatformWeCom || msg.Envelope.ChannelID != "c1" {
		t.Fatalf("envelope = %+v", msg.Envelope)
	}
	if msg.Envelope.SenderID != "s1" || msg.Envelope.SenderName != "s1" || !msg.Envelope.ReceivedAt.Equal(now) {
		t.Fatalf("sender/time = %+v", msg.Envelope)
	}
	if msg.Text != "hello" || !reflect.DeepEqual(msg.Attachments, att) {
		t.Fatalf("payload = %+v", msg)
	}
}

func newWecomSeamAdapter() *wecomAdapter {
	return &wecomAdapter{
		name:          "t",
		seen:          map[string]time.Time{},
		replyReqIDs:   map[string]string{},
		replyReqOrder: []string{},
	}
}

func TestWecomDedupeAndRememberDropsDuplicates(t *testing.T) {
	a := newWecomSeamAdapter()
	if !a.dedupeAndRemember("m1", "") {
		t.Fatal("first m1 must pass")
	}
	if a.dedupeAndRemember("m1", "") {
		t.Fatal("second m1 must be dropped as duplicate")
	}
}

func TestWecomReplyReqEvictionOldestFirst(t *testing.T) {
	a := newWecomSeamAdapter()
	// Fill beyond capacity; oldest-first eviction must keep the newest ids.
	for i := 0; i < wecomDedupMaxSize+1; i++ {
		id := "m" + string(rune('A'+i%26)) + string(rune('a'+i/26))
		if !a.dedupeAndRemember(id, "req-"+id) {
			t.Fatalf("unique id %q dropped", id)
		}
	}
	if len(a.replyReqIDs) > wecomDedupMaxSize {
		t.Fatalf("replyReqIDs len = %d, want <= %d", len(a.replyReqIDs), wecomDedupMaxSize)
	}
	if _, ok := a.replyReqIDs["mAa"]; ok {
		t.Fatal("oldest req id (index 0) survived eviction")
	}
	last := "m" + string(rune('A'+wecomDedupMaxSize%26)) + string(rune('a'+wecomDedupMaxSize/26))
	if _, ok := a.replyReqIDs[last]; !ok {
		t.Fatalf("newest req id %q evicted", last)
	}
}

func TestWecomSeenPruneDropsStaleThenOldest(t *testing.T) {
	// TTL prune: over-capacity map with all-stale entries collapses to the
	// just-inserted one.
	a := newWecomSeamAdapter()
	stale := time.Now().Add(-10 * time.Minute)
	for i := 0; i < wecomDedupMaxSize+1; i++ {
		a.seen["old"+string(rune('A'+i%26))+string(rune('a'+i/26))] = stale
	}
	if !a.dedupeAndRemember("fresh", "") {
		t.Fatal("fresh id must pass")
	}
	if len(a.seen) != 1 {
		t.Fatalf("seen len = %d, want 1 after TTL prune", len(a.seen))
	}
	if _, ok := a.seen["fresh"]; !ok {
		t.Fatal("fresh entry missing")
	}

	// #1567-D oldest-entry fallback: a burst inside the TTL window must not
	// grow unbounded - oldest entries are dropped by timestamp.
	b := newWecomSeamAdapter()
	recent := time.Now().Add(-1 * time.Minute)
	for i := 0; i < wecomDedupMaxSize+1; i++ {
		b.seen["b"+string(rune('A'+i%26))+string(rune('a'+i/26))] = recent
	}
	if !b.dedupeAndRemember("fresh2", "") {
		t.Fatal("fresh2 must pass")
	}
	if len(b.seen) >= wecomDedupMaxSize+1 {
		t.Fatalf("seen len = %d, oldest-drop fallback did not engage", len(b.seen))
	}
	if _, ok := b.seen["fresh2"]; !ok {
		t.Fatal("fresh2 entry missing")
	}
}
