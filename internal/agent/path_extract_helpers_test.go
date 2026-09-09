package agent

import "testing"

// #1619-C: the read-path extractor must not admit url/directory/source
// keys - URLs entered the file-read set and grep's directory argument
// inflated it (mirror of the #953 write-side isolation).
func TestExtractFilePathsFromArgsExcludesURLAndDirectory(t *testing.T) {
	args := []byte(`{"url":"https://example.com/x","directory":"/tmp/scan","source":"inline content","path":"/a.go"}`)
	got := extractFilePathsFromArgs(args, "web_fetch")
	if len(got) != 1 || got[0] != "/a.go" {
		t.Fatalf("expected only /a.go, got %v", got)
	}
}
