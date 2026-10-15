package wailskit

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

// #4124: sendContentUserSummary drives the liveHistory/tunnel push for the
// pasted-image path. Matrix: text-only, image-only, mixed, empty, and
// whitespace-only text blocks.
func TestSendContentUserSummary(t *testing.T) {
	cases := []struct {
		name   string
		blocks []provider.ContentBlock
		want   string
	}{
		{"text only", []provider.ContentBlock{{Type: "text", Text: "what is this"}}, "what is this"},
		{"image only", []provider.ContentBlock{{Type: "image", ImageMIME: "image/png"}}, "[image]"},
		{"mixed", []provider.ContentBlock{{Type: "text", Text: " describe "}, {Type: "image", ImageMIME: "image/png"}}, "describe [image]"},
		{"empty", nil, ""},
		{"whitespace text only", []provider.ContentBlock{{Type: "text", Text: "   "}}, ""},
		{"multi text", []provider.ContentBlock{{Type: "text", Text: "a"}, {Type: "text", Text: "b"}}, "a b"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sendContentUserSummary(tc.blocks)
			if got != tc.want {
				t.Fatalf("sendContentUserSummary(%s) = %q, want %q", tc.name, got, tc.want)
			}
		})
	}
}

// #4124 contract: a non-empty summary must be non-empty so the liveHistory
// append + tunnel push never get skipped for a pasted image round.
func TestSendContentUserSummaryImageNeverEmpty(t *testing.T) {
	if sendContentUserSummary([]provider.ContentBlock{{Type: "image", ImageMIME: "image/jpeg"}}) == "" {
		t.Fatal("image-only paste must produce a non-empty summary or the round becomes an orphan turn")
	}
	if !strings.Contains(sendContentUserSummary([]provider.ContentBlock{{Type: "image"}, {Type: "text", Text: "hi"}}), "[image]") {
		t.Fatal("mixed paste summary must carry the image marker")
	}
}
