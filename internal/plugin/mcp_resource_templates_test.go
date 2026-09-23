package plugin

// Companion tests for the sa-148 resource templates wire-through:
// listResourceTemplateNames degrades on any error (legacy servers answer
// -32601) and formats named/unnamed templates deterministically.

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/topcheer/ggcode/internal/mcp"
)

func TestListResourceTemplateNames_Nil(t *testing.T) {
	if got := listResourceTemplateNames(nil, nil); len(got) != 0 {
		t.Fatalf("expected empty for empty input, got %v", got)
	}
}

func TestListResourceTemplateNames_ErrorsDegrade(t *testing.T) {
	rpcErr := &mcp.Error{Code: -32601, Message: "Method not found"}
	if got := listResourceTemplateNames(nil, fmt.Errorf("mcp[s]: resources/templates/list: %w", rpcErr)); got != nil {
		t.Fatalf("-32601 must degrade to nil (legacy server), got %v", got)
	}
	if got := listResourceTemplateNames(nil, fmt.Errorf("mcp[s]: boom")); got != nil {
		t.Fatalf("any error must degrade to nil, got %v", got)
	}
}

func TestListResourceTemplateNames_FormatAndSort(t *testing.T) {
	got := listResourceTemplateNames([]mcp.ResourceTemplate{
		{URITemplate: "db://views/{v}", Name: "view"},
		{URITemplate: "db://tables/{t}"},
		{Name: "missing-uri"},
		{URITemplate: "  "},
	}, nil)
	want := []string{"db://tables/{t}", "view (db://views/{v})"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}
