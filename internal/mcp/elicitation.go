package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

type ElicitationFieldSchema struct {
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`

	Format string `json:"format,omitempty"`

	Enum []any `json:"enum,omitempty"`
}

func FormatEnumValue(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

type ElicitationSchema struct {
	Type       string                            `json:"type"`
	Properties map[string]ElicitationFieldSchema `json:"properties"`
	Required   []string                          `json:"required,omitempty"`
}

const (
	ElicitationModeForm = "form"
	ElicitationModeURL  = "url"
)

type ElicitationParams struct {
	Mode    string `json:"mode,omitempty"`
	Message string `json:"message"`

	Schema ElicitationSchema `json:"requestedSchema"`

	URL string `json:"url,omitempty"`

	ElicitationID string `json:"elicitationId,omitempty"`
}

func (p ElicitationParams) EffectiveMode() string {
	if p.Mode == ElicitationModeURL {
		return ElicitationModeURL
	}
	return ElicitationModeForm
}

func ValidateElicitationURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("url is not parseable: %w", err)
	}
	if u.Host == "" {
		return fmt.Errorf("url is missing a host")
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		host := u.Hostname()

		if strings.EqualFold(host, "localhost") || host == "127.0.0.1" || host == "::1" {
			return nil
		}
		return fmt.Errorf("url must use https for non-local host %q", host)
	default:
		return fmt.Errorf("url scheme must be https, got %q", u.Scheme)
	}
}

func validateElicitationParams(params ElicitationParams) error {
	if params.EffectiveMode() == ElicitationModeURL {
		if params.ElicitationID == "" {
			return fmt.Errorf("url elicitation requires elicitationId")
		}
		if params.URL == "" {
			return fmt.Errorf("url elicitation requires url")
		}
		return ValidateElicitationURL(params.URL)
	}
	return ValidateElicitationSchema(params.Schema)
}

type ElicitationAction string

const (
	ElicitationActionAccept  ElicitationAction = "accept"
	ElicitationActionDecline ElicitationAction = "decline"
	ElicitationActionCancel  ElicitationAction = "cancel"
)

type ElicitationResult struct {
	Action  ElicitationAction `json:"action"`
	Content map[string]any    `json:"content,omitempty"`
}

type ElicitationHandler func(ctx context.Context, params ElicitationParams) (*ElicitationResult, error)

func ParseElicitationParams(raw json.RawMessage) (ElicitationParams, error) {
	var p ElicitationParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return p, err
	}
	return p, nil
}

const MaxElicitationFields = 20

func ValidateElicitationSchema(s ElicitationSchema) error {
	if s.Type != "" && s.Type != "object" {
		return fmt.Errorf("elicitation schema type must be \"object\", got %q", s.Type)
	}
	if len(s.Properties) == 0 {
		return fmt.Errorf("elicitation schema must have at least one property")
	}
	if len(s.Properties) > MaxElicitationFields {
		return fmt.Errorf("elicitation schema has %d properties, max is %d", len(s.Properties), MaxElicitationFields)
	}

	allowedTypes := map[string]bool{
		"string":  true,
		"number":  true,
		"integer": true,
		"boolean": true,
	}

	for name, field := range s.Properties {
		if name == "" {
			return fmt.Errorf("elicitation schema has empty property name")
		}
		if !allowedTypes[field.Type] {
			return fmt.Errorf("elicitation field %q has unsupported type %q (allowed: string, number, integer, boolean)", name, field.Type)
		}

		for _, opt := range field.Enum {
			if s, isStr := opt.(string); isStr && s == "" {
				return fmt.Errorf("elicitation field %q has an empty-string enum member", name)
			}
		}
	}

	propSet := make(map[string]bool, len(s.Properties))
	for k := range s.Properties {
		propSet[k] = true
	}
	for _, req := range s.Required {
		if !propSet[req] {
			return fmt.Errorf("elicitation required field %q not found in properties", req)
		}
	}

	return nil
}
