//go:build goolm

package agent

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/tool"
)

func engine3865(t *testing.T) *workflowEngine {
	t.Helper()
	dir := t.TempDir()
	gg := dir + "/.ggcode"
	if err := os.MkdirAll(gg, 0o755); err != nil {
		t.Fatal(err)
	}
	spec := `{"steps":[{"id":"t","on_commands":["go *"]}]}`
	if err := os.WriteFile(gg+"/"+workflowSpecFileName, []byte(spec), 0o644); err != nil {
		t.Fatal(err)
	}
	return &workflowEngine{loadDir: gg}
}

// #3865 C: the FAILED-attempt snippet must carry the TAIL of the output
// (where build/test errors actually live) and be valid UTF-8 - the head
// slice quoted the command echo/banner instead of the error, and byte
// slicing could split a rune mid-sequence.
func TestIssue3865_ErrSnippetTailAndUTF8(t *testing.T) {
	e := engine3865(t)
	long := strings.Repeat("banner line\n", 40) + "# github.com/x/y: undefined: Foo"
	e.recordAttempt("run_command",
		json.RawMessage(`{"command":"go build ./..."}`),
		tool.Result{Content: long, IsError: true})

	atts := e.recentAttempts("t", 3)
	if len(atts) != 1 {
		t.Fatalf("expected 1 attempt, got %d", len(atts))
	}
	if !strings.Contains(atts[0].ErrSnippet, "undefined: Foo") {
		t.Fatalf("snippet must carry the tail error line, got: %q", atts[0].ErrSnippet)
	}
	// The tail legitimately ends with the filler's last banners; the head
	// behavior would start from the FIRST banner and MISS the error line -
	// the Contains check above pins exactly that difference.
	if len(atts[0].ErrSnippet) > wfErrSnippetMax+8 { // +8: sanitizer may append '?' runes
		t.Fatalf("snippet length %d exceeds cap+%d", len(atts[0].ErrSnippet), 8)
	}
}

// Multi-byte tail: slicing the last 160 bytes of a CJK-heavy output must
// not produce invalid UTF-8 in the snippet (it is fmt-ed into messages).
func TestIssue3865_ErrSnippetRuneBoundary(t *testing.T) {
	e := engine3865(t)
	content := strings.Repeat("汉", 200) // 600 bytes of multi-byte runes
	e.recordAttempt("run_command",
		json.RawMessage(`{"command":"go test ./..."}`),
		tool.Result{Content: content, IsError: true})
	atts := e.recentAttempts("t", 1)
	for _, r := range atts[0].ErrSnippet {
		if r == 0xFFFD {
			t.Fatalf("snippet contains replacement runes (unsanitized split): %q", atts[0].ErrSnippet)
		}
	}
}
