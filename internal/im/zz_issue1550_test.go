package im

// #1550: the feishu WEBHOOK branch never read message_type and never
// called processAttachments - an image message's raw {"image_key":...}
// JSON fell through parseMessageContent and reached the agent as text,
// while the WS branch of the SAME adapter downloads the image. The
// webhook branch now mirrors the WS pipeline.

import (
	"os"
	"strings"
	"testing"
)

func TestIssue1550ImageJSONNotText(t *testing.T) {
	a := &feishuAdapter{name: "test"}
	text := a.parseMessageContent(`{"image_key":"img_v3_abc"}`)
	if strings.Contains(text, "image_key") {
		t.Fatalf("raw image JSON must not survive as text, got %q", text)
	}
}

func TestIssue1550WebhookBranchMirrorsWS(t *testing.T) {
	// Source pin: the webhook branch must read message_type and route
	// through processAttachments with the same empty gate as the WS branch.
	b, err := os.ReadFile("feishu_adapter.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	start := strings.Index(src, "func (a *feishuAdapter) handleMessageEvent")
	end := strings.Index(src[start+1:], "\nfunc ")
	branch := src[start : start+1+end]
	if !strings.Contains(branch, `message["message_type"]`) {
		t.Fatal("webhook branch must read message_type")
	}
	if !strings.Contains(branch, "processAttachments(ctx, msgType, content, messageID)") {
		t.Fatal("webhook branch must route non-text types through processAttachments")
	}
	if !strings.Contains(branch, "#1550") {
		t.Fatal("the fix must carry its issue annotation")
	}
}
