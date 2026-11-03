package agentruntime

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/mcp"
	"github.com/topcheer/ggcode/internal/provider"
)

func TestIssue2283MimelessImageDefaults(t *testing.T) {

	msg := mcp.SamplingMessage{
		Role: "user",
		Content: mcp.SamplingContent{
			Type: "image",
			Data: "aGVsbG8=",
		},
	}
	mime := msg.Content.MIMEType
	if mime == "" {
		mime = "image/png"
	}
	block := provider.ImageBlock(mime, msg.Content.Data)
	if block.ImageMIME != "image/png" {
		t.Errorf("empty MIMEType must default to image/png, got %q", block.ImageMIME)
	}

	block2 := provider.ImageBlock("image/webp", msg.Content.Data)
	if block2.ImageMIME != "image/webp" {
		t.Errorf("explicit MIMEType must pass through, got %q", block2.ImageMIME)
	}
	if !strings.EqualFold(block.Type, "image") {
		t.Errorf("block type must be image, got %q", block.Type)
	}
}
