package plugin

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	pb "github.com/topcheer/ggcode/internal/plugin/grpc/proto"
)

// #1863: grpc-go does not recover handler panics - a panicking tool
// implementation used to kill the whole plugin process. The handler must
// convert a panic into the same IsError response an ordinary provider
// error gets.
type panickingProvider struct{}

func (panickingProvider) ListTools() []ToolSpec {
	return []ToolSpec{{Name: "boom", Description: "panics on execute"}}
}

func (panickingProvider) Execute(name string, input json.RawMessage, ctx Context) (*Result, error) {
	panic("kaboom from third-party tool")
}

func (panickingProvider) Shutdown() {}

func TestExecuteRecoversPanicIntoIsError(t *testing.T) {
	srv := &toolServer{provider: panickingProvider{}}
	resp, err := srv.Execute(context.Background(), &pb.ExecuteRequest{ToolName: "boom"})
	if err != nil {
		t.Fatalf("panic must not surface as a gRPC error, got: %v", err)
	}
	if !resp.IsError {
		t.Fatal("panic must convert to IsError response")
	}
	if !strings.Contains(resp.Content, "panic") || !strings.Contains(resp.Content, "kaboom") {
		t.Fatalf("panic response should carry the panic value, got: %q", resp.Content)
	}
}
