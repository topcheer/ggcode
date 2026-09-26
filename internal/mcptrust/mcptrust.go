// Package mcptrust implements the MCP tool-description trust baseline
// ("tool poisoning" / rug-pull defense) recommended by the 2025-06-18 /
// 2025-11-25 MCP security guidance and OWASP's agentic LLM01 mitigations:
//
//	A tool's DESCRIPTION and SCHEMA are attacker-influenceable content that
//	is injected straight into the planner context. Hash them on first
//	sighting, re-verify on every later connection, and surface any silent
//	change for human review instead of letting it slip into the prompt.
//
// The package is pure logic: it hashes definitions, persists per-server
// baselines as JSON, and diffs a fresh sighting against the stored
// baseline. Transport/presentation live in internal/plugin.
package mcptrust

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// StoreVersion is the baseline file schema version. A file with a different
// version is discarded (treated as no baseline) rather than misparsed.
const StoreVersion = 1

// Hints mirrors the trust-relevant parts of the MCP 2025-06-18 tool
// annotations. Pointer semantics are preserved (absent vs explicitly false)
// because a flipped annotation changes how the client treats the tool --
// e.g. readOnlyHint=true lifts the read-only-server name-heuristic block --
// so "absent → present" must count as a change even when the value is false.
type Hints struct {
	Title       string
	ReadOnly    *bool
	Destructive *bool
	Idempotent  *bool
	OpenWorld   *bool
}

// Fingerprint returns the stable trust hash for one tool definition.
// inputSchema is canonicalized (keys sorted via a decode/encode round-trip)
// before hashing so cosmetic re-serialization of the same schema does not
// register as a change; only semantic schema edits trip the hash.
func Fingerprint(name, description string, inputSchema []byte, h Hints) string {
	digest := sha256.New()
	writeField := func(part string) {
		digest.Write([]byte(part))
		digest.Write([]byte{0})
	}
	writeField(name)
	writeField(description)
	writeField(string(canonicalJSON(inputSchema)))
	writeField(h.Title)
	for _, p := range []*bool{h.ReadOnly, h.Destructive, h.Idempotent, h.OpenWorld} {
		switch {
		case p == nil:
			digest.Write([]byte{'a'})
		case *p:
			digest.Write([]byte{'t'})
		default:
			digest.Write([]byte{'f'})
		}
		digest.Write([]byte{0})
	}
	return hex.EncodeToString(digest.Sum(nil))
}

// canonicalJSON returns the key-sorted compact form of raw (decode/encode
// round-trip; encoding/json sorts map keys on marshal). Only semantic
// schema content survives canonicalization -- whitespace and key order do
// not. On parse failure the compacted raw bytes pass through unchanged
// (hashing still succeeds deterministically).
func canonicalJSON(raw []byte) []byte {
	if len(raw) == 0 {
		return nil
	}
	var v interface{}
	if err := json.Unmarshal(raw, &v); err != nil {
		return compactJSON(raw)
	}
	out, err := json.Marshal(v)
	if err != nil {
		return compactJSON(raw)
	}
	return out
}

// compactJSON returns the compacted form of raw; on parse failure the raw
// bytes pass through unchanged (hashing still succeeds deterministically).
func compactJSON(raw []byte) []byte {
	if len(raw) == 0 {
		return nil
	}
	var out bytes.Buffer
	if err := json.Compact(&out, raw); err != nil {
		return raw
	}
	return out.Bytes()
}

// ToolFingerprint pairs a tool name with its trust hash.
type ToolFingerprint struct {
	Name string
	Hash string
}

// ToolEntry is one tool's stored baseline.
type ToolEntry struct {
	Hash      string    `json:"hash"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ServerEntry is one server's stored baseline.
type ServerEntry struct {
	Tools     map[string]ToolEntry `json:"tools"`
	UpdatedAt time.Time            `json:"updated_at"`
}

// Store is the persisted trust baseline for all MCP servers.
type Store struct {
	Version int                    `json:"version"`
	Servers map[string]ServerEntry `json:"servers"`
}

// ChangeKind classifies a baseline diff.
type ChangeKind string

const (
	// ChangeModified: the tool still exists but its description, schema, or
	// annotations changed. This is the security-relevant kind -- the new text
	// reaches the planner context verbatim.
	ChangeModified ChangeKind = "modified"
	// ChangeAdded: a tool the baseline has never seen.
	ChangeAdded ChangeKind = "added"
	// ChangeRemoved: a baseline tool is no longer offered.
	ChangeRemoved ChangeKind = "removed"
)

// Change is one diff entry.
type Change struct {
	Tool string
	Kind ChangeKind
}

// Load reads the baseline file. A missing file yields an empty store with a
// nil error (first-run semantics); a corrupt or wrong-version file does the
// same -- a broken baseline must never block the agent from starting.
func Load(path string) (*Store, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return emptyStore(), nil
		}
		return nil, err
	}
	var store Store
	if err := json.Unmarshal(data, &store); err != nil || store.Version != StoreVersion || store.Servers == nil {
		return emptyStore(), nil
	}
	return &store, nil
}

func emptyStore() *Store {
	return &Store{Version: StoreVersion, Servers: make(map[string]ServerEntry)}
}

// Save persists the store with 0600 (the hashes are low-sensitivity, but
// the file lists every MCP server the user talks to). Parent dirs are
// created as needed.
func (s *Store) Save(path string) error {
	if s.Servers == nil {
		s.Servers = make(map[string]ServerEntry)
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	return os.WriteFile(path, data, 0o600)
}

// Has reports whether a baseline exists for the server. First sightings are
// silently baselined; only later connections can produce drift notes.
func (s *Store) Has(server string) bool {
	_, ok := s.Servers[server]
	return ok
}

// Diff compares a fresh fingerprint set against the stored baseline and
// returns the changes sorted by tool name. Zero-fingerprint tools
// (defensive: empty names) are ignored.
func (s *Store) Diff(server string, fps []ToolFingerprint) []Change {
	entry, ok := s.Servers[server]
	if !ok {
		return nil
	}
	seen := make(map[string]bool, len(fps))
	var changes []Change
	for _, fp := range fps {
		if fp.Name == "" {
			continue
		}
		seen[fp.Name] = true
		prev, exists := entry.Tools[fp.Name]
		switch {
		case !exists:
			changes = append(changes, Change{Tool: fp.Name, Kind: ChangeAdded})
		case prev.Hash != fp.Hash:
			changes = append(changes, Change{Tool: fp.Name, Kind: ChangeModified})
		}
	}
	for name := range entry.Tools {
		if !seen[name] {
			changes = append(changes, Change{Tool: name, Kind: ChangeRemoved})
		}
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Tool < changes[j].Tool })
	return changes
}

// Apply replaces the server's baseline with the fresh fingerprints. Named
// tools only; duplicates keep the first occurrence.
func (s *Store) Apply(server string, fps []ToolFingerprint, now time.Time) {
	tools := make(map[string]ToolEntry, len(fps))
	for _, fp := range fps {
		if fp.Name == "" {
			continue
		}
		if _, dup := tools[fp.Name]; dup {
			continue
		}
		tools[fp.Name] = ToolEntry{Hash: fp.Hash, UpdatedAt: now}
	}
	s.Servers[server] = ServerEntry{Tools: tools, UpdatedAt: now}
}

// Reset drops the server's baseline entirely (user-confirmed re-baseline:
// the next connection re-seeds silently).
func (s *Store) Reset(server string) {
	delete(s.Servers, server)
}

// ResolvePath returns the baseline file path and whether trust checking is
// enabled. GGCODE_MCP_TRUST=off disables; GGCODE_MCP_TRUST=<path> overrides
// the default ~/.ggcode/mcp-tool-trust.json (used by tests and -config
// setups that keep state elsewhere).
func ResolvePath() (string, bool) {
	if v := os.Getenv("GGCODE_MCP_TRUST"); v != "" {
		if v == "off" || v == "0" || v == "false" {
			return "", false
		}
		return v, true
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", false
	}
	return filepath.Join(home, ".ggcode", "mcp-tool-trust.json"), true
}

// ShortHash renders a hash prefix for human-facing notes.
func ShortHash(hash string) string {
	if len(hash) > 8 {
		return hash[:8]
	}
	return hash
}

// Note renders one change as a user-facing trust note.
func (c Change) Note() string {
	switch c.Kind {
	case ChangeModified:
		return fmt.Sprintf("tool %q changed since last connection (description/schema/annotations edited)", c.Tool)
	case ChangeAdded:
		return fmt.Sprintf("tool %q is new since last connection", c.Tool)
	case ChangeRemoved:
		return fmt.Sprintf("tool %q is no longer offered by this server", c.Tool)
	default:
		return fmt.Sprintf("tool %q: unknown change %q", c.Tool, c.Kind)
	}
}
