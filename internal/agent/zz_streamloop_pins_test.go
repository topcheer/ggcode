package agent

import (
	"os"
	"strings"
)

// readStreamLoopSource concatenates the named stream-loop source files in
// order. #5543 split RunStreamWithContent' +BT+'s inline loop body into
// dedicated run_*.go methods; source-level regression pins that target the
// loop body stay file-shape-agnostic by scanning the whole file set instead
// of a single hard-coded file (same upgrade pattern the #1817 pin got when
// streamChatResponse moved to stream_chat.go in #5521 PR3).
func readStreamLoopSource(names ...string) (string, error) {
	var b strings.Builder
	for _, n := range names {
		raw, err := os.ReadFile(n)
		if err != nil {
			return "", err
		}
		b.Write(raw)
		b.WriteString("\n")
	}
	return b.String(), nil
}
