//go:build goolm

package im

import (
	"os"
	"strings"
	"testing"
)

func TestIssue4519_RemoteImageURLNotSuppressed(t *testing.T) {
	b, err := os.ReadFile("discord_adapter.go")
	if err != nil {
		t.Skip("source not readable in this environment")
	}
	s := string(b)

	idx := strings.Index(s, "func (a *discordAdapter) sendExtractedImage")
	if idx < 0 {
		t.Fatal("sendExtractedImage not found")
	}
	end := strings.Index(s[idx+1:], "\nfunc ")
	if end < 0 {
		end = len(s) - idx - 1
	}
	body := s[idx : idx+1+end]
	if !strings.Contains(body, "sendChannelMessageFlags(ctx, channelID, img.Data, false)") {
		t.Fatal("#4519 regression: remote URL path still suppresses embeds")
	}

	if !strings.Contains(s, "func (a *discordAdapter) sendChannelMessage(ctx context.Context, channelID, content string) error {\n\treturn a.sendChannelMessageFlags(ctx, channelID, content, true)") {
		t.Fatal("#4519 regression: default text path lost SUPPRESS_EMBEDS")
	}
}
