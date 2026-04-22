package tool

import (
	"context"
	"encoding/json"
	"testing"
)

// mockConfigAccess implements ConfigAccess for testing.
type mockConfigAccess struct {
	data map[string]string
}

func newMockConfigAccess() *mockConfigAccess {
	return &mockConfigAccess{data: map[string]string{}}
}

func (m *mockConfigAccess) Get(key string) (string, bool) {
	v, ok := m.data[key]
	return v, ok
}

func (m *mockConfigAccess) Set(key, value string) error {
	m.data[key] = value
	return nil
}

func (m *mockConfigAccess) List() map[string]string {
	return m.data
}

func TestConfigTool_Read(t *testing.T) {
	acc := newMockConfigAccess()
	acc.data["theme"] = "dark"
	ct := ConfigTool{Access: acc}

	input, _ := json.Marshal(map[string]interface{}{
		"setting": "theme",
	})
	result, err := ct.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
	if !containsAny(result.Content, "dark") {
		t.Errorf("expected 'dark', got: %s", result.Content)
	}
}

func TestConfigTool_Write(t *testing.T) {
	acc := newMockConfigAccess()
	ct := ConfigTool{Access: acc}

	input, _ := json.Marshal(map[string]interface{}{
		"setting": "lang",
		"value":   "zh",
	})
	result, err := ct.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
	if acc.data["lang"] != "zh" {
		t.Errorf("expected lang=zh, got %s", acc.data["lang"])
	}
}

func TestConfigTool_NotFound(t *testing.T) {
	acc := newMockConfigAccess()
	ct := ConfigTool{Access: acc}

	input, _ := json.Marshal(map[string]interface{}{
		"setting": "nonexistent",
	})
	result, err := ct.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error for missing setting")
	}
}

func TestConfigTool_List(t *testing.T) {
	acc := newMockConfigAccess()
	acc.data["a"] = "1"
	acc.data["b"] = "2"
	ct := ConfigTool{Access: acc}

	input, _ := json.Marshal(map[string]interface{}{
		"list": true,
	})
	result, err := ct.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
	if !containsAny(result.Content, "a:", "b:") {
		t.Errorf("expected both keys, got: %s", result.Content)
	}
}

func TestConfigTool_ListEmpty(t *testing.T) {
	acc := newMockConfigAccess()
	ct := ConfigTool{Access: acc}

	input, _ := json.Marshal(map[string]interface{}{
		"list": true,
	})
	result, err := ct.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !containsAny(result.Content, "No configuration") {
		t.Errorf("expected empty message, got: %s", result.Content)
	}
}

func TestConfigTool_MissingSetting(t *testing.T) {
	acc := newMockConfigAccess()
	ct := ConfigTool{Access: acc}

	result, err := ct.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error for missing setting")
	}
}
