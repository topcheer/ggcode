package tool

import (
	"strings"
	"testing"
)

func TestIMToolDescriptionDynamic(t *testing.T) {

	tool := IMTool{}
	if d := tool.Description(); !strings.Contains(d, "No IM manager") {
		t.Fatalf("nil-manager description should say no manager: %q", d)
	}

	mgr := &mockIMManager{}
	tool = IMTool{Manager: mgr}
	if d := tool.Description(); !strings.Contains(d, "No IM adapter is bound") {
		t.Fatalf("empty description should guide binding: %q", d)
	}

	mgr = newSendFileMock()
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

	mgr = &mockIMManager{snapshot: IMSnapshot{
		CurrentBindings: []IMChannelBinding{{Adapter: "ding", ChannelID: "C9", Platform: "dingtalk"}},
	}}
	tool = IMTool{Manager: mgr}
	if d := tool.Description(); !strings.Contains(d, "ding (dingtalk, text only)") {
		t.Fatalf("text-only capability missing: %q", d)
	}
}
