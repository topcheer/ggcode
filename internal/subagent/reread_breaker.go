package subagent

// #5013 Ask-2: same-file repeated-read circuit breaker. Task-contract prose
// has zero enforcement power; a sub-agent that loops on reads burns tokens
// until a human cancels (sa-2: 173 reads / sa-3: 181 reads). This breaker is
// deterministic: more than rereadBreakerLimit identical-path reads with no
// intervening edit trips it, and the runner terminates the run with a partial
// result. Edits reset the window because re-reading a file you just changed
// is legitimate verification, not a loop.

import (
	"encoding/json"
	"fmt"
	"strings"
)

const rereadBreakerLimit = 3

var rereadBreakerReadTools = map[string]bool{
	"read_file":       true,
	"multi_file_read": true,
}

var rereadBreakerEditTools = map[string]bool{
	"edit_file": true, "multi_edit_file": true, "write_file": true,
	"multi_file_edit": true, "multi_file_write": true, "file_ops": true,
	"batch_replace": true, "notebook_edit": true,
}

type rereadBreaker struct {
	counts  map[string]int
	tripped string
}

func newRereadBreaker() *rereadBreaker {
	return &rereadBreaker{counts: make(map[string]int)}
}

// rereadBreakerPaths extracts the file paths a read-class call touches
// (best effort: JSON parse failures yield no paths and never trip).
func rereadBreakerPaths(toolName, rawArgs string) []string {
	if !rereadBreakerReadTools[toolName] || strings.TrimSpace(rawArgs) == "" {
		return nil
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(rawArgs), &args); err != nil {
		return nil
	}
	var paths []string
	if p, ok := args["path"].(string); ok && p != "" {
		paths = append(paths, p)
	}
	if files, ok := args["files"].([]any); ok {
		for _, f := range files {
			if fm, ok := f.(map[string]any); ok {
				if p, ok := fm["path"].(string); ok && p != "" {
					paths = append(paths, p)
				}
			}
		}
	}
	return paths
}

// record observes one tool call. It returns the tripped path and its read
// count when the breaker fires (first time only).
func (b *rereadBreaker) record(toolName, rawArgs string) (path string, count int, tripped bool) {
	if b.tripped != "" {
		return "", 0, false
	}
	if rereadBreakerEditTools[toolName] {
		// A mutation makes subsequent reads of the same file legitimate
		// verification - reset the window.
		b.counts = make(map[string]int)
		return "", 0, false
	}
	for _, p := range rereadBreakerPaths(toolName, rawArgs) {
		key := strings.TrimSpace(p)
		if key == "" {
			continue
		}
		b.counts[key]++
		if b.counts[key] > rereadBreakerLimit {
			b.tripped = key
			return key, b.counts[key], true
		}
	}
	return "", 0, false
}

func (b *rereadBreaker) reason() string {
	if b.tripped == "" {
		return ""
	}
	return fmt.Sprintf("same-file reread loop: %q read %d times with no intervening edit (#5013 reread breaker)",
		b.tripped, b.counts[b.tripped])
}
