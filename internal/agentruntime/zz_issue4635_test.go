package agentruntime

import (
	"os"
	"strings"
	"testing"
)

// #4635: BindSession and ResetStreamState reset the same stream state
// (currentMsgID / finalized / needsFinalize) that pushTextEvent and the
// rollover path mutate under pushMu - the #4564 contract requires the
// reset to serialize on pushMu too, with pushMu OUTSIDE mu to match
// pushTextEvent's lock order.
func TestIssue4635_StreamResetsHoldPushMu(t *testing.T) {
	data, err := os.ReadFile("tunnel_host.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, fn := range []string{
		"func (h *TunnelHost) BindSession(",
		"func (h *TunnelHost) ResetStreamState(",
	} {
		i := strings.Index(text, fn)
		if i < 0 {
			t.Fatalf("%s not found", fn)
		}
		// Function head up to the currentMsgID reset.
		j := strings.Index(text[i:], `h.currentMsgID = ""`)
		if j < 0 {
			t.Fatalf("stream reset not found in %s", fn)
		}
		head := text[i : i+j]
		pushIdx := strings.Index(head, "h.pushMu.Lock()")
		muIdx := strings.Index(head, "h.mu.Lock()")
		if pushIdx < 0 {
			t.Fatalf("%s must take pushMu before the stream reset (#4635)", fn)
		}
		if muIdx < 0 || muIdx < pushIdx {
			t.Fatalf("%s lock order must be pushMu then mu (#4635/#4564 order)", fn)
		}
	}
}
