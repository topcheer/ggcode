package im

import (
	"context"
	"strings"
	"testing"
	"time"
)

// Pins for the WechatAdapter.Send seams extracted from the outbound send
// path (r189 behavior-preserving split). The orchestrator keeps the gate
// order: empty bot_token → #973 context_token age → image pass → empty-text
// gate (#1250) → chunk cap (#973/#1567-B/#1599-A) → chunk loop.

func TestWechatTokenExpired(t *testing.T) {
	now := time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name         string
		contextToken string
		updatedAt    time.Time
		wantExpired  bool
		wantAgeZero  bool
	}{
		{"fresh within ttl", "tok", now.Add(-time.Hour), false, false},
		{"boundary exactly ttl is not expired", "tok", now.Add(-wechatContextTokenTTL), false, false},
		{"aged past ttl", "tok", now.Add(-wechatContextTokenTTL - time.Minute), true, false},
		{"empty token never expires", "", now.Add(-48 * time.Hour), false, true},
		{"zero timestamp legacy allowed", "tok", time.Time{}, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			age, expired := wechatTokenExpired(tc.contextToken, tc.updatedAt, now)
			if expired != tc.wantExpired {
				t.Fatalf("expired = %v, want %v", expired, tc.wantExpired)
			}
			if tc.wantAgeZero && age != 0 {
				t.Fatalf("age = %v, want 0", age)
			}
			if !tc.wantAgeZero && age <= 0 {
				t.Fatalf("age = %v, want > 0", age)
			}
		})
	}
}

func TestWechatEmptyTextResult(t *testing.T) {
	cases := []struct {
		name      string
		sentImage bool
		attempted int
		images    int
		wantErr   string
	}{
		{"image delivered counts as success", true, 1, 1, ""},
		{"text-only no images counts as success", false, 0, 0, ""},
		{"all attempted failed fails honestly", false, 3, 3, "wechat: all 3 image(s) failed to send via iLink"},
		{"undeliverable without attempt fails honestly", false, 0, 2, "wechat: 2 image(s) not deliverable via iLink (need public URLs)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := wechatEmptyTextResult(tc.sentImage, tc.attempted, tc.images)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("err = %v, want nil", err)
				}
				return
			}
			if err == nil || err.Error() != tc.wantErr {
				t.Fatalf("err = %v, want %q", err, tc.wantErr)
			}
		})
	}
}

func TestWechatCapChunks(t *testing.T) {
	t.Run("under quota unchanged", func(t *testing.T) {
		got := wechatCapChunks("wx", []string{"a", "b", "c"}, 0)
		if strings.Join(got, "|") != "a|b|c" {
			t.Fatalf("got %v", got)
		}
	})
	t.Run("capped to quota with truncation notice", func(t *testing.T) {
		in := []string{"1", "2", "3", "4", "5", "6", "7"}
		got := wechatCapChunks("wx", in, 0)
		if len(got) != wechatMaxChunksPerSend {
			t.Fatalf("len = %d, want %d", len(got), wechatMaxChunksPerSend)
		}
		if got[len(got)-1] != "5"+wechatTruncateNotice {
			t.Fatalf("last = %q, want %q", got[len(got)-1], "5"+wechatTruncateNotice)
		}
	})
	t.Run("sent images shrink text quota", func(t *testing.T) {
		got := wechatCapChunks("wx", []string{"1", "2", "3"}, 4)
		if len(got) != 1 || got[0] != "1"+wechatTruncateNotice {
			t.Fatalf("got %v", got)
		}
	})
	t.Run("quota floors at one", func(t *testing.T) {
		got := wechatCapChunks("wx", []string{"1", "2"}, wechatMaxChunksPerSend)
		if len(got) != 1 || got[0] != "1"+wechatTruncateNotice {
			t.Fatalf("got %v", got)
		}
	})
	t.Run("overflow tail truncated to platform limit", func(t *testing.T) {
		maxBytes := PlatformLimits[PlatformWechat]
		if maxBytes <= 0 {
			t.Skip("platform limit unset")
		}
		big := strings.Repeat("x", maxBytes) // last chunk cannot fit the notice
		in := []string{"1", "2", "3", "4", big, "6"}
		got := wechatCapChunks("wx", in, 0)
		if len(got) != wechatMaxChunksPerSend {
			t.Fatalf("len = %d, want %d", len(got), wechatMaxChunksPerSend)
		}
		last := got[len(got)-1]
		if len(last) > maxBytes {
			t.Fatalf("last chunk %d bytes > limit %d", len(last), maxBytes)
		}
		if !strings.HasSuffix(last, wechatTruncateNotice) {
			t.Fatalf("last chunk %q missing truncation notice", last)
		}
	})
	t.Run("indexes by effective quota not raw constant", func(t *testing.T) {
		maxBytes := PlatformLimits[PlatformWechat]
		if maxBytes <= 0 {
			t.Skip("platform limit unset")
		}
		big := strings.Repeat("x", maxBytes)
		// sentImages=2 → quota 3; the truncation must target chunks[2] (the
		// big chunk), not the raw constant's fixed index chunks[4].
		in := []string{"1", "2", big, "4", "5", "6"}
		got := wechatCapChunks("wx", in, 2)
		if len(got) != 3 {
			t.Fatalf("len = %d, want 3", len(got))
		}
		if got[2] == "5"+wechatTruncateNotice || got[2] == "6"+wechatTruncateNotice {
			t.Fatalf("cap indexed past the effective quota: got %q", got[2])
		}
		if !strings.HasSuffix(got[2], wechatTruncateNotice) || len(got[2]) > maxBytes {
			t.Fatalf("expected truncated big chunk with notice, got %q (%d bytes)", got[2], len(got[2]))
		}
	})
}

func TestWechatSendGates(t *testing.T) {
	t.Run("empty bot token rejected", func(t *testing.T) {
		a := &WechatAdapter{name: "wx"}
		err := a.Send(context.Background(), ChannelBinding{}, OutboundEvent{Kind: OutboundEventText})
		if err == nil || !strings.Contains(err.Error(), "no bot_token") {
			t.Fatalf("err = %v, want no bot_token error", err)
		}
	})
	t.Run("expired context token rejected", func(t *testing.T) {
		a := &WechatAdapter{name: "wx", botToken: "tok"}
		binding := ChannelBinding{
			ContextToken:          "ctx",
			ContextTokenUpdatedAt: time.Now().Add(-wechatContextTokenTTL - time.Minute),
		}
		err := a.Send(context.Background(), binding, OutboundEvent{Kind: OutboundEventText})
		if err == nil || !strings.Contains(err.Error(), "context_token expired") {
			t.Fatalf("err = %v, want context_token expired error", err)
		}
	})
	t.Run("legacy zero timestamp passes gate and empty text skips cleanly", func(t *testing.T) {
		a := &WechatAdapter{name: "wx", botToken: "tok"}
		// Zero ContextTokenUpdatedAt = unknown-age binding: the #973 gate must
		// let it through; an empty text event with no images is then counted
		// as delivered and Send returns nil without any network activity.
		binding := ChannelBinding{ContextToken: "ctx"}
		if err := a.Send(context.Background(), binding, OutboundEvent{Kind: OutboundEventText}); err != nil {
			t.Fatalf("err = %v, want nil (gate must pass and empty text must skip)", err)
		}
	})
}
