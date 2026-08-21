// Package toolreplay provides deterministic record/replay of tool executions.
//
// It implements the "VCR/cassette" pattern: during a RECORD phase, every
// (toolName, input) → result mapping is persisted to a Tape file. During a
// REPLAY phase, the real tool is never invoked — results are looked up by
// matching the input and returned from the Tape.
//
// This enables:
//   - Regression tests that reproduce agent behaviour without touching the
//     filesystem, network, or subprocesses.
//   - Deterministic benchmark suites (like Aider's benchmark replay).
//   - Debugging flaky agent loops by replaying an exact tool-call sequence.
//
// Usage:
//
//	// Record
//	tape := toolreplay.NewTape()
//	wrapped := toolreplay.NewRecorder(realTool, tape)
//	result, err := wrapped.Execute(ctx, input)
//	tape.Save("testdata/foo.tape.json")
//
//	// Replay
//	tape, err := toolreplay.LoadTape("testdata/foo.tape.json")
//	wrapped := toolreplay.NewReplayer(realTool, tape) // realTool used for metadata only
//	result, err := wrapped.Execute(ctx, input)         // never hits real tool
package toolreplay

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"sync"
)

// Entry records a single tool execution: the input that was given and the
// result that the real tool returned.
type Entry struct {
	// ToolName is the tool that produced this entry.
	ToolName string `json:"tool_name"`
	// InputHash is the SHA-256 hash of the normalized input JSON.
	InputHash string `json:"input_hash"`
	// Input is the raw input JSON (kept for debugging / human inspection).
	Input json.RawMessage `json:"input,omitempty"`
	// Result is the tool result that was recorded.
	Result Result `json:"result"`
	// Err stores a non-nil Go-level error from the tool execution, if any.
	// Most tools return (Result{IsError:true}, nil) rather than a Go error,
	// but we preserve both for fidelity.
	Err string `json:"err,omitempty"`
}

// Result mirrors tool.Result but without the unexportable/functional fields
// (FollowUpMessages, SuggestedWorkingDir) that are not meaningful in replay.
// Only Content and IsError are reproduced; Images are included so screenshot
// results can be replayed.
type Result struct {
	Content string        `json:"content"`
	IsError bool          `json:"is_error"`
	Images  []ResultImage `json:"images,omitempty"`
}

// ResultImage mirrors tool.ResultImage for tape persistence.
type ResultImage struct {
	MIME       string `json:"mime"`
	Base64     string `json:"base64"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
	SourcePath string `json:"source_path"`
}

// Tape is the collection of recorded tool executions, indexed for fast
// lookup by (toolName, inputHash). Multiple entries with the same key are
// stored in insertion order; replay returns the first unused one (FIFO),
// which correctly handles tools called multiple times with the same input
// but different results (e.g. a file that changes between calls).
type Tape struct {
	mu      sync.RWMutex
	entries map[string][]*entrySlot // key: toolName + "\x00" + inputHash
	order   []*entrySlot            // insertion order for serialization
}

// entrySlot wraps an Entry with a "consumed" flag for FIFO replay.
type entrySlot struct {
	Entry     Entry
	consumed  bool
	consumers int // how many replay calls have used this slot
}

// NewTape creates an empty Tape.
func NewTape() *Tape {
	return &Tape{entries: make(map[string][]*entrySlot)}
}

// tapeFile is the JSON serialization format.
type tapeFile struct {
	Version int     `json:"version"`
	Entries []Entry `json:"entries"`
}

const tapeVersion = 1

// HashInput normalizes the input JSON (canonicalizes key order) and returns
// the hex-encoded SHA-256 hash. Two inputs that are semantically identical
// (same keys/values, different order) produce the same hash.
func HashInput(input json.RawMessage) string {
	normalized := canonicalizeJSON(input)
	h := sha256.Sum256(normalized)
	return hex.EncodeToString(h[:])
}

// canonicalizeJSON re-serializes JSON with sorted object keys so that
// key-order differences don't affect the hash. This handles the common case
// where different models or serialization libraries emit keys in different
// orders for the same logical input.
func canonicalizeJSON(input json.RawMessage) []byte {
	if len(input) == 0 {
		return []byte("null")
	}
	var v interface{}
	if err := json.Unmarshal(input, &v); err != nil {
		// Not valid JSON — hash the raw bytes as-is.
		return input
	}
	canonical, err := json.Marshal(canonicalizeValue(v))
	if err != nil {
		return input
	}
	return canonical
}

// canonicalizeValue recursively sorts map keys.
func canonicalizeValue(v interface{}) interface{} {
	switch t := v.(type) {
	case map[string]interface{}:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		ordered := make(map[string]interface{}, len(t))
		for _, k := range keys {
			ordered[k] = canonicalizeValue(t[k])
		}
		return ordered
	case []interface{}:
		for i := range t {
			t[i] = canonicalizeValue(t[i])
		}
		return t
	default:
		return v
	}
}

// Record appends an entry to the tape.
func (t *Tape) Record(e Entry) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if e.InputHash == "" {
		e.InputHash = HashInput(e.Input)
	}
	key := e.ToolName + "\x00" + e.InputHash
	slot := &entrySlot{Entry: e}
	t.entries[key] = append(t.entries[key], slot)
	t.order = append(t.order, slot)
}

// Lookup finds and consumes the next available entry for the given
// (toolName, input). Returns the entry and true if found. In replay mode,
// each matching entry can be consumed once (FIFO), so repeated calls with
// the same input walk through the recorded sequence.
//
// If strictFIFO is false, the same entry may be returned multiple times
// (useful when you want "last recorded result wins" semantics).
func (t *Tape) Lookup(toolName string, input json.RawMessage, strictFIFO bool) (Entry, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	inputHash := HashInput(input)
	key := toolName + "\x00" + inputHash
	slots := t.entries[key]
	// #877: non-strict mode promises "last recorded result wins" but the
	// loop returned on the FIRST slot — first-wins, never advancing, serving
	// stale results when a key was recorded 2+ times (e.g. two identical
	// read_file calls spanning a file change). Take the last slot directly.
	if !strictFIFO && len(slots) > 0 {
		s := slots[len(slots)-1]
		s.consumers++
		return s.Entry, true
	}
	for _, s := range slots {
		if !s.consumed {
			s.consumed = true
			s.consumers++
			return s.Entry, true
		}
	}
	return Entry{}, false
}

// Len returns the total number of recorded entries.
func (t *Tape) Len() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.order)
}

// Save writes the tape to a JSON file. The file is written atomically.
func (t *Tape) Save(path string) error {
	t.mu.RLock()
	defer t.mu.RUnlock()

	entries := make([]Entry, 0, len(t.order))
	for _, s := range t.order {
		entries = append(entries, s.Entry)
	}

	data, err := json.MarshalIndent(tapeFile{Version: tapeVersion, Entries: entries}, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal tape: %w", err)
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write tape file: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("rename tape file: %w", err)
	}
	return nil
}

// LoadTape reads a tape from a JSON file.
func LoadTape(path string) (*Tape, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read tape file: %w", err)
	}
	var tf tapeFile
	if err := json.Unmarshal(data, &tf); err != nil {
		return nil, fmt.Errorf("unmarshal tape: %w", err)
	}
	if tf.Version != tapeVersion {
		return nil, fmt.Errorf("tape version mismatch: file has %d, expected %d", tf.Version, tapeVersion)
	}
	tape := NewTape()
	for _, e := range tf.Entries {
		tape.Record(e)
	}
	return tape, nil
}
