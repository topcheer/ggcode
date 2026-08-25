package tool

import (
	"strings"
	"testing"
)

// Description must reflect the actual workspace IM state instead of a
// generic platform list: nil manager, no bindings, and bound adapters with
// per-adapter media capability.
func TestIMToolDescriptionDynamic(t *testing.T) {
	// nil manager: minimal, honest description.
	tool := IMTool{}
	if d := tool.Description(); !strings.Contains(d, "No IM manager") {
		t.Fatalf("nil-manager description should say no manager: %q", d)
	}

	// No bindings: guidance to bind first.
	mgr := &mockIMManager{}
	tool = IMTool{Manager: mgr}
	if d := tool.Description(); !strings.Contains(d, "No IM adapter is bound") {
		t.Fatalf("empty description should guide binding: %q", d)
	}

	// Bound adapters: names + platform + media capability, no foreign platforms.
	mgr = newSendFileMock() // qq binding, platform qq
	tool = IMTool{Manager: mgr}
	d := tool.Description()
	for _, want := range []string{"qq (qq, media upload)", "send_file"} {
		if !strings.Contains(d, want) {
			t.Fatalf("description missing %q: %q", want, d)
		}
	}
	for _, ghost := range []string{"Telegram", "Discord", "Slack", "DingTalk"} {
		if strings.Contains(d, ghost) {
			t.Fatalf("description advertises unbound platform %q: %q", ghost, d)
		}
	}

	// Text-only platform: capability label differs.
	mgr = &mockIMManager{snapshot: IMSnapshot{
		CurrentBindings: []IMChannelBinding{{Adapter: "ding", ChannelID: "C9", Platform: "dingtalk"}},
	}}
	tool = IMTool{Manager: mgr}
	if d := tool.Description(); !strings.Contains(d, "ding (dingtalk, text only)") {
		t.Fatalf("text-only capability missing: %q", d)
	}
}
