package toolreplay

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"sync"
)

type Entry struct {
	ToolName string `json:"tool_name"`

	InputHash string `json:"input_hash"`

	Input json.RawMessage `json:"input,omitempty"`

	Result Result `json:"result"`

	Err string `json:"err,omitempty"`
}

type Result struct {
	Content string        `json:"content"`
	IsError bool          `json:"is_error"`
	Images  []ResultImage `json:"images,omitempty"`
}

type ResultImage struct {
	MIME       string `json:"mime"`
	Base64     string `json:"base64"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
	SourcePath string `json:"source_path"`
}

type Tape struct {
	mu      sync.RWMutex
	entries map[string][]*entrySlot
	order   []*entrySlot
}

type entrySlot struct {
	Entry     Entry
	consumed  bool
	consumers int
}

func NewTape() *Tape {
	return &Tape{entries: make(map[string][]*entrySlot)}
}

type tapeFile struct {
	Version int     `json:"version"`
	Entries []Entry `json:"entries"`
}

const tapeVersion = 1

func HashInput(input json.RawMessage) string {
	normalized := canonicalizeJSON(input)
	h := sha256.Sum256(normalized)
	return hex.EncodeToString(h[:])
}

func canonicalizeJSON(input json.RawMessage) []byte {
	if len(input) == 0 {
		return []byte("null")
	}

	var v interface{}
	dec := json.NewDecoder(bytes.NewReader(input))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {

		return input
	}
	canonical, err := json.Marshal(canonicalizeValue(v))
	if err != nil {
		return input
	}
	return canonical
}

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

func (t *Tape) Lookup(toolName string, input json.RawMessage, strictFIFO bool) (Entry, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	inputHash := HashInput(input)
	key := toolName + "\x00" + inputHash
	slots := t.entries[key]

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

func (t *Tape) Len() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.order)
}

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
