//go:build goolm

package im

import (
	"os"
	"strings"
	"testing"
)

// #4519: sendExtractedImage's remote-URL path must NOT suppress embeds -
// the unconditional discordSuppressEmbeds stopped Discord from expanding
// the image URL into a preview, leaving the user with bare URL text.
func TestIssue4519_RemoteImageURLNotSuppressed(t *testing.T) {
	b, err := os.ReadFile("discord_adapter.go")
	if err != nil {
		t.Skip("source not readable in this environment")
	}
	s := string(b)
	// The image path routes through the flags variant with suppress=false.
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
	// Ordinary text messages keep the cleaner no-embed UX.
	if !strings.Contains(s, "func (a *discordAdapter) sendChannelMessage(ctx context.Context, channelID, content string) error {\n\treturn a.sendChannelMessageFlags(ctx, channelID, content, true)") {
		t.Fatal("#4519 regression: default text path lost SUPPRESS_EMBEDS")
	}
}
