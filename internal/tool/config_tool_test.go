package tool

import (
	"context"
	"encoding/json"
	"testing"
)

// mockConfigAccess implements ConfigAccess for testing.
type mockConfigAccess struct {
	data      map[string]string
	listData  string
	setErr    error
	deleteErr error
}

func (m *mockConfigAccess) Get(key string) (string, error) {
	v, ok := m.data[key]
	if !ok {
		return "", errNotFound(key)
	}
	return v, nil
}

func (m *mockConfigAccess) Set(key, value string) error {
	if m.setErr != nil {
		return m.setErr
	}
	m.data[key] = value
	return nil
}

func (m *mockConfigAccess) List(section string) (string, error) {
	return m.listData, nil
}

func (m *mockConfigAccess) Delete(key string) error {
	if m.deleteErr != nil {
		return m.deleteErr
	}
	delete(m.data, key)
	return nil
}

type errNotFound string

func (e errNotFound) Error() string { return "not found: " + string(e) }

func newMockConfigAccess() *mockConfigAccess {
	return &mockConfigAccess{
		data: map[string]string{
			"vendor":   "zai",
			"endpoint": "test",
			"model":    "gpt-4",
			"language": "en",
		},
		listData: "== Core ==\n  vendor: zai\n",
	}
}

func TestConfigToolGet(t *testing.T) {
	acc := newMockConfigAccess()
	ct := ConfigTool{Access: acc}

	input, _ := json.Marshal(map[string]string{
		"setting":     "vendor",
		"description": "test",
	})
	result, err := ct.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Errorf("unexpected error result: %s", result.Content)
	}
	if result.Content != "vendor = zai\n" {
		t.Errorf("expected 'vendor = zai\\n', got %q", result.Content)
	}
}

func TestConfigToolSet(t *testing.T) {
	acc := newMockConfigAccess()
	ct := ConfigTool{Access: acc}

	input, _ := json.Marshal(map[string]string{
		"setting":     "language",
		"value":       "zh-CN",
		"description": "test",
	})
	result, err := ct.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Errorf("unexpected error result: %s", result.Content)
	}
	if acc.data["language"] != "zh-CN" {
		t.Errorf("expected language to be zh-CN, got %q", acc.data["language"])
	}
}

func TestConfigToolSetError(t *testing.T) {
	acc := newMockConfigAccess()
	acc.setErr = errNotFound("blocked")
	ct := ConfigTool{Access: acc}

	input, _ := json.Marshal(map[string]string{
		"setting":     "vendor",
		"value":       "bad",
		"description": "test",
	})
	result, err := ct.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error result")
	}
}

func TestConfigToolList(t *testing.T) {
	acc := newMockConfigAccess()
	ct := ConfigTool{Access: acc}

	input, _ := json.Marshal(map[string]interface{}{
		"list":        true,
		"description": "test",
	})
	result, err := ct.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Errorf("unexpected error result: %s", result.Content)
	}
}

func TestConfigToolDelete(t *testing.T) {
	acc := newMockConfigAccess()
	ct := ConfigTool{Access: acc}

	input, _ := json.Marshal(map[string]interface{}{
		"setting":     "mcp_servers.test",
		"delete":      true,
		"description": "test",
	})
	result, err := ct.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Errorf("unexpected error result: %s", result.Content)
	}
}

func TestConfigToolMissingSetting(t *testing.T) {
	acc := newMockConfigAccess()
	ct := ConfigTool{Access: acc}

	input, _ := json.Marshal(map[string]string{
		"description": "test",
	})
	result, err := ct.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error for missing setting")
	}
}

func TestConfigToolSetValueEmpty(t *testing.T) {
	acc := newMockConfigAccess()
	ct := ConfigTool{Access: acc}

	// value="" and not explicitly provided → should read
	input, _ := json.Marshal(map[string]string{
		"setting":     "vendor",
		"description": "test",
	})
	result, err := ct.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Errorf("unexpected error result: %s", result.Content)
	}
	if result.Content != "vendor = zai\n" {
		t.Errorf("expected read mode, got %q", result.Content)
	}
}
