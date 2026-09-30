package provider

import (
	"bytes"
	"encoding/json"
	"sync"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/topcheer/ggcode/internal/debug"
	"google.golang.org/genai"
)

// Tool-schema memoization.
//
// Every request rebuilds the tool list (~191 registered tools plus
// MCP/plugin tools), yet each tool's schema is byte-stable for the life of
// its registration. The conversion paths re-parsed every schema on every
// request — json.Valid plus strict preparation on the OpenAI path, and a
// full json.Unmarshal into the SDK schema types on the Anthropic and Gemini
// paths — spending milliseconds of CPU and real GC churn per turn for
// identical results (and re-emitting the same invalid-schema warnings).
//
// Each memo keys on the tool name and verifies the schema bytes are
// unchanged, so a schema edited or re-registered mid-session simply
// recomputes. Memoized values are shared across concurrent requests and MUST
// be treated as read-only; the one call site that augments a value (the
// Anthropic strict branch adds "additionalProperties" to ExtraFields) builds
// a fresh map instead of mutating the shared one.

// toolSchemaMemoLimit bounds each memo. Built-in tool sets are small, but
// MCP servers can churn names across a long session; a wholesale reset
// (rather than LRU bookkeeping) is fine because misses just recompute.
const toolSchemaMemoLimit = 1024

type memoEntry[T any] struct {
	raw []byte // schema bytes the value was computed from (identity key)
	val T
}

type schemaMemo[T any] struct {
	mu      sync.RWMutex
	entries map[string]memoEntry[T]
}

// get returns the memoized compute(raw) for this tool, recomputing when the
// schema bytes differ from the cached entry. compute must be pure.
func (m *schemaMemo[T]) get(name string, raw json.RawMessage, compute func(json.RawMessage) T) T {
	if raw == nil {
		return compute(raw)
	}
	m.mu.RLock()
	e, ok := m.entries[name]
	m.mu.RUnlock()
	if ok && bytes.Equal(e.raw, raw) {
		return e.val
	}
	val := compute(raw)
	m.mu.Lock()
	if m.entries == nil {
		m.entries = make(map[string]memoEntry[T], 64)
	} else if len(m.entries) >= toolSchemaMemoLimit {
		m.entries = make(map[string]memoEntry[T], 64)
	}
	m.entries[name] = memoEntry[T]{raw: append([]byte(nil), raw...), val: val}
	m.mu.Unlock()
	return val
}

// ---- OpenAI path: schema validity check + fallback ----

var openaiToolParamsMemo schemaMemo[json.RawMessage]

// validatedToolParams returns the schema bytes to send on the OpenAI wire
// path. Empty or invalid JSON (MCP servers and plugins can produce it, and
// json.RawMessage.MarshalJSON panics on it) falls back to an empty object
// schema instead of failing serialization for every tool. Memoized per tool.
func validatedToolParams(name string, raw json.RawMessage) json.RawMessage {
	return openaiToolParamsMemo.get(name, raw, func(r json.RawMessage) json.RawMessage {
		if len(bytes.TrimSpace(r)) == 0 || !json.Valid(r) {
			debug.Log("openai", "WARNING: tool %q has invalid JSON schema (%d bytes), using empty object fallback", name, len(r))
			return json.RawMessage(`{"type":"object","properties":{}}`)
		}
		return r
	})
}

// ---- Strict preparation (OpenAI + Anthropic paths) ----

type strictSchemaResult struct {
	schema json.RawMessage
	ok     bool
}

var strictToolSchemaMemo schemaMemo[strictSchemaResult]

// prepareStrictToolSchemaCached is PrepareStrictToolSchema memoized per
// tool. On ok=false the returned schema is the input unchanged (matching the
// uncached function), so callers keep sending the tool without `strict`.
func prepareStrictToolSchemaCached(name string, raw json.RawMessage) (json.RawMessage, bool) {
	v := strictToolSchemaMemo.get(name, raw, func(r json.RawMessage) strictSchemaResult {
		s, ok := PrepareStrictToolSchema(name, r)
		return strictSchemaResult{schema: s, ok: ok}
	})
	return v.schema, v.ok
}

// ---- Anthropic path: SDK param unmarshal ----

type anthropicSchemaResult struct {
	schema anthropic.ToolInputSchemaParam
	valid  bool
}

var anthropicInputSchemaMemo schemaMemo[anthropicSchemaResult]

// anthropicToolInputSchema unmarshals a tool schema into the SDK param type,
// memoized per tool. The returned ExtraFields map (populated from unknown
// root keys by the SDK unmarshaler) is shared: callers that add strict-mode
// extras must build their own map rather than mutate it.
func anthropicToolInputSchema(name string, raw json.RawMessage) (anthropic.ToolInputSchemaParam, bool) {
	v := anthropicInputSchemaMemo.get(name, raw, func(r json.RawMessage) anthropicSchemaResult {
		inputSchema := anthropic.ToolInputSchemaParam{Type: "object"}
		if json.Unmarshal(r, &inputSchema) == nil {
			return anthropicSchemaResult{schema: inputSchema, valid: true}
		}
		return anthropicSchemaResult{schema: inputSchema}
	})
	return v.schema, v.valid
}

// ---- Gemini path: SDK schema unmarshal ----

var geminiSchemaMemo schemaMemo[*genai.Schema]

// geminiToolSchema unmarshals a tool schema into genai.Schema, memoized per
// tool. Returns nil when the schema does not unmarshal (the declaration is
// then sent without parameters, as before).
func geminiToolSchema(name string, raw json.RawMessage) *genai.Schema {
	return geminiSchemaMemo.get(name, raw, func(r json.RawMessage) *genai.Schema {
		schema := &genai.Schema{}
		if json.Unmarshal(r, schema) == nil {
			return schema
		}
		return nil
	})
}
