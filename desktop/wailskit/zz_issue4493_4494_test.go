//go:build goolm

package wailskit

import (
	"os"
	"strings"
	"testing"
)

// #4493 pin: the RequestAskUser timeout branch must close BOTH ends
// (#4250 pattern) - emit ask_user:cancel and push the tunnel result.
func TestIssue4493_AskUserTimeoutClosesBothEnds(t *testing.T) {
	b, err := os.ReadFile("chat.go")
	if err != nil {
		t.Skip("source not readable in this environment")
	}
	s := string(b)
	fn := extractChatFunc(t, s, "RequestAskUser")
	if !strings.Contains(fn, "askCtx.Err() != nil") {
		t.Fatal("RequestAskUser lacks a timeout branch")
	}
	if !strings.Contains(fn, `"ask_user:cancel"`) {
		t.Fatal("timeout branch must emit ask_user:cancel (#4250 pattern)")
	}
	if !strings.Contains(fn, "PushTunnelAskUserResult") {
		t.Fatal("timeout branch must push the mobile-side closure")
	}
	// Late desktop answer (RespondAskUser) and late mobile tap
	// (HandleMobileAskUserResponse) must feed back "stale" instead of
	// returning silently.
	for _, fnName := range []string{"RespondAskUser", "HandleMobileAskUserResponse"} {
		f := extractChatFunc(t, s, fnName)
		if !strings.Contains(f, "PushTunnelAskUserResult") {
			t.Fatalf("%s stale path must push a closure event", fnName)
		}
	}
}

// #4494 pin: SendHiddenText must NOT re-bump runGeneration after
// InitAgent - only the guarded persist refresh (#1181/#1511, same as
// SendContent).
func TestIssue4494_SendHiddenTextNoReBump(t *testing.T) {
	b, err := os.ReadFile("chat.go")
	if err != nil {
		t.Skip("source not readable in this environment")
	}
	s := string(b)
	fn := extractChatFunc(t, s, "SendHiddenText")
	initIdx := strings.Index(fn, "InitAgent(")
	streamIdx := strings.Index(fn, ".RunStream(")
	if initIdx < 0 || streamIdx < 0 || initIdx > streamIdx {
		t.Fatal("SendHiddenText structure changed: InitAgent before RunStream expected")
	}
	between := fn[initIdx:streamIdx]
	if strings.Contains(between, "b.runGeneration++") {
		t.Fatal("#4494 regression: runGeneration re-bumped between InitAgent and RunStream")
	}
	if !strings.Contains(between, "if b.activeRunGen == runGen {") {
		t.Fatal("persist refresh must be guarded by activeRunGen ownership")
	}
}

func extractChatFunc(t *testing.T, s, name string) string {
	t.Helper()
	idx := strings.Index(s, "func (b *ChatBridge) "+name+"(")
	if idx < 0 {
		t.Fatalf("%s not found", name)
	}
	end := strings.Index(s[idx+1:], "\nfunc ")
	if end < 0 {
		return s[idx:]
	}
	return s[idx : idx+1+end]
}
