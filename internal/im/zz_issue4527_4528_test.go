//go:build goolm

package im

import (
	"os"
	"strings"
	"testing"
)

// #4527 pin: the interactive-message path must loop-send ALL formatMessages
// chunks (only messages[0] used to survive) and carry entities in entity
// mode - the plain-text path already did both.
func TestIssue4527_InteractiveSendsAllChunksWithEntities(t *testing.T) {
	b, err := os.ReadFile("tg_adapter.go")
	if err != nil {
		t.Skip("source not readable in this environment")
	}
	s := string(b)
	if strings.Contains(s, "textContent = messages[0].Text") {
		t.Fatal("#4527 regression: interactive path truncates to the first chunk")
	}
	if !strings.Contains(s, "for i := 0; i < len(messages)-1; i++") {
		t.Fatal("#4527 regression: leading chunks not loop-sent")
	}
	// Entity mode must attach entities on the keyboard chunk too.
	if !strings.Contains(s, `body["entities"] = tgEntitiesToRaw(messages[len(messages)-1].Entities)`) {
		t.Fatal("#4527 regression: entity mode drops entities on the button message")
	}
}

// #4528 pin: handleMessage must run asynchronously off the Slack read loop -
// synchronous processing stalled pong-driven deadline renewal (reconnect) and
// envelopes were already acked, so messages in the blocked window were lost.
func TestIssue4528_SlackHandleMessageAsync(t *testing.T) {
	b, err := os.ReadFile("slack_adapter.go")
	if err != nil {
		t.Skip("source not readable in this environment")
	}
	s := string(b)
	if !strings.Contains(s, `safego.Go("im.slack.envelope", func() { a.handleMessage(ctx, ev) })`) {
		t.Fatal("#4528 regression: handleMessage back on the read loop")
	}
	if strings.Contains(s, "\t\t\t\t\ta.handleMessage(ctx, event)\n") {
		t.Fatal("#4528 regression: synchronous handleMessage call remains")
	}
}
