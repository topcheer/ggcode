package webui

import (
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

func TestNewServer(t *testing.T) {
	cfg := &config.Config{}
	s := NewServer(cfg)
	if s == nil {
		t.Fatal("expected non-nil server")
	}
}

func TestServerSetters(t *testing.T) {
	cfg := &config.Config{}
	s := NewServer(cfg)

	// Setters should not panic
	s.SetIMActionFn(func(action, adapter string) error { return nil })
	s.SetRestartFn(func() {})
	s.SetA2ADiscoverFn(func() []A2ADiscoveredInstance { return nil })
	s.SetAgent(nil)
	s.SetChatBridge(nil)
}

func TestServerAddr_BeforeStart(t *testing.T) {
	cfg := &config.Config{}
	s := NewServer(cfg)
	if addr := s.Addr(); addr != "" {
		t.Errorf("expected empty addr before start, got %q", addr)
	}
}

func TestServerClose_NotStarted(t *testing.T) {
	cfg := &config.Config{}
	s := NewServer(cfg)
	if err := s.Close(); err != nil {
		t.Errorf("expected nil error for not-started server, got %v", err)
	}
}

func TestServerStartAndClose(t *testing.T) {
	cfg := &config.Config{}
	s := NewServer(cfg)

	addr, err := s.Start("127.0.0.1:0")
	if err != nil {
		t.Fatalf("Start error: %v", err)
	}
	if addr == "" {
		t.Fatal("expected non-empty addr")
	}

	gotAddr := s.Addr()
	if gotAddr == "" {
		t.Error("expected non-empty addr after start")
	}

	if err := s.Close(); err != nil {
		t.Fatalf("Close error: %v", err)
	}
}
