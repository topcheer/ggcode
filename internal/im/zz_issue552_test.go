package im

// Issue #552-D: a StreamEventError mid-run must reset the round buffer so
// partial text from the failed attempt is not concatenated with the next
// round's text in the same IM bubble.

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/session"
	"github.com/topcheer/ggcode/internal/tool"
)

func TestIssue552D_StreamErrorResetsRoundBuffer(t *testing.T) {
	store, err := session.NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ses := session.NewSession("openai", "api", "test-model")
	prov := &daemonBridgeMetricsProvider{
		events: []provider.StreamEvent{
			{Type: provider.StreamEventText, Text: "LEFTOVER_HALF_SENTENCE"},
			{Type: provider.StreamEventError, Error: errors.New("boom")},
			{Type: provider.StreamEventDone},
		},
	}
	ag := agent.NewAgent(prov, tool.NewRegistry(), "", 3)
	mgr := NewManager()
	sink := &namedCaptureSink{name: "telegram"}
	mgr.RegisterSink(sink)
	mgr.currentBindings["telegram"] = &ChannelBinding{Adapter: "telegram", ChannelID: "ch1"}
	emitter := NewIMEmitter(mgr, "en", t.TempDir())
	bridge := NewDaemonBridge(mgr, ag, emitter, store, ses)
	defer bridge.Close()

	if err := bridge.SubmitInboundMessage(t.Context(), InboundMessage{
		Text:     "run",
		Envelope: Envelope{Adapter: "telegram", Platform: PlatformTelegram},
	}); err != nil {
		t.Fatalf("SubmitInboundMessage: %v", err)
	}

	time.Sleep(300 * time.Millisecond)

	var sawError, sawLeftover bool
	for _, ev := range sink.events() {
		if ev.Kind == OutboundEventText {
			if strings.Contains(ev.Text, "LEFTOVER_HALF_SENTENCE") {
				sawLeftover = true
			}
			// UserFacingError maps a generic error to the zh-CN fallback
			// "请求失败，请稍后重试" — assert on that, not the raw error.
			if strings.Contains(ev.Text, "请求失败") {
				sawError = true
			}
		}
	}
	if !sawError {
		t.Error("expected the stream error to be emitted to IM")
	}
	if sawLeftover {
		t.Fatal("REGRESSION: failed round's partial text leaked into the IM bubble (#552-D)")
	}
}
