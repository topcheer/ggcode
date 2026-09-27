package im

import (
	"context"
	"testing"
)

// Pin tests for the r188 handleUpdate decomposition. These lock the pure-seam
// behavior (envelope extraction, text merging, group mention stripping,
// private-chat text assembly) so the refactor stays behavior-preserving.

func TestTGMessageMetaExtraction(t *testing.T) {
	cases := []struct {
		name     string
		msg      map[string]any
		wantOK   bool
		wantMeta tgMessageMeta
	}{
		{
			name: "full sender first+last name joined and trimmed",
			msg: map[string]any{
				"message_id": float64(42),
				"chat":       map[string]any{"id": float64(100), "type": "private"},
				"from":       map[string]any{"id": float64(7), "first_name": "Ada", "last_name": "Lovelace"},
			},
			wantOK:   true,
			wantMeta: tgMessageMeta{msgID: "42", chatID: "100", chatType: "private", senderID: "7", senderName: "Ada Lovelace"},
		},
		{
			name: "username fallback only when name empty",
			msg: map[string]any{
				"message_id": float64(1),
				"chat":       map[string]any{"id": float64(2), "type": "group"},
				"from":       map[string]any{"id": float64(7), "username": "ada"},
			},
			wantOK:   true,
			wantMeta: tgMessageMeta{msgID: "1", chatID: "2", chatType: "group", senderID: "7", senderName: "ada"},
		},
		{
			name: "real name wins over username",
			msg: map[string]any{
				"message_id": float64(1),
				"chat":       map[string]any{"id": float64(2), "type": "supergroup"},
				"from":       map[string]any{"id": float64(7), "first_name": "Ada", "username": "ada"},
			},
			wantOK:   true,
			wantMeta: tgMessageMeta{msgID: "1", chatID: "2", chatType: "supergroup", senderID: "7", senderName: "Ada"},
		},
		{
			name: "nil from yields empty sender fields",
			msg: map[string]any{
				"message_id": float64(3),
				"chat":       map[string]any{"id": float64(4), "type": "private"},
			},
			wantOK:   true,
			wantMeta: tgMessageMeta{msgID: "3", chatID: "4", chatType: "private"},
		},
		{
			name:   "nil chat rejected",
			msg:    map[string]any{"message_id": float64(5), "chat": nil},
			wantOK: false,
		},
		{
			name: "missing chat type defaults to empty",
			msg: map[string]any{
				"message_id": float64(6),
				"chat":       map[string]any{"id": float64(8)},
			},
			wantOK:   true,
			wantMeta: tgMessageMeta{msgID: "6", chatID: "8"},
		},
		{
			name: "missing message_id falls to default formatter",
			msg: map[string]any{
				"chat": map[string]any{"id": float64(9), "type": "private"},
			},
			wantOK:   true,
			wantMeta: tgMessageMeta{msgID: "<nil>", chatID: "9", chatType: "private"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			meta, ok := tgExtractMessageMeta(tc.msg)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if !tc.wantOK {
				return
			}
			if meta != tc.wantMeta {
				t.Fatalf("meta = %+v, want %+v", meta, tc.wantMeta)
			}
		})
	}
}

func TestTGMergeTextExtra(t *testing.T) {
	cases := []struct {
		text, extra, want string
	}{
		{"", "", ""},
		{"a", "", "a"},
		{"", "b", "b"},
		{"a", "b", "a\n\nb"},
	}
	for _, tc := range cases {
		if got := tgMergeTextExtra(tc.text, tc.extra); got != tc.want {
			t.Fatalf("tgMergeTextExtra(%q, %q) = %q, want %q", tc.text, tc.extra, got, tc.want)
		}
	}
}

func TestTGStripGroupMentions(t *testing.T) {
	mention := func(offset, length int) []any {
		return []any{map[string]any{"type": "mention", "offset": float64(offset), "length": float64(length)}}
	}
	a := &tgAdapter{botUsername: "dev"}

	t.Run("entities strip exact mention from text and caption", func(t *testing.T) {
		msg := map[string]any{
			"entities":         mention(0, 4),
			"caption_entities": mention(0, 4),
		}
		text, caption := a.stripGroupMentions(msg, "@dev hi", "@dev img")
		if text != "hi" {
			t.Fatalf("text = %q, want %q", text, "hi")
		}
		if caption != "img" {
			t.Fatalf("caption = %q, want %q", caption, "img")
		}
	})

	t.Run("empty caption stays untouched", func(t *testing.T) {
		msg := map[string]any{"entities": mention(0, 4)}
		text, caption := a.stripGroupMentions(msg, "@dev hi", "")
		if text != "hi" {
			t.Fatalf("text = %q, want %q", text, "hi")
		}
		if caption != "" {
			t.Fatalf("caption = %q, want untouched empty", caption)
		}
	})

	t.Run("text without mention returned untouched", func(t *testing.T) {
		msg := map[string]any{"entities": mention(0, 4)}
		text, caption := a.stripGroupMentions(msg, "plain words", "")
		if text != "plain words" {
			t.Fatalf("text = %q, want untouched", text)
		}
		if caption != "" {
			t.Fatalf("caption = %q, want untouched empty", caption)
		}
	})

	t.Run("empty bot username leaves text untouched", func(t *testing.T) {
		zero := &tgAdapter{}
		msg := map[string]any{"entities": mention(0, 4)}
		text, caption := zero.stripGroupMentions(msg, "@dev hi", "cap")
		if text != "@dev hi" || caption != "cap" {
			t.Fatalf("got (%q, %q), want untouched", text, caption)
		}
	})
}

func TestTGAssembleInboundTextPrivateChat(t *testing.T) {
	a := &tgAdapter{name: "tg"}
	ctx := context.Background()
	meta := tgMessageMeta{chatID: "1", chatType: "private"}
	msg := map[string]any{"text": " hello ", "caption": " world "}
	text, attachments := a.assembleInboundText(ctx, msg, meta)
	if text != "hello\n\nworld" {
		t.Fatalf("text = %q, want %q", text, "hello\n\nworld")
	}
	if len(attachments) != 0 {
		t.Fatalf("attachments = %v, want none", attachments)
	}
}

func TestTGAssembleInboundTextGroupChatStripsMention(t *testing.T) {
	a := &tgAdapter{name: "tg", botUsername: "dev"}
	ctx := context.Background()
	meta := tgMessageMeta{chatID: "1", chatType: "group"}
	msg := map[string]any{
		"text":     "@dev run the tests",
		"entities": []any{map[string]any{"type": "mention", "offset": float64(0), "length": float64(4)}},
	}
	text, _ := a.assembleInboundText(ctx, msg, meta)
	if text != "run the tests" {
		t.Fatalf("text = %q, want %q", text, "run the tests")
	}
}

func TestTGHandleUpdateGatesNoPanic(t *testing.T) {
	a := &tgAdapter{name: "tg"}
	ctx := context.Background()

	// Empty update: no callback_query, no message -> silent return.
	a.handleUpdate(ctx, map[string]any{})

	// Message without chat object and update_id <= 0 (seenUpdate early gate):
	// drops at the envelope-extraction seam without touching the manager
	// (nil manager would panic if reached) and without recording dedup state.
	a.handleUpdate(ctx, map[string]any{
		"update_id": float64(0),
		"message":   map[string]any{"message_id": float64(1)},
	})
	if len(a.seen) != 0 {
		t.Fatalf("seen = %v, want empty (updateID<=0 must not be recorded)", a.seen)
	}
}
