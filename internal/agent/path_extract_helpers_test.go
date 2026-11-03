package agent

import "testing"

func TestExtractFilePathsFromArgsExcludesURLAndDirectory(t *testing.T) {
	args := []byte(`{"url":"https://example.com/x","directory":"/tmp/scan","source":"inline content","path":"/a.go"}`)
	got := extractFilePathsFromArgs(args, "web_fetch")
	if len(got) != 1 || got[0] != "/a.go" {
		t.Fatalf("expected only /a.go, got %v", got)
	}
}
