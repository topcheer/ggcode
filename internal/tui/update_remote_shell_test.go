package tui

import (
	"testing"

	"github.com/topcheer/ggcode/internal/im"
)

func TestHandleRemoteInbound_ShellExecutesWhileLoading(t *testing.T) {
	m := newTestModel()
	m.loading = true

	msg := remoteInboundMsg{Message: im.InboundMessage{Text: "$ printf tuishell-ok"}}
	updated, _ := m.handleRemoteInbound(msg, nil)
	m2, ok := updated.(Model)
	if !ok {
		t.Fatalf("expected Model, got %T", updated)
	}

	if got := len(m2.pending.items); got != 0 {
		t.Fatalf("shell passthrough must not queue pending submissions while loading, queued=%d", got)
	}

}
