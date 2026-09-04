package plugin

import (
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

// TestIssue1601_GrpcEntryNotFailed pins #1601-A: a type:grpc entry is the
// grpc manager's business - the generic loader must record a successful
// handoff, not a fake command-plugin failure the panel displays.
func TestIssue1601_GrpcEntryNotFailed(t *testing.T) {
	m := NewManager()
	m.LoadAll([]config.PluginConfigEntry{{Name: "p", Type: "grpc", Command: []string{"x"}}})
	if len(m.results) == 0 {
		t.Fatal("expected a recorded result")
	}
	if !m.results[0].Success {
		t.Fatalf("grpc entry must not record failure, got %+v", m.results[0])
	}
}
