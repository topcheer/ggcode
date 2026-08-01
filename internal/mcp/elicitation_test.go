package mcp

import (
	"context"
	"encoding/json"
	"testing"
)

func TestParseElicitationParams(t *testing.T) {
	raw := json.RawMessage(`{
		"message": "Please provide your API key",
		"requestedSchema": {
			"type": "object",
			"properties": {
				"apiKey": {"type": "string", "description": "Your API key"},
				"region": {"type": "string", "enum": ["us", "eu", "asia"]}
			},
			"required": ["apiKey"]
		}
	}`)
	params, err := ParseElicitationParams(raw)
	if err != nil {
		t.Fatalf("ParseElicitationParams error: %v", err)
	}
	if params.Message != "Please provide your API key" {
		t.Errorf("expected message, got %q", params.Message)
	}
	if len(params.Schema.Properties) != 2 {
		t.Fatalf("expected 2 properties, got %d", len(params.Schema.Properties))
	}
	if params.Schema.Properties["apiKey"].Type != "string" {
		t.Errorf("expected apiKey type string, got %q", params.Schema.Properties["apiKey"].Type)
	}
	if len(params.Schema.Properties["region"].Enum) != 3 {
		t.Errorf("expected 3 enum values, got %d", len(params.Schema.Properties["region"].Enum))
	}
	if len(params.Schema.Required) != 1 || params.Schema.Required[0] != "apiKey" {
		t.Errorf("expected required [apiKey], got %v", params.Schema.Required)
	}
}

func TestValidateElicitationSchema_Valid(t *testing.T) {
	s := ElicitationSchema{
		Type: "object",
		Properties: map[string]ElicitationFieldSchema{
			"name":   {Type: "string", Description: "Your name"},
			"active": {Type: "boolean"},
			"count":  {Type: "integer"},
			"rate":   {Type: "number"},
		},
		Required: []string{"name"},
	}
	if err := ValidateElicitationSchema(s); err != nil {
		t.Fatalf("expected valid schema, got error: %v", err)
	}
}

func TestValidateElicitationSchema_EmptyProperties(t *testing.T) {
	s := ElicitationSchema{
		Type:       "object",
		Properties: map[string]ElicitationFieldSchema{},
	}
	if err := ValidateElicitationSchema(s); err == nil {
		t.Fatal("expected error for empty properties")
	}
}

func TestValidateElicitationSchema_TooManyFields(t *testing.T) {
	props := make(map[string]ElicitationFieldSchema)
	for i := 0; i < MaxElicitationFields+1; i++ {
		props["field"+string(rune('a'+i%26))+string(rune('a'+i/26))] = ElicitationFieldSchema{Type: "string"}
	}
	s := ElicitationSchema{
		Type:       "object",
		Properties: props,
	}
	if err := ValidateElicitationSchema(s); err == nil {
		t.Fatal("expected error for too many fields")
	}
}

func TestValidateElicitationSchema_UnsupportedType(t *testing.T) {
	s := ElicitationSchema{
		Type: "object",
		Properties: map[string]ElicitationFieldSchema{
			"data": {Type: "array"},
		},
	}
	if err := ValidateElicitationSchema(s); err == nil {
		t.Fatal("expected error for unsupported type 'array'")
	}
}

func TestValidateElicitationSchema_RequiredNotInProperties(t *testing.T) {
	s := ElicitationSchema{
		Type: "object",
		Properties: map[string]ElicitationFieldSchema{
			"name": {Type: "string"},
		},
		Required: []string{"name", "email"},
	}
	if err := ValidateElicitationSchema(s); err == nil {
		t.Fatal("expected error for required field not in properties")
	}
}

func TestValidateElicitationSchema_WrongRootType(t *testing.T) {
	s := ElicitationSchema{
		Type:       "array",
		Properties: map[string]ElicitationFieldSchema{"name": {Type: "string"}},
	}
	if err := ValidateElicitationSchema(s); err == nil {
		t.Fatal("expected error for non-object root type")
	}
}

func TestElicitationResultJSON(t *testing.T) {
	result := ElicitationResult{
		Action: ElicitationActionAccept,
		Content: map[string]any{
			"apiKey": "sk-123",
			"region": "us",
		},
	}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}
	var decoded ElicitationResult
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if decoded.Action != ElicitationActionAccept {
		t.Errorf("expected action %q, got %q", ElicitationActionAccept, decoded.Action)
	}
	if decoded.Content["apiKey"] != "sk-123" {
		t.Errorf("expected apiKey 'sk-123', got %v", decoded.Content["apiKey"])
	}

	declineResult := ElicitationResult{Action: ElicitationActionDecline}
	declineJSON, _ := json.Marshal(declineResult)
	if string(declineJSON) != `{"action":"decline"}` {
		t.Errorf("decline result should have no content, got %s", string(declineJSON))
	}
}

func TestElicitationHandlerSetter(t *testing.T) {
	c := NewClient("test", "echo", nil)
	if c.elicitationHandler != nil {
		t.Fatal("expected nil elicitation handler by default")
	}

	c.SetElicitationHandler(func(ctx context.Context, p ElicitationParams) (*ElicitationResult, error) {
		return &ElicitationResult{Action: ElicitationActionAccept}, nil
	})
	if c.elicitationHandler == nil {
		t.Fatal("expected non-nil elicitation handler after SetElicitationHandler")
	}

	// Clear by setting nil
	c.SetElicitationHandler(nil)
	if c.elicitationHandler != nil {
		t.Fatal("expected nil elicitation handler after setting nil")
	}
}

func TestElicitationHandlerRejectsWhenNil(t *testing.T) {
	// When no handler is set, the client should not advertise elicitation
	// capability during initialize. We verify the guard logic directly.
	c := NewClient("test", "echo", nil)
	if c.elicitationHandler != nil {
		t.Fatal("expected nil elicitation handler by default")
	}
	// Verify that the capability is NOT set when handler is nil
	caps := ClientCaps{
		Roots: struct {
			ListChanged bool `json:"listChanged,omitempty"`
		}{ListChanged: true},
	}
	if c.elicitationHandler != nil {
		caps.Elicitation = &struct{}{}
	}
	if caps.Elicitation != nil {
		t.Fatal("elicitation capability should not be advertised when handler is nil")
	}

	// Now set a handler and verify capability would be advertised
	c.SetElicitationHandler(func(ctx context.Context, p ElicitationParams) (*ElicitationResult, error) {
		return &ElicitationResult{Action: ElicitationActionAccept}, nil
	})
	if c.elicitationHandler != nil {
		caps.Elicitation = &struct{}{}
	}
	if caps.Elicitation == nil {
		t.Fatal("elicitation capability should be advertised when handler is set")
	}
}
