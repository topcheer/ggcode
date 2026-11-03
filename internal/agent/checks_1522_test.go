package agent

import (
	"encoding/json"
	"testing"
)

func Test1522Compound127IsFailure(t *testing.T) {
	if isNonFailureExit("go test ./... || npm run build", 127) {
		t.Fatal("compound-command 127 must not get the not-found amnesty (fallback leg 127 swallowed a real test failure)")
	}
	if !isNonFailureExit("cargo build", 127) {
		t.Fatal("simple missing-binary 127 keeps the amnesty")
	}
}

func Test1522StripCodeFence(t *testing.T) {
	for in, want := range map[string]string{
		"```go\ngo test ./...\n```":         "go test ./...",
		"```bash\nnpm test\n```":            "npm test",
		"go vet ./...":                      "go vet ./...",
		"```sh\ngo build ./...\n\nextra```": "go build ./...",
	} {
		if got := stripCodeFence(in); got != want {
			t.Errorf("stripCodeFence(%q) = %q, want %q", in, got, want)
		}
	}
}

func Test1522BatchReplaceFilesExtracted(t *testing.T) {
	args := json.RawMessage(`{"pattern":"x","replacement":"y","files":["/a/foo.go","/b/bar.go"]}`)
	if got := extractFilePathFromArgs("batch_replace", args); got != "/a/foo.go" {
		t.Fatalf("batch_replace first path not extracted: %q", got)
	}
}
