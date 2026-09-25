package config

// Unknown-config-key detection.
//
// ggcode loads its YAML config with a non-strict yaml.Unmarshal, which
// silently drops any key that does not match the Config schema. A misspelled
// key (e.g. `modle:` instead of `model:`) is therefore ignored forever and
// the user only sees the symptom ("I set X but nothing changed") with no
// diagnostic pointing at the typo.
//
// This file closes that gap with two surfaces:
//
//  1. Load() logs every unknown key it finds in the main config file to the
//     debug log (diagnostics must never break loading, so this is log-only).
//  2. `ggcode doctor` reports the same findings, with line numbers and a
//     nearest-known-key suggestion, as part of its config check.
//
// The known-key set is derived from the Config struct itself via reflection
// over `yaml` struct tags, so it stays in sync with the schema without a
// hand-maintained list. Map-typed sections (vendors.*, tool_permissions,
// lsp_servers, mcp_servers[].env, ...) accept arbitrary user keys by
// construction; their *value* structs are still validated one level down.
// Fields typed map[string]string or interface{} (including inline maps such
// as PluginConfigEntry.Extra) are free-form and never produce findings.

import (
	"fmt"
	"reflect"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"

	"github.com/topcheer/ggcode/internal/debug"
)

// UnknownKeyFinding describes one key present in a config file that does not
// match any field of the Config schema at that position.
type UnknownKeyFinding struct {
	// Path is the dotted path of the key, e.g. "modle" or "lsp_servers.go.bogus".
	Path string
	// Line is the 1-based line of the key in the file (0 if not resolvable).
	Line int
	// Hint is the nearest known key at the same level, "" when none is close.
	Hint string
}

// keyNode is one level of the known-key trie. literal children are fixed
// struct fields; wildcard is reached by any key (map sections); freeLeaf
// marks levels whose children are user-defined or opaque.
type keyNode struct {
	literal  map[string]*keyNode
	wildcard *keyNode
	freeLeaf bool
}

var (
	knownKeysOnce sync.Once
	knownKeysRoot *keyNode
)

// knownConfigKeys builds (once) the known-key trie from the Config schema.
func knownConfigKeys() *keyNode {
	knownKeysOnce.Do(func() {
		knownKeysRoot = childKeyNode(reflect.TypeOf(Config{}), map[reflect.Type]*keyNode{})
	})
	return knownKeysRoot
}

func derefType(t reflect.Type) reflect.Type {
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t
}

// isFreeForm reports whether values of type t are opaque to validation
// (primitives, interface{}, maps of primitives, slices of those).
func isFreeForm(t reflect.Type) bool {
	t = derefType(t)
	if t == nil {
		return true
	}
	switch t.Kind() {
	case reflect.Struct:
		return false
	case reflect.Slice, reflect.Array:
		return isFreeForm(t.Elem())
	case reflect.Map:
		return isFreeForm(t.Elem())
	default:
		return true
	}
}

// childKeyNode returns the trie node describing values of type t.
func childKeyNode(t reflect.Type, visiting map[reflect.Type]*keyNode) *keyNode {
	t = derefType(t)
	if t == nil {
		return &keyNode{freeLeaf: true}
	}
	switch t.Kind() {
	case reflect.Struct:
		return buildStructNode(t, visiting)
	case reflect.Map:
		n := &keyNode{}
		elem := derefType(t.Elem())
		if isFreeForm(elem) {
			n.wildcard = &keyNode{freeLeaf: true}
		} else {
			n.wildcard = buildStructNode(elem, visiting)
		}
		return n
	case reflect.Slice, reflect.Array:
		return childKeyNode(t.Elem(), visiting)
	default:
		return &keyNode{freeLeaf: true}
	}
}

// buildStructNode reflects a struct type into a trie node.
func buildStructNode(t reflect.Type, visiting map[reflect.Type]*keyNode) *keyNode {
	if node, ok := visiting[t]; ok {
		return node
	}
	n := &keyNode{literal: map[string]*keyNode{}}
	visiting[t] = n
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.PkgPath != "" { // unexported (internal state like diskStrSnap)
			continue
		}
		tag := f.Tag.Get("yaml")
		name, opts, _ := strings.Cut(tag, ",")
		if name == "-" {
			continue
		}
		ft := derefType(f.Type)
		inline := strings.Contains(opts, "inline")
		if inline {
			if ft != nil && ft.Kind() == reflect.Map {
				// Inline map absorbs arbitrary keys at this level
				// (e.g. PluginConfigEntry.Extra).
				n.freeLeaf = true
				continue
			}
			if ft != nil && ft.Kind() == reflect.Struct {
				// Inline struct: merge its fields into this level.
				merged := buildStructNode(ft, visiting)
				for k, v := range merged.literal {
					if _, exists := n.literal[k]; !exists {
						n.literal[k] = v
					}
				}
				if merged.freeLeaf {
					n.freeLeaf = true
				}
			}
			continue
		}
		if name == "" {
			// yaml.v3 lowercases the Go field name when no tag is present.
			name = strings.ToLower(f.Name)
		}
		n.literal[name] = childKeyNode(f.Type, visiting)
	}
	return n
}

// FindUnknownKeys parses YAML config data and returns every key that does not
// match the Config schema at its position. Parse errors yield nil - surfacing
// syntax errors is the loader's job, not the diagnostics walker's.
func FindUnknownKeys(data []byte) []UnknownKeyFinding {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return nil
	}
	var findings []UnknownKeyFinding
	walkYAML(doc.Content[0], knownConfigKeys(), "", &findings)
	return findings
}

func walkYAML(node *yaml.Node, known *keyNode, prefix string, out *[]UnknownKeyFinding) {
	if node == nil {
		return
	}
	if node.Kind == yaml.AliasNode && node.Alias != nil {
		node = node.Alias
	}
	switch node.Kind {
	case yaml.DocumentNode, yaml.SequenceNode:
		for _, c := range node.Content {
			walkYAML(c, known, prefix, out)
		}
	case yaml.MappingNode:
		if known == nil || known.freeLeaf {
			return
		}
		for i := 0; i+1 < len(node.Content); i += 2 {
			k, v := node.Content[i], node.Content[i+1]
			if k.Tag == "!!merge" {
				continue // merge keys select other nodes, they are not schema keys
			}
			name := k.Value
			child, ok := known.literal[name]
			if !ok {
				if known.wildcard != nil {
					child = known.wildcard // map key: valid by construction
				} else {
					*out = append(*out, UnknownKeyFinding{
						Path: joinKeyPath(prefix, name),
						Line: k.Line,
						Hint: suggestConfigKey(name, known.literal),
					})
					continue // unknown subtree: flag the top, do not descend
				}
			}
			walkYAML(v, child, joinKeyPath(prefix, name), out)
		}
	}
}

func joinKeyPath(prefix, name string) string {
	if prefix == "" {
		return name
	}
	return prefix + "." + name
}

// suggestConfigKey returns the closest known key at the same level when it is
// within edit-distance tolerance, "" otherwise.
func suggestConfigKey(name string, literal map[string]*keyNode) string {
	best, bestDist := "", -1
	lower := strings.ToLower(name)
	for k := range literal {
		d := levenshtein(lower, strings.ToLower(k))
		if bestDist == -1 || d < bestDist {
			best, bestDist = k, d
		}
	}
	allow := 2
	if len(lower) >= 8 {
		allow = 3
	}
	// bestDist == 0 means the unknown key differs only in case from a known
	// key (yaml keys are case-sensitive) - a very actionable suggestion.
	if best == "" || bestDist > allow {
		return ""
	}
	return best
}

func levenshtein(a, b string) int {
	if a == b {
		return 0
	}
	if len(a) == 0 {
		return len(b)
	}
	if len(b) == 0 {
		return len(a)
	}
	prev := make([]int, len(b)+1)
	curr := make([]int, len(b)+1)
	for j := 0; j <= len(b); j++ {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		curr[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			curr[j] = min3(curr[j-1]+1, prev[j]+1, prev[j-1]+cost)
		}
		prev, curr = curr, prev
	}
	return prev[len(b)]
}

func min3(a, b, c int) int {
	if b < a {
		a = b
	}
	if c < a {
		a = c
	}
	return a
}

// logUnknownConfigKeys reports unknown keys in the config file to the debug
// log. Findings never fail loading: a diagnostic must not become an outage.
func logUnknownConfigKeys(path string, data []byte) {
	for _, f := range FindUnknownKeys(data) {
		msg := fmt.Sprintf("unknown config key %q (%s:%d)", f.Path, path, f.Line)
		if f.Hint != "" {
			msg += fmt.Sprintf(" - did you mean %q?", f.Hint)
		}
		debug.Log("config", "%s; it is silently ignored by the config schema", msg)
	}
}
