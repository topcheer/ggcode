package agentruntime

// R234 (R232 audit item): an image-typed sampling message must convert
// to provider.ImageBlock, not silently degrade to empty text.

import (
	"context"
	"testing"

	"github.com/topcheer/ggcode/internal/mcp"
	"github.com/topcheer/ggcode/internal/provider"
)

// fakeProvider234 captures the messages handed to Chat.
type fakeProvider234 struct {
	provider.Provider
	got []provider.Message
}

func (f *fakeProvider234) Name() string { return "fake234" }
func (f *fakeProvider234) Chat(ctx context.Context, msgs []provider.Message, _ []provider.ToolDefinition) (*provider.ChatResponse, error) {
	f.got = msgs
	return &provider.ChatResponse{Message: provider.Message{Role: "assistant", Content: []provider.ContentBlock{provider.TextBlock("ok")}}}, nil
}

func TestR234_ImageSamplingMessageConvertsToImageBlock(t *testing.T) {
	fp := &fakeProvider234{}
	params := mcp.SamplingParams{
		Messages: []mcp.SamplingMessage{
			{Role: "user", Content: mcp.SamplingContent{Type: "image", MIMEType: "image/png", Data: "aWNvbg=="}},
			{Role: "user", Content: mcp.SamplingContent{Type: "text", Text: "describe"}},
		},
	}
	if _, err := mcpSamplingHandlerWith(context.Background(), params, fp); err != nil {
		t.Fatal(err)
	}
	var imgCount, textCount int
	for _, m := range fp.got {
		for _, b := range m.Content {
			switch b.Type {
			case "image":
				imgCount++
				if b.ImageMIME != "image/png" || b.ImageData != "aWNvbg==" {
					t.Errorf("image block payload mangled: %+v", b)
				}
			case "text":
				textCount++
			}
		}
	}
	if imgCount != 1 || textCount != 1 {
		t.Fatalf("want 1 image + 1 text block, got %d/%d (all %d msgs: %+v)", imgCount, textCount, len(fp.got), fp.got)
	}
}
