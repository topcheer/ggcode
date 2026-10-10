package mcp

// #3808 companion: x-mcp-header annotations inside array items collect a
// path that skips the array dimension (args[name][0][field]) which
// lookupPath can never resolve. Previously the header silently vanished
// with ZERO violations; now collection rejects the form loudly.

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestIssue3808_ArrayItemAnnotationFlagged(t *testing.T) {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"regions": {
				"type": "array",
				"items": {
					"type": "object",
					"properties": {
						"region": {"type": "string", "x-mcp-header": "Region"}
					}
				}
			}
		}
	}`)
	// Runtime: headers must stay empty (unchanged), but collection now
	// reports the violation instead of silence.
	v := headerAnnotationViolations(schema)
	if len(v) != 1 || !strings.Contains(v[0], "array items") {
		t.Fatalf("array-item annotation must be flagged, got %v", v)
	}
	args := map[string]interface{}{
		"regions": []interface{}{map[string]interface{}{"region": "eu-1"}},
	}
	if h := buildMCPParamHeaders(schema, args); len(h) != 0 {
		t.Fatalf("array-element annotation must not emit headers, got %v", h)
	}
	// Conformant sibling shapes stay silent and keep working.
	okSchema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"options": {
				"type": "object",
				"properties": {
					"region": {"type": "string", "x-mcp-header": "Region"}
				}
			}
		}
	}`)
	if v := headerAnnotationViolations(okSchema); len(v) != 0 {
		t.Fatalf("plain nested annotation must stay conformant, got %v", v)
	}
	h := buildMCPParamHeaders(okSchema, map[string]interface{}{
		"options": map[string]interface{}{"region": "eu-1"},
	})
	if len(h) != 1 || h[0][0] != "Mcp-Param-Region" || h[0][1] != "eu-1" {
		t.Fatalf("nested object annotation regression, got %v", h)
	}
	// Plain array-typed property WITHOUT items annotations stays clean.
	arrSchema := json.RawMessage(`{"type":"object","properties":{"tags":{"type":"array","items":{"type":"string"}}}}`)
	if v := headerAnnotationViolations(arrSchema); len(v) != 0 {
		t.Fatalf("annotation-free array must stay conformant, got %v", v)
	}
}
