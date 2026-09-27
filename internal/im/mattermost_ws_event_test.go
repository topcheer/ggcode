package im

import (
	"fmt"
	"testing"
	"time"
)

// Pins for the r176 behavior-preserving split of mattermostAdapter.handleWSEvent:
// every seam extracted from the original monolithic handler is exercised as a
// pure unit, and the gate ordering is pinned through the orchestrator with a
// nil manager (same harness style as TestMattermostWSEventParsing).

func TestParsePostedPostSeam(t *testing.T) {
	post, ok := parsePostedPost(map[string]any{
		"post": `{"id":"p1","user_id":"u1","message":"hi"}`,
	})
	if !ok || post == nil {
		t.Fatalf("valid post: ok=%v post=%v, want decoded post", ok, post)
	}
	if post["id"] != "p1" {
		t.Fatalf("valid post: id=%v, want p1", post["id"])
	}

	cases := []struct {
		name string
		data map[string]any
	}{
		{"nil data", nil},
		{"missing post", map[string]any{}},
		{"empty post string", map[string]any{"post": ""}},
		{"non-string post", map[string]any{"post": 42}},
		{"malformed json", map[string]any{"post": `{"id":`}},
	}
	for _, tc := range cases {
		if post, ok := parsePostedPost(tc.data); ok || post != nil {
			t.Fatalf("%s: ok=%v post=%v, want nil,false", tc.name, ok, post)
		}
	}
}

func TestMattermostIsIgnorablePostSeam(t *testing.T) {
	a := &mattermostAdapter{botUserID: "bot123"}
	normal := map[string]any{"id": "p1", "user_id": "u1"}
	if a.isIgnorablePost("u1", normal) {
		t.Fatal("normal post must not be ignored")
	}

	if !a.isIgnorablePost("bot123", map[string]any{"id": "p2"}) {
		t.Fatal("own message must be ignored")
	}
	if !a.isIgnorablePost("u1", map[string]any{"id": "p3", "type": "system_join_channel"}) {
		t.Fatal("system post must be ignored")
	}
	if !a.isIgnorablePost("u1", map[string]any{"user_id": "u1"}) {
		t.Fatal("post without id must be ignored")
	}
	// Pin the original zero-value comparison: userID "" == botUserID "" drops.
	zero := &mattermostAdapter{}
	if !zero.isIgnorablePost("", map[string]any{"id": "p4"}) {
		t.Fatal("empty userID vs empty botUserID must be ignored (original == semantics)")
	}
}

func TestMattermostMarkPostSeenSeam(t *testing.T) {
	a := &mattermostAdapter{seen: make(map[string]time.Time)}

	if a.markPostSeen("p1") {
		t.Fatal("first sighting must not be reported as duplicate")
	}
	if !a.markPostSeen("p1") {
		t.Fatal("second sighting must be reported as duplicate")
	}
	if len(a.seen) != 1 {
		t.Fatalf("seen len=%d, want 1 (duplicates never double-insert)", len(a.seen))
	}

	// Prune pin: entries older than the 5-minute cutoff are dropped when the
	// set outgrows mattermostDedupMaxSize.
	a.seen = make(map[string]time.Time)
	old := time.Now().Add(-10 * time.Minute)
	for i := 0; i < mattermostDedupMaxSize; i++ {
		a.seen[fmt.Sprintf("old-%d", i)] = old
	}
	if a.markPostSeen("fresh-old-prune") {
		t.Fatal("new entry must not be duplicate")
	}
	if len(a.seen) != 1 {
		t.Fatalf("after prune len=%d, want 1 (all stale entries dropped)", len(a.seen))
	}
	if _, ok := a.seen["fresh-old-prune"]; !ok {
		t.Fatal("newly seen entry must survive prune")
	}

	// Eviction pin (#963): a burst of fresh entries keeps the new entry; the
	// oldest entries are evicted until the map is back under the cap.
	fresh := time.Now()
	a.seen = make(map[string]time.Time)
	for i := 0; i < mattermostDedupMaxSize; i++ {
		a.seen[fmt.Sprintf("burst-%d", i)] = fresh
	}
	if a.markPostSeen("burst-new") {
		t.Fatal("burst new entry must not be duplicate")
	}
	if len(a.seen) > mattermostDedupMaxSize {
		t.Fatalf("after eviction len=%d, want <= %d", len(a.seen), mattermostDedupMaxSize)
	}
	if _, ok := a.seen["burst-new"]; !ok {
		t.Fatal("burst new entry must survive eviction")
	}
}

func TestMattermostApplyInboundPolicySeam(t *testing.T) {
	a := &mattermostAdapter{
		botUserID:      "bot123",
		botUsername:    "testbot",
		requireMention: true,
		freeChannels:   []string{"free-ch"},
		allowedUsers:   []string{"user1"},
	}

	text, ok := a.applyInboundPolicy("hello", "user1", "ch1", true)
	if !ok || text != "hello" {
		t.Fatalf("DM allowed user: ok=%v text=%q, want true/hello", ok, text)
	}

	if _, ok := a.applyInboundPolicy("hello", "intruder", "ch1", true); ok {
		t.Fatal("user outside allowed list must be dropped")
	}

	// Mirrors TestMattermostRequireMentionPolicy expectations at seam level.
	cases := []struct {
		name        string
		channelType string // "D" = DM
		channelID   string
		message     string
		wantOK      bool
		wantText    string
	}{
		{"DM no mention needed", "D", "dm1", "hello", true, "hello"},
		{"Channel with mention", "O", "ch1", "@testbot hello", true, "hello"},
		{"Channel without mention", "O", "ch1", "hello", false, ""},
		{"Free channel no mention", "O", "free-ch", "hello", true, "hello"},
		{"Group without mention", "G", "grp1", "hello", false, ""},
		{"Group with mention", "G", "grp1", "@testbot hello", true, "hello"},
	}
	for _, tc := range cases {
		text, ok := a.applyInboundPolicy(tc.message, "user1", tc.channelID, tc.channelType == "D")
		if ok != tc.wantOK {
			t.Fatalf("%s: ok=%v, want %v", tc.name, ok, tc.wantOK)
		}
		if ok && text != tc.wantText {
			t.Fatalf("%s: text=%q, want %q", tc.name, text, tc.wantText)
		}
	}

	// require_mention disabled: non-DM passes untouched.
	a.requireMention = false
	if text, ok := a.applyInboundPolicy("hello", "user1", "ch1", false); !ok || text != "hello" {
		t.Fatalf("requireMention=false: ok=%v text=%q, want true/hello", ok, text)
	}
}

func TestMattermostAttachmentsFromPostSeam(t *testing.T) {
	post := map[string]any{
		"file_ids": []any{"f1", "", 42, "f2"},
	}
	atts := attachmentsFromPost(post, "https://mm.example.com")
	if len(atts) != 2 {
		t.Fatalf("len=%d, want 2 (empty and non-string ids skipped)", len(atts))
	}
	for i, want := range []string{"f1", "f2"} {
		if atts[i].Kind != AttachmentFile {
			t.Fatalf("att[%d].Kind=%v, want AttachmentFile", i, atts[i].Kind)
		}
		if atts[i].Name != want {
			t.Fatalf("att[%d].Name=%q, want %q", i, atts[i].Name, want)
		}
		wantURL := "https://mm.example.com/" + mattermostAPIVersion + "/files/" + want
		if atts[i].URL != wantURL {
			t.Fatalf("att[%d].URL=%q, want %q", i, atts[i].URL, wantURL)
		}
	}
	if got := attachmentsFromPost(map[string]any{}, "https://mm.example.com"); got != nil {
		t.Fatalf("no file_ids: %v, want nil", got)
	}
}

func TestMattermostBuildInboundMessageSeam(t *testing.T) {
	a := &mattermostAdapter{name: "mm1", baseURL: "https://mm.example.com"}
	post := map[string]any{
		"id":       "p1",
		"user_id":  "u1",
		"root_id":  "root9",
		"file_ids": []any{"f1"},
	}
	msg := a.buildInboundMessage(post, "ch1", "alice", "  hello  ")
	env := msg.Envelope
	if env.Adapter != "mm1" || env.Platform != PlatformMattermost {
		t.Fatalf("adapter/platform = %s/%v", env.Adapter, env.Platform)
	}
	if env.ChannelID != "ch1" || env.ThreadID != "root9" || env.SenderID != "u1" {
		t.Fatalf("channel/thread/sender = %s/%s/%s", env.ChannelID, env.ThreadID, env.SenderID)
	}
	if env.SenderName != "alice" || env.MessageID != "p1" {
		t.Fatalf("senderName/messageID = %s/%s", env.SenderName, env.MessageID)
	}
	if env.ReceivedAt.IsZero() {
		t.Fatal("ReceivedAt must be stamped")
	}
	if msg.Text != "hello" {
		t.Fatalf("Text=%q, want trimmed %q", msg.Text, "hello")
	}
	if len(msg.Attachments) != 1 || msg.Attachments[0].Name != "f1" {
		t.Fatalf("Attachments=%v, want one file f1", msg.Attachments)
	}
}

func TestMattermostPairingAndDeliverNilManagerSeam(t *testing.T) {
	a := &mattermostAdapter{seen: make(map[string]time.Time)}
	msg := InboundMessage{}
	if a.pairingConsumedMessage(nil, msg, "ch1", "t1") {
		t.Fatal("nil manager: pairing must not consume")
	}
	a.deliverInbound(nil, msg) // must not panic
}

func TestMattermostHandleWSEventGateOrderingPins(t *testing.T) {
	a := &mattermostAdapter{
		botUserID:   "bot123",
		botUsername: "testbot",
		seen:        make(map[string]time.Time),
		manager:     nil,
	}

	mkEvent := func(id, userID, message, postType string) map[string]any {
		post := `{"id":"` + id + `","user_id":"` + userID + `","message":"` + message + `","channel_id":"ch1"`
		if postType != "" {
			post += `,"type":"` + postType + `"`
		}
		post += `}`
		return map[string]any{
			"event": "posted",
			"data": map[string]any{
				"post":         post,
				"channel_type": "O",
				"sender_name":  "@carol",
			},
		}
	}

	// Non-posted events: silent no-op, dedup map untouched.
	a.handleWSEvent(nil, map[string]any{"event": "status_change", "data": map[string]any{}})
	if len(a.seen) != 0 {
		t.Fatalf("non-posted event touched seen: %v", a.seen)
	}

	// Own message: dropped before dedup.
	a.handleWSEvent(nil, mkEvent("p-own", "bot123", "self", ""))
	if len(a.seen) != 0 {
		t.Fatalf("own message reached dedup: %v", a.seen)
	}

	// System post: dropped before dedup.
	a.handleWSEvent(nil, mkEvent("p-sys", "u1", "joined", "system_join_channel"))
	if len(a.seen) != 0 {
		t.Fatalf("system post reached dedup: %v", a.seen)
	}

	// Normal post: passes all gates, lands in dedup exactly once.
	a.handleWSEvent(nil, mkEvent("p1", "u1", "hello", ""))
	if len(a.seen) != 1 {
		t.Fatalf("normal post seen len=%d, want 1", len(a.seen))
	}
	// Duplicate delivery: dedup intercepts, no second insert.
	a.handleWSEvent(nil, mkEvent("p1", "u1", "hello", ""))
	if len(a.seen) != 1 {
		t.Fatalf("duplicate delivery len=%d, want 1", len(a.seen))
	}
}
