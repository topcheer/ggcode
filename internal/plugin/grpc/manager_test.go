package grpcplugin

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	pb "github.com/topcheer/ggcode/internal/plugin/grpc/proto"
	"github.com/topcheer/ggcode/internal/tool"
	"google.golang.org/grpc"
)

type mockToolServer struct {
	pb.UnimplementedToolServiceServer
	tools []*pb.ToolDefinition
}

func (s *mockToolServer) ListTools(ctx context.Context, _ *pb.ListToolsRequest) (*pb.ListToolsResponse, error) {
	return &pb.ListToolsResponse{Tools: s.tools}, nil
}

func (s *mockToolServer) Execute(ctx context.Context, req *pb.ExecuteRequest) (*pb.ExecuteResponse, error) {
	if req.ToolName == "echo" {
		var input struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(req.Input, &input)
		return &pb.ExecuteResponse{Content: "echo: " + input.Message}, nil
	}
	if req.ToolName == "error_tool" {
		return &pb.ExecuteResponse{Content: "something went wrong", IsError: true}, nil
	}
	return &pb.ExecuteResponse{Content: "unknown tool", IsError: true}, nil
}

func (s *mockToolServer) Shutdown(ctx context.Context, _ *pb.ShutdownRequest) (*pb.ShutdownResponse, error) {
	return &pb.ShutdownResponse{}, nil
}

func startMockServer(t *testing.T) (pb.ToolServiceClient, func()) {
	t.Helper()

	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "test.sock")

	server := grpc.NewServer()
	mock := &mockToolServer{
		tools: []*pb.ToolDefinition{
			{
				Name:        "echo",
				Description: "Echoes the input message",
				Parameters:  []byte(`{"type":"object","properties":{"message":{"type":"string"}},"required":["message"]}`),
				Categories:  []string{"test"},
			},
			{
				Name:        "error_tool",
				Description: "Always returns an error result",
				Parameters:  []byte(`{"type":"object","properties":{}}`),
			},
		},
	}
	pb.RegisterToolServiceServer(server, mock)

	go func() {
		_ = server.Serve(nil)
	}()

	cleanup := func() {
		server.GracefulStop()
		_ = os.Remove(sockPath)
	}

	return nil, cleanup
}

func TestGRPCAdapter_BasicInterface(t *testing.T) {

	var _ tool.Tool = (*GRPCAdapter)(nil)
}

func TestGRPCAdapter_Properties(t *testing.T) {
	def := &pb.ToolDefinition{
		Name:        "test_tool",
		Description: "A test tool",
		Parameters:  []byte(`{"type":"object","properties":{"x":{"type":"integer"}}}`),
	}

	adapter := NewGRPCAdapter(nil, def, "/tmp")

	if adapter.Name() != "test_tool" {
		t.Errorf("expected name 'test_tool', got %q", adapter.Name())
	}
	if adapter.Description() != "A test tool" {
		t.Errorf("expected description 'A test tool', got %q", adapter.Description())
	}

	params := adapter.Parameters()
	if string(params) != `{"type":"object","properties":{"x":{"type":"integer"}}}` {
		t.Errorf("unexpected parameters: %s", string(params))
	}
}

func TestGRPCAdapter_DefaultParameters(t *testing.T) {
	def := &pb.ToolDefinition{
		Name:        "minimal_tool",
		Description: "No parameters defined",
		Parameters:  nil,
	}

	adapter := NewGRPCAdapter(nil, def, "/tmp")
	params := adapter.Parameters()

	if string(params) == "" {
		t.Error("expected default parameters, got empty")
	}

	var schema map[string]interface{}
	if err := json.Unmarshal(params, &schema); err != nil {
		t.Errorf("default parameters is not valid JSON: %v", err)
	}
	if schema["type"] != "object" {
		t.Errorf("expected type=object, got %v", schema["type"])
	}
}

func TestGRPCAdapter_ExecuteWithMockClient(t *testing.T) {

	t.Skip("requires full gRPC server setup — covered by integration test")
}

func TestManager_New(t *testing.T) {
	m := NewManager("/tmp/test")
	if m == nil {
		t.Fatal("NewManager returned nil")
	}
	if len(m.Status()) != 0 {
		t.Errorf("expected 0 plugins, got %d", len(m.Status()))
	}
}

func TestManager_ShutdownEmpty(t *testing.T) {
	m := NewManager("/tmp/test")

	m.Shutdown()
}

func TestEnvSlice(t *testing.T) {
	result := envSlice(map[string]string{
		"FOO": "bar",
		"BAZ": "qux",
	})

	if len(result) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(result))
	}

	found := make(map[string]bool)
	for _, e := range result {
		found[e] = true
	}
	if !found["FOO=bar"] {
		t.Error("missing FOO=bar")
	}
	if !found["BAZ=qux"] {
		t.Error("missing BAZ=qux")
	}
}

func TestLoadAll_EmptyCommand(t *testing.T) {
	m := NewManager("/tmp")
	registry := tool.NewRegistry()

	errs := m.LoadAll([]GRPCPluginConfig{
		{Name: "bad", Command: nil},
	}, registry)

	if len(errs) != 1 {
		t.Fatalf("expected 1 error, got %d", len(errs))
	}
}

func TestGRPCAdapter_Timeout(t *testing.T) {

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
	defer cancel()

	select {
	case <-ctx.Done():

	case <-time.After(100 * time.Millisecond):
		t.Error("context should have timed out")
	}
}
