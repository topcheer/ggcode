package memory

// #3699 probe: AnalyzeToolFlows must STREAM session files, never slurp a
// whole file into memory. The pre-fix shape (one strings.Builder holding the
// entire JSONL text per file) was profiled live as 3.6GB of retained
// strings.Builder allocations on a multi-hundred-MB session store - the
// dominant term of the "one agent loop eats several GB of RSS" regression.
//
// Pin A (equivalence): per-line extraction yields the IDENTICAL sequence to
// the whole-body scan (ExtractToolSequence), including multiple tool_name
// fields on one line and empty-name skips.
// Pin B (memory bound): analyzing a synthetic ~48MB session file must not
// retain anywhere near the file's size on the heap.

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestIssue3699_LineExtractionEquivalence(t *testing.T) {
	body := strings.Join([]string{
		`{"type":"tool","tool_name":"read_file","path":"a.go"}`,
		`{"type":"tool","tool_name":"grep"} extra "tool_name":"lsp_hover" tail`,
		`no tool here`,
		`{"type":"tool","tool_name":""}`, // empty name skipped
		`{"type":"tool","tool_name":"run_command"}`,
	}, "\n")

	want := ExtractToolSequence(body)

	var got []string
	for _, line := range strings.Split(body, "\n") {
		got = appendToolNamesFromLine(got, []byte(line))
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("streamed sequence diverged:\n got  %v\n want %v", got, want)
	}
	if len(got) == 0 || got[0] != "read_file" || got[len(got)-1] != "run_command" {
		t.Fatalf("unexpected sequence: %v", got)
	}
}

func TestIssue3699_AnalyzeDoesNotRetainFileText(t *testing.T) {
	dir := t.TempDir()
	// ~48MB session file: 120k lines x ~400B payload, tool_name every line.
	var sb strings.Builder
	for i := 0; i < 120_000; i++ {
		name := "run_command"
		if i%2 == 1 {
			name = "grep"
		}
		sb.WriteString(`{"type":"tool","tool_name":"` + name + `","args":"echo filler-filler-filler-filler #`)
		sb.WriteString(strings.Repeat("x", 340))
		sb.WriteString(`"}`)
		sb.WriteString("\n")
	}
	if err := os.WriteFile(filepath.Join(dir, "s1.jsonl"), []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	fileSize := int64(len(sb.String()))
	if fileSize < 40*1024*1024 {
		t.Fatalf("fixture too small to prove streaming: %d bytes", fileSize)
	}

	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	pats, err := AnalyzeToolFlows(dir, 5)
	if err != nil {
		t.Fatal(err)
	}
	runtime.ReadMemStats(&after)

	if len(pats) == 0 {
		t.Fatal("fixture must yield at least one pattern (self-loops excluded; add a second tool)")
	}
	// HeapInuse delta must stay far below the file size: streaming holds only
	// tool names plus scanner buffers (~KBs). Allow a generous 8MB margin for
	// test-harness noise; the old slurping shape would blow straight past it.
	delta := int64(after.HeapInuse) - int64(before.HeapInuse)
	if delta > 8*1024*1024 {
		t.Fatalf("AnalyzeToolFlows retained %d bytes on the heap for a %d-byte file - not streaming", delta, fileSize)
	}
}
