package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

type stubTool struct{ name string }

func (s stubTool) Name() string                { return s.name }
func (s stubTool) Description() string         { return "stub " + s.name }
func (s stubTool) Parameters() json.RawMessage { return nil }
func (s stubTool) Execute(ctx context.Context, args json.RawMessage) (Result, error) {
	return Result{Content: "ok"}, nil
}

func TestToDefinitionsDescriptionAugmenter(t *testing.T) {
	r := NewRegistry()
	r.Register(stubTool{name: "alpha"})
	r.Register(stubTool{name: "beta"})

	for _, d := range r.ToDefinitions() {
		if strings.Contains(d.Description, "usage hints") {
			t.Fatalf("unexpected overlay without augmenter: %q", d.Description)
		}
	}

	r.SetDescriptionAugmenter(func(name string) string {
		if name == "alpha" {
			return "\n[usage hints from past failures]\n- parameter x must be N"
		}
		return ""
	})

	defs := r.ToDefinitions()
	byName := map[string]string{}
	for _, d := range defs {
		byName[d.Name] = d.Description
	}
	if !strings.Contains(byName["alpha"], "[usage hints from past failures]") {
		t.Fatalf("alpha missing overlay: %q", byName["alpha"])
	}
	if !strings.HasPrefix(byName["alpha"], "stub alpha") {
		t.Fatalf("overlay must append, not replace: %q", byName["alpha"])
	}
	if byName["beta"] != "stub beta" {
		t.Fatalf("beta must be untouched: %q", byName["beta"])
	}
}
