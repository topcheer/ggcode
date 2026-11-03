package acp

import (
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

func TestToolKindCoversRealWriteTools(t *testing.T) {
	cases := map[string]string{
		"write_file":      "write",
		"edit_file":       "write",
		"multi_edit_file": "write",
		"batch_replace":   "write",
		"diff_apply":      "write",
		"read_file":       "read",
		"grep":            "read",
		"run_command":     "execute",
		"git_commit":      "execute",
	}
	for name, want := range cases {
		if got := toolKind(name); got != want {
			t.Errorf("toolKind(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestProviderToACPMessagePreservesImageBlocks(t *testing.T) {
	msgs := []provider.Message{{
		Role: "user",
		Content: []provider.ContentBlock{
			provider.TextBlock("look at this"),
			provider.ImageBlock("image/png", "aGVsbG8="),
		},
	}}

	acpMsgs := providerToACPMessage(msgs)
	if len(acpMsgs) != 1 || len(acpMsgs[0].Content) != 2 {
		t.Fatalf("unexpected message shape: %+v", acpMsgs)
	}
	img := acpMsgs[0].Content[1]
	if img.Type != "image" {
		t.Fatalf("block 1 type = %q, want image", img.Type)
	}
	if img.ImageMIME != "image/png" || img.ImageData != "aGVsbG8=" {
		t.Fatalf("image fields lost in conversion: MIME=%q data=%q", img.ImageMIME, img.ImageData)
	}

	back := acpToProviderContent(acpMsgs[0].Content)
	if len(back) != 2 {
		t.Fatalf("roundtrip dropped blocks: %+v", back)
	}
	if back[1].ImageMIME != "image/png" || back[1].ImageData != "aGVsbG8=" {
		t.Fatalf("image fields lost in roundtrip: %+v", back[1])
	}
}
