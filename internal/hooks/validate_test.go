package hooks

import (
	"testing"
)

func TestValidateHooks_AllValid(t *testing.T) {
	cfg := HookConfig{
		PreToolUse: []Hook{
			{Match: "write_file", Command: "echo ok"},
		},
		PostToolUse: []Hook{
			{Match: "write_file", Command: "echo ok", InjectOutput: true},
		},
		OnAgentStop: []Hook{
			{Match: "*", Type: HookTypeHTTP, URL: "https://example.com"},
		},
	}
	errs := ValidateHooks(cfg)
	if len(errs) != 0 {
		t.Errorf("expected no errors, got %v", errs)
	}
}

func TestValidateHooks_HTTPMissingURL(t *testing.T) {
	cfg := HookConfig{
		PreToolUse: []Hook{
			{Match: "write_file", Type: HookTypeHTTP},
		},
	}
	errs := ValidateHooks(cfg)
	if len(errs) != 1 {
		t.Fatalf("expected 1 error, got %d: %v", len(errs), errs)
	}
	if errs[0] != "pre_tool_use[0]: type=http requires url" {
		t.Errorf("unexpected error: %s", errs[0])
	}
}

func TestValidateHooks_CommandMissingCommand(t *testing.T) {
	cfg := HookConfig{
		PostToolUse: []Hook{
			{Match: "write_file"},
		},
	}
	errs := ValidateHooks(cfg)
	if len(errs) != 1 {
		t.Fatalf("expected 1 error, got %d: %v", len(errs), errs)
	}
}

func TestValidateHooks_MissingMatch(t *testing.T) {
	cfg := HookConfig{
		PreToolUse: []Hook{
			{Command: "echo ok"},
		},
	}
	errs := ValidateHooks(cfg)
	if len(errs) != 1 {
		t.Fatalf("expected 1 error, got %d: %v", len(errs), errs)
	}
}

func TestValidateHooks_InvalidTimeout(t *testing.T) {
	cfg := HookConfig{
		PreToolUse: []Hook{
			{Match: "*", Type: HookTypeHTTP, URL: "https://example.com", Timeout: "not-a-duration"},
		},
	}
	errs := ValidateHooks(cfg)
	if len(errs) != 1 {
		t.Fatalf("expected 1 error, got %d: %v", len(errs), errs)
	}
}

func TestValidateHooks_InjectOutputWrongEvent(t *testing.T) {
	cfg := HookConfig{
		PreToolUse: []Hook{
			{Match: "*", Command: "echo ok", InjectOutput: true},
		},
	}
	errs := ValidateHooks(cfg)
	if len(errs) != 1 {
		t.Fatalf("expected 1 error, got %d: %v", len(errs), errs)
	}
}

func TestValidateHooks_Empty(t *testing.T) {
	cfg := HookConfig{}
	errs := ValidateHooks(cfg)
	if len(errs) != 0 {
		t.Errorf("empty config should have no errors, got %v", errs)
	}
}
