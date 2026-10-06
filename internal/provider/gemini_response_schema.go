package provider

// Gemini structured outputs (sa-229 follow-up to #3312): constrain the
// final assistant response via generationConfig.responseSchema. Gemini's
// schema dialect is OpenAPI-style with STRING-typed enums (not JSON
// Schema), so a recursive converter maps the OpenAI-style JSON Schema
// that --output-schema accepts onto genai.Schema. Unsupported keywords
// (additionalProperties, $schema, etc.) are dropped; unknown types are
// skipped rather than risk a 400.

import (
	"encoding/json"

	"github.com/topcheer/ggcode/internal/debug"

	"google.golang.org/genai"
)

// SetResponseSchema installs a JSON Schema constraining the final
// assistant response (Gemini responseSchema). nil/empty disables.
func (p *GeminiProvider) SetResponseSchema(schema json.RawMessage) {
	p.responseSchema = schema
}

// ResponseSchema returns the installed response schema (nil = none).
func (p *GeminiProvider) ResponseSchema() json.RawMessage { return p.responseSchema }

// applyResponseSchema arms responseMimeType+responseSchema on the
// generateContent config. No-op without an installed schema.
func (p *GeminiProvider) applyResponseSchema(config *genai.GenerateContentConfig) {
	if len(p.responseSchema) == 0 {
		return
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(p.responseSchema, &raw); err != nil {
		debug.Log("gemini", "responseSchema: root is not an object, ignoring: %v", err)
		return
	}
	schema := jsonSchemaToGenai(raw)
	if schema == nil {
		debug.Log("gemini", "responseSchema: conversion produced nil schema, ignoring")
		return
	}
	config.ResponseMIMEType = "application/json"
	config.ResponseSchema = schema
}

// jsonSchemaToGenai converts a JSON-Schema object (as decoded raw map)
// into a genai.Schema. Returns nil when the node's type is unsupported.
func jsonSchemaToGenai(raw map[string]json.RawMessage) *genai.Schema {
	out := &genai.Schema{}
	var t string
	if tb, ok := raw["type"]; ok {
		_ = json.Unmarshal(tb, &t)
	}
	switch t {
	case "object":
		out.Type = genai.TypeObject
	case "array":
		out.Type = genai.TypeArray
	case "string":
		out.Type = genai.TypeString
	case "integer":
		out.Type = genai.TypeInteger
	case "number":
		out.Type = genai.TypeNumber
	case "boolean":
		out.Type = genai.TypeBoolean
	default:
		return nil
	}
	if db, ok := raw["description"]; ok {
		_ = json.Unmarshal(db, &out.Description)
	}
	if rb, ok := raw["required"]; ok {
		_ = json.Unmarshal(rb, &out.Required)
	}
	if ib, ok := raw["items"]; ok {
		var itemRaw map[string]json.RawMessage
		if err := json.Unmarshal(ib, &itemRaw); err == nil {
			if item := jsonSchemaToGenai(itemRaw); item != nil {
				out.Items = item
			}
		}
	}
	if pb, ok := raw["properties"]; ok {
		var props map[string]json.RawMessage
		if err := json.Unmarshal(pb, &props); err == nil {
			out.Properties = make(map[string]*genai.Schema, len(props))
			for name, propRaw := range props {
				var propNode map[string]json.RawMessage
				if err := json.Unmarshal(propRaw, &propNode); err != nil {
					continue
				}
				if prop := jsonSchemaToGenai(propNode); prop != nil {
					out.Properties[name] = prop
				}
			}
		}
	}
	if eb, ok := raw["enum"]; ok {
		var enums []string
		if err := json.Unmarshal(eb, &enums); err == nil {
			out.Enum = enums
		}
	}
	return out
}
