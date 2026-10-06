package provider

// Gemini structured outputs probes (sa-229): schema conversion fidelity
// (type mapping, nesting, enum, required, unsupported-type skip) and
// config arming (no-op without schema, responseMimeType pairing).

import (
	"encoding/json"
	"testing"

	"google.golang.org/genai"
)

func TestJSONSchemaToGenaiConversion(t *testing.T) {
	raw := json.RawMessage(`{
		"type":"object",
		"required":["summary","tags"],
		"properties":{
			"summary":{"type":"string","description":"one-line summary"},
			"tags":{"type":"array","items":{"type":"string"}},
			"score":{"type":"number"},
			"ok":{"type":"boolean"},
			"meta":{"type":"object","properties":{"k":{"type":"integer"}}},
			"weird":{"type":"nullmode","description":"unsupported type must be skipped"}
		},
		"additionalProperties":false
	}`)
	var node map[string]json.RawMessage
	if err := json.Unmarshal(raw, &node); err != nil {
		t.Fatal(err)
	}
	s := jsonSchemaToGenai(node)
	if s == nil || s.Type != genai.TypeObject {
		t.Fatalf("root type = %v, want OBJECT", s)
	}
	if len(s.Required) != 2 || s.Required[0] != "summary" {
		t.Errorf("required = %v", s.Required)
	}
	sum, ok := s.Properties["summary"]
	if !ok || sum.Type != genai.TypeString || sum.Description != "one-line summary" {
		t.Errorf("summary prop wrong: %+v", sum)
	}
	tags, ok := s.Properties["tags"]
	if !ok || tags.Type != genai.TypeArray || tags.Items == nil || tags.Items.Type != genai.TypeString {
		t.Errorf("tags prop wrong: %+v", tags)
	}
	if m := s.Properties["meta"]; m == nil || m.Properties["k"].Type != genai.TypeInteger {
		t.Errorf("nested meta.k wrong: %+v", m)
	}
	if _, present := s.Properties["weird"]; present {
		t.Errorf("unsupported type must be skipped, not emitted")
	}
	if len(s.Properties) != 5 {
		t.Errorf("properties count = %d, want 5 (unsupported skipped)", len(s.Properties))
	}
}

func TestJSONSchemaToGenaiEnumAndUnknownRoot(t *testing.T) {
	var node map[string]json.RawMessage
	_ = json.Unmarshal([]byte(`{"type":"string","enum":["a","b"]}`), &node)
	s := jsonSchemaToGenai(node)
	if s == nil || len(s.Enum) != 2 {
		t.Fatalf("enum conversion failed: %+v", s)
	}
	var noType map[string]json.RawMessage
	_ = json.Unmarshal([]byte(`{"description":"no type"}`), &noType)
	if jsonSchemaToGenai(noType) != nil {
		t.Errorf("node without type must convert to nil")
	}
}

func TestGeminiApplyResponseSchemaArming(t *testing.T) {
	p, err := NewGeminiProviderWithBaseURL("k", "gemini-2.5-pro", 0, "http://localhost")
	if err != nil {
		t.Fatal(err)
	}
	cfg := &genai.GenerateContentConfig{}
	p.applyResponseSchema(cfg) // no schema: no-op
	if cfg.ResponseSchema != nil || cfg.ResponseMIMEType != "" {
		t.Fatalf("arming without schema must be a no-op")
	}

	schema := json.RawMessage(`{"type":"object","properties":{"summary":{"type":"string"}},"required":["summary"]}`)
	p.SetResponseSchema(schema)
	if string(p.ResponseSchema()) != string(schema) {
		t.Fatalf("getter round-trip mismatch")
	}
	cfg = &genai.GenerateContentConfig{}
	p.applyResponseSchema(cfg)
	if cfg.ResponseSchema == nil || cfg.ResponseSchema.Type != genai.TypeObject {
		t.Fatalf("responseSchema not armed")
	}
	if cfg.ResponseMIMEType != "application/json" {
		t.Errorf("responseMimeType = %q, want application/json", cfg.ResponseMIMEType)
	}

	// Non-object root (JSON Schema allows it, Gemini responseSchema does
	// not) must be ignored rather than sent as a malformed config.
	p.SetResponseSchema(json.RawMessage(`[1,2,3]`))
	cfg = &genai.GenerateContentConfig{}
	p.applyResponseSchema(cfg)
	if cfg.ResponseSchema != nil {
		t.Errorf("array root must be ignored")
	}
}
