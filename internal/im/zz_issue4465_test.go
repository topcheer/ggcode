package im

import (
	"testing"
	"time"
)

// #4465: seen must only be written after the allowed-users / mention /
// empty-text filters pass. A filtered message must NOT occupy dedup capacity.
func TestMattermostSeenNotWrittenForFilteredMessages(t *testing.T) {
	a := &mattermostAdapter{
		botUserID:      "bot123",
		botUsername:    "testbot",
		requireMention: true,
		freeChannels:   []string{"free-ch"},
		seen:           make(map[string]time.Time),
		manager:        nil,
	}

	mkEvent := func(postID, channelType, channelID, message string) map[string]any {
		return map[string]any{
			"event": "posted",
			"data": map[string]any{
				"post":         `{"id":"` + postID + `","user_id":"user1","message":"` + message + `","channel_id":"` + channelID + `"}`,
				"channel_type": channelType,
				"sender_name":  "testuser",
			},
		}
	}

	a.handleWSEvent(nil, mkEvent("p-filtered", "O", "ch1", "hello"))
	if _, ok := a.seen["p-filtered"]; ok {
		t.Errorf("filtered (no-mention) message must not be marked seen, seen map = %v", a.seen)
	}

	a.handleWSEvent(nil, mkEvent("p-live", "D", "dm1", "hello"))
	if _, ok := a.seen["p-live"]; !ok {
		t.Errorf("message that passed all filters must be marked seen, seen map = %v", a.seen)
	}

	before := len(a.seen)
	a.handleWSEvent(nil, mkEvent("p-live", "D", "dm1", "hello"))
	if len(a.seen) != before {
		t.Errorf("replayed post changed seen size: before=%d after=%d", before, len(a.seen))
	}
}
