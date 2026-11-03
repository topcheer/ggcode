package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

type zz4718Stub struct{}

func (zz4718Stub) Name() string { return "zz4718_probe" }
func (zz4718Stub) Description() string {
	return "stub tool for clone-augment pinning"
}
func (zz4718Stub) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object"}`)
}
func (zz4718Stub) Execute(ctx context.Context, args json.RawMessage) (Result, error) {
	return Result{Content: "ok"}, nil
}

func TestIssue4718_ClonePropagatesDescAugment(t *testing.T) {
	parent := NewRegistry()
	if err := parent.Register(zz4718Stub{}); err != nil {
		t.Fatal(err)
	}
	parent.SetDescriptionAugmenter(func(name string) string {
		return "\nlearned-hint: prefer -p=1"
	})

	clone := parent.Clone()

	for _, reg := range []*Registry{parent, clone} {
		defs := reg.ToDefinitions()
		var found bool
		for _, d := range defs {
			if d.Name == "zz4718_probe" && strings.Contains(d.Description, "learned-hint: prefer -p=1") {
				found = true
			}
		}
		if !found {
			t.Fatalf("registry (%p) tool definition missing learned-hint overlay", reg)
		}
	}
}
