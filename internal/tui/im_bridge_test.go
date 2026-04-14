package tui

import (
	"context"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/im"
	"github.com/topcheer/ggcode/internal/session"
)

func TestBuildRemoteInboundPromptDedupesVoiceTranscript(t *testing.T) {
	got := buildRemoteInboundPrompt(im.InboundMessage{
		Attachments: []im.Attachment{
			{Kind: im.AttachmentVoice, Transcript: "再帮我看一下STT的东西。"},
		},
	})
	if got != "再帮我看一下STT的东西。" {
		t.Fatalf("unexpected remote inbound prompt: %q", got)
	}
}

type tuiTestSink struct {
	name   string
	events []im.OutboundEvent
}

func (s *tuiTestSink) Name() string { return s.name }

func (s *tuiTestSink) Send(_ context.Context, _ im.ChannelBinding, event im.OutboundEvent) error {
	s.events = append(s.events, event)
	return nil
}

func TestRemoteInboundProviderCommandEmitsSummary(t *testing.T) {
	m := NewModel(nil, nil)
	m.SetConfig(config.DefaultConfig())
	imMgr := im.NewManager()
	if err := imMgr.SetBindingStore(im.NewMemoryBindingStore()); err != nil {
		t.Fatalf("SetBindingStore returned error: %v", err)
	}
	sink := &tuiTestSink{name: "qq-bot-1"}
	imMgr.RegisterSink(sink)
	m.SetIMManager(imMgr)
	store, err := session.NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewJSONLStore returned error: %v", err)
	}
	ses := session.NewSession("zai", "cn-coding-openai", "glm-5-turbo")
	m.SetSession(ses, store)
	if _, err := imMgr.BindChannel(im.ChannelBinding{
		Platform:  im.PlatformQQ,
		Adapter:   "qq-bot-1",
		TargetID:  "ops",
		ChannelID: "group-1",
	}); err != nil {
		t.Fatalf("BindChannel returned error: %v", err)
	}

	updated, cmd := m.Update(remoteInboundMsg{
		Message: im.InboundMessage{
			Text: "/provider",
			Envelope: im.Envelope{
				Adapter:   "qq-bot-1",
				Platform:  im.PlatformQQ,
				ChannelID: "group-1",
			},
		},
		Response: make(chan error, 1),
	})
	_ = updated
	if cmd != nil {
		_ = cmd()
	}
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) && len(sink.events) == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	if len(sink.events) == 0 {
		t.Fatal("expected remote /provider to emit at least one IM event")
	}
	if got := sink.events[len(sink.events)-1].Text; got == "" {
		t.Fatalf("expected provider summary text, got %#v", sink.events[len(sink.events)-1])
	}
}
