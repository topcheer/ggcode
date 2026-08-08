package agent

import (
	"math"
	"strings"
	"testing"
)

func TestToolResultRedundancy_BasicOverlap(t *testing.T) {
	s := newToolResultRedundancyState()

	content1 := `func main() {
	fmt.Println("hello")
	for i := 0; i < 10; i++ {
		fmt.Println(i)
	}
	return result
}`

	// First call - no warning, just stored.
	msg := s.recordResult("read_file", content1, 1)
	if msg != "" {
		t.Fatalf("first call should not warn, got: %s", msg)
	}

	// Second call with overlapping content (same lines, small additions).
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

	// Content with fewer than trMinLines meaningful lines.
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

	// Trigger max warnings.
	msg2 := s.recordResult("read_file", content1, 2)
	if msg2 == "" {
		t.Fatal("should warn on redundant call")
	}

	// Different iteration to avoid consecutive guard.
	s.lastWarnedIter = 0 // reset guard for test
	msg3 := s.recordResult("read_file", content1, 5)
	if msg3 == "" {
		t.Fatal("should warn second time")
	}

	// Third warning attempt should be suppressed.
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

	// First call: store + warn (since it's the first, no warning).
	s.recordResult("read_file", content, 3)

	// Same iteration, same content from different tool: IS redundant.
	msg := s.recordResult("grep", content, 3)
	if msg == "" {
		t.Fatal("same content from different tool at same iteration should warn (genuine redundancy)")
	}

	// Now try a third call at the same iteration - should be suppressed
	// because we already warned at this iteration.
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
	// Warning should mention both tool names.
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

	// Intersection: {x,y} = 2, Union: {x,y,z,w} = 4, Jaccard = 0.5
	j := trJaccard(a, b)
	if math.Abs(j-0.5) > 1e-9 {
		t.Errorf("expected Jaccard 0.5, got %.2f", j)
	}

	// Identical sets.
	j = trJaccard(a, a)
	if math.Abs(j-1.0) > 1e-9 {
		t.Errorf("identical sets should have Jaccard 1.0, got %.2f", j)
	}

	// Empty.
	j = trJaccard(map[string]bool{}, map[string]bool{"a": true})
	if math.Abs(j) > 1e-9 {
		t.Errorf("empty set should have Jaccard 0, got %.2f", j)
	}
}
