package agent

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"
)

func TestToolResultRedundancy_VerifyRerunExempt(t *testing.T) {
	verifyOut := func(residualErrs int) string {
		var b strings.Builder
		b.WriteString("=== RUN TestFixLoop\n")
		for i := 0; i < residualErrs; i++ {
			fmt.Fprintf(&b, "--- FAIL: TestFixLoop case %d: assertion mismatch\n", i)
			fmt.Fprintf(&b, "    expected value 42, got 41\n")
		}
		b.WriteString("FAIL\n")
		b.WriteString("exit status 1\n")
		return b.String()
	}
	goTestArgs := json.RawMessage(`{"command":"go test ./internal/agent/"}`)

	s := newToolResultRedundancyState()

	if msg := s.recordToolCall("run_command", goTestArgs, verifyOut(15), 1); msg != "" {
		t.Fatalf("first verify run should not warn, got: %s", msg)
	}

	if msg := s.recordToolCall("run_command", goTestArgs, verifyOut(14), 3); msg != "" {
		t.Fatalf("verify rerun must be exempt from redundancy warning, got: %s", msg)
	}

	if msg := s.recordToolCall("run_command", goTestArgs, verifyOut(13), 5); msg != "" {
		t.Fatalf("repeated verify reruns must stay exempt, got: %s", msg)
	}

	c := newToolResultRedundancyState()
	if msg := c.recordToolCall("read_file", json.RawMessage(`{"path":"/a/b.go"}`), verifyOut(15), 1); msg != "" {
		t.Fatalf("first read should not warn, got: %s", msg)
	}
	if msg := c.recordToolCall("read_file", json.RawMessage(`{"path":"/a/b.go"}`), verifyOut(14), 3); msg == "" {
		t.Fatal("non-verify overlapping results should still warn")
	}
}

func TestToolResultRedundancy_VerifyRerunExemptCommandForms(t *testing.T) {
	out := "building module...\nrunning tests...\nall packages compiled\n4 tests run\n1 failed\n"
	for name, args := range map[string]json.RawMessage{
		"plain":     json.RawMessage(`{"command":"make test"}`),
		"envPrefix": json.RawMessage(`{"command":"GOFLAGS=-p=1 make verify-ci"}`),
		"compound":  json.RawMessage(`{"command":"cd /app && go build ./... && go test ./..."}`),
	} {
		s := newToolResultRedundancyState()
		if msg := s.recordToolCall("run_command", args, out, 1); msg != "" {
			t.Fatalf("%s: first run should not warn, got: %s", name, msg)
		}
		if msg := s.recordToolCall("run_command", args, out, 3); msg != "" {
			t.Fatalf("%s: verify rerun should be exempt, got: %s", name, msg)
		}
	}

	s := newToolResultRedundancyState()
	args := json.RawMessage(`{"command":"echo deployment step one && echo deployment step two"}`)
	if msg := s.recordToolCall("run_command", args, out, 1); msg != "" {
		t.Fatalf("first run should not warn, got: %s", msg)
	}
	if msg := s.recordToolCall("run_command", args, out, 3); msg == "" {
		t.Fatal("non-verify run_command overlap should still warn")
	}
}

func TestToolResultRedundancy_BasicOverlap(t *testing.T) {
	s := newToolResultRedundancyState()

	content1 := `func main() {
	fmt.Println("hello")
	for i := 0; i < 10; i++ {
		fmt.Println(i)
	}
	return result
}`

	msg := s.recordResult("read_file", content1, 1)
	if msg != "" {
		t.Fatalf("first call should not warn, got: %s", msg)
	}

	content2 := `func main() {
	fmt.Println("hello")
	for i := 0; i < 10; i++ {
		fmt.Println(i)
	}
	return result
	// extra line here
}
`
	msg = s.recordResult("read_file", content2, 2)
	if msg == "" {
		t.Fatal("second call with high overlap should warn")
	}
	if !strings.Contains(msg, "redundancy") {
		t.Errorf("warning should mention redundancy, got: %s", msg)
	}
}

func TestToolResultRedundancy_NoOverlap(t *testing.T) {
	s := newToolResultRedundancyState()

	content1 := `package foo
import "fmt"
func alpha() {
	fmt.Println("alpha")
	return
}
`

	content2 := `package bar
import "os"
func beta() {
	os.Exit(1)
}
`

	msg := s.recordResult("read_file", content1, 1)
	if msg != "" {
		t.Fatalf("first call should not warn, got: %s", msg)
	}

	msg = s.recordResult("read_file", content2, 2)
	if msg != "" {
		t.Fatalf("dissimilar content should not warn, got: %s", msg)
	}
}

func TestToolResultRedundancy_TooShort(t *testing.T) {
	s := newToolResultRedundancyState()

	content1 := `line one
line two
line three`

	msg := s.recordResult("grep", content1, 1)
	if msg != "" {
		t.Fatalf("short result should not warn, got: %s", msg)
	}
}

func TestToolResultRedundancy_MaxWarnings(t *testing.T) {
	s := newToolResultRedundancyState()

	content1 := `first line of content
second line of content
third line of content
fourth line of content
fifth line of content
sixth line of content
`

	msg1 := s.recordResult("read_file", content1, 1)
	if msg1 != "" {
		t.Fatalf("first call should not warn, got: %s", msg1)
	}

	msg2 := s.recordResult("read_file", content1, 2)
	if msg2 == "" {
		t.Fatal("should warn on redundant call")
	}

	s.lastWarnedIter = 0
	msg3 := s.recordResult("read_file", content1, 5)
	if msg3 == "" {
		t.Fatal("should warn second time")
	}

	s.lastWarnedIter = 0
	msg4 := s.recordResult("read_file", content1, 10)
	if msg4 != "" {
		t.Fatalf("should not warn beyond max, got: %s", msg4)
	}
}

func TestToolResultRedundancy_ConsecutiveGuard(t *testing.T) {
	s := newToolResultRedundancyState()

	content := `alpha line one
alpha line two
alpha line three
alpha line four
alpha line five
alpha line six
`

	s.recordResult("read_file", content, 3)

	msg := s.recordResult("grep", content, 3)
	if msg == "" {
		t.Fatal("same content from different tool at same iteration should warn (genuine redundancy)")
	}

	msg2 := s.recordResult("search_files", content, 3)
	if msg2 != "" {
		t.Fatalf("third call at same iter should be suppressed after warning, got: %s", msg2)
	}
}

func TestToolResultRedundancy_DifferentToolNames(t *testing.T) {
	s := newToolResultRedundancyState()

	content := `func handler() error {
	if err != nil {
		return err
	}
	return nil
}
// some additional meaningful content
var x = 42
`

	s.recordResult("read_file", content, 1)

	msg := s.recordResult("grep", content, 2)
	if msg == "" {
		t.Fatal("should warn when same content from different tool")
	}

	if !strings.Contains(msg, "read_file") || !strings.Contains(msg, "grep") {
		t.Errorf("warning should mention both tools, got: %s", msg)
	}
}

func TestToolResultRedundancy_Reset(t *testing.T) {
	s := newToolResultRedundancyState()

	content := `line one content
line two content
line three content
line four content
line five content
line six content
`

	s.recordResult("read_file", content, 1)
	s.recordResult("read_file", content, 2)

	s.reset()
	if len(s.entries) != 0 {
		t.Errorf("entries should be empty after reset, got %d", len(s.entries))
	}
	if s.warningsFired != 0 {
		t.Errorf("warningsFired should be 0 after reset, got %d", s.warningsFired)
	}
}

func TestTRNormalize_SkipsShortLines(t *testing.T) {
	content := "a\n\nbc\n\n\nvalid line one\nvalid line two\n"
	lines := trNormalize(content)
	if len(lines) != 2 {
		t.Errorf("expected 2 meaningful lines, got %d", len(lines))
	}
}

func TestTRNormalize_TruncatesLongLines(t *testing.T) {
	longLine := make([]byte, trMaxLineLen+50)
	for i := range longLine {
		longLine[i] = 'x'
	}
	content := string(longLine) + "\n" + string(longLine)
	lines := trNormalize(content)
	for line := range lines {
		if len(line) > trMaxLineLen {
			t.Errorf("line should be truncated, got len %d", len(line))
		}
	}
}

func TestTRJaccard(t *testing.T) {
	a := map[string]bool{"x": true, "y": true, "z": true}
	b := map[string]bool{"x": true, "y": true, "w": true}

	j := trJaccard(a, b)
	if math.Abs(j-0.5) > 1e-9 {
		t.Errorf("expected Jaccard 0.5, got %.2f", j)
	}

	j = trJaccard(a, a)
	if math.Abs(j-1.0) > 1e-9 {
		t.Errorf("identical sets should have Jaccard 1.0, got %.2f", j)
	}

	j = trJaccard(map[string]bool{}, map[string]bool{"a": true})
	if math.Abs(j) > 1e-9 {
		t.Errorf("empty set should have Jaccard 0, got %.2f", j)
	}
}

func TestRedundancyAdjacentIterCooldown1855(t *testing.T) {
	tr := newToolResultRedundancyState()
	var sb strings.Builder
	for i := 0; i < trMinLines+1; i++ {
		fmt.Fprintf(&sb, "unique output line %02d\n", i)
	}
	content := sb.String()

	tr.recordResult("read_file", content, 4)

	if m := tr.recordResult("read_file", content, 5); m == "" {
		t.Fatal("first warning expected")
	}

	if m := tr.recordResult("read_file", content, 6); m != "" {
		t.Fatal("adjacent-iteration repeat must be suppressed")
	}

	if m := tr.recordResult("read_file", content, 20); m == "" {
		t.Fatal("second warning must fire for later redundancy")
	}

	if m := tr.recordResult("read_file", content, 40); m != "" {
		t.Fatal("cap must still apply")
	}
}
