//go:build integration_local

package a2a

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestIssue1073_StreamingSendsRealStatus tests that tasks/resubscribe sends the
// actual task status instead of hardcoded working. #1073
func TestIssue1073_StreamingSendsRealStatus(t *testing.T) {
	handler := NewTaskHandler(".", nil, nil)
	cfg := ServerConfig{
		Port:   0,
		APIKey: "test-key",
	}
	srv := NewServer(cfg, handler)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()

	// Create a task that will reach input-required state.
	taskID := "task-input-required"
	now := time.Now()
	task := &Task{
		ID:        taskID,
		ContextID: "ctx-test-1073",
		Skill:     SkillFullTask,
		History:   []Message{{Role: "user", Parts: []Part{{Kind: "text", Text: "test"}}}},
		Status: TaskStatus{
			State:     TaskStateInputRequired,
			Timestamp: now,
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
	handler.mu.Lock()
	handler.tasks[taskID] = task
	handler.mu.Unlock()

	// Subscribe to the task stream via tasks/resubscribe.
	params, _ := json.Marshal(TaskSubscriptionParams{ID: taskID})
	body, _ := json.Marshal(JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`"1"`),
		Method:  "tasks/resubscribe",
		Params:  json.RawMessage(params),
	})
	r, _ := http.NewRequest("POST", srv.Endpoint()+"/", bytes.NewReader(body))
	r.Header.Set("X-API-Key", "test-key")
	r.Header.Set("Content-Type", "application/json")

	// Use httptest.ResponseRecorder to capture SSE output.
	w := httptest.NewRecorder()
	srv.handleRPC(w, r)

	// The response should be SSE with the actual task status (input-required),
	// not hardcoded working.
	resp := w.Body.String()
	if !strings.Contains(resp, `"state":"input-required"`) {
		t.Errorf("stream should contain input-required state, got: %s", resp)
	}
	// The status event should include Final=false since input-required is not terminal.
	if !strings.Contains(resp, `"final":false`) && strings.Contains(resp, `"state":"input-required"`) {
		t.Errorf("input-required status should have Final=false")
	}
}

// TestIssue1075_CancelRaceCondition tests that handleTaskCancel handles the race
// condition where a task is deleted after cancellation. #1075
func TestIssue1075_CancelRaceCondition(t *testing.T) {
	handler := NewTaskHandler(".", nil, nil)
	cfg := ServerConfig{
		Port:   0,
		APIKey: "test-key",
	}
	srv := NewServer(cfg, handler)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()

	// Create a task.
	taskID := "task-cancel-race"
	now := time.Now()
	task := &Task{
		ID:        taskID,
		ContextID: "ctx-test-1075",
		History:   []Message{{Role: "user", Parts: []Part{{Kind: "text", Text: "test"}}}},
		Status: TaskStatus{
			State:     TaskStateWorking,
			Timestamp: now,
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
	handler.mu.Lock()
	handler.tasks[taskID] = task
	handler.mu.Unlock()

	// Cancel the task.
	cancelBody, _ := json.Marshal(JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`"1"`),
		Method:  "tasks/cancel",
		Params: func(v interface{}) json.RawMessage {
			b, _ := json.Marshal(v)
			return json.RawMessage(b)
		}(CancelTaskParams{ID: taskID}),
	})
	r, _ := http.NewRequest("POST", srv.Endpoint()+"/", bytes.NewReader(cancelBody))
	r.Header.Set("X-API-Key", "test-key")
	r.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("cancel failed with status %d", resp.StatusCode)
	}

	var cancelResp JSONRPCResponse
	if err := json.NewDecoder(resp.Body).Decode(&cancelResp); err != nil {
		t.Fatalf("unmarshal cancel response: %v", err)
	}
	if cancelResp.Error != nil {
		t.Fatalf("cancel returned error: %+v", cancelResp.Error)
	}

	// Delete the task to simulate the race with cleanupExpiredTasksLocked.
	handler.mu.Lock()
	delete(handler.tasks, taskID)
	handler.mu.Unlock()

	// Try to cancel again - should get ErrTaskNotFound, not result:null.
	r2, _ := http.NewRequest("POST", srv.Endpoint()+"/", bytes.NewReader(cancelBody))
	r2.Header.Set("X-API-Key", "test-key")
	r2.Header.Set("Content-Type", "application/json")
	resp2, err := http.DefaultClient.Do(r2)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != 200 {
		t.Fatalf("second cancel failed with status %d", resp2.StatusCode)
	}

	var resp2Body JSONRPCResponse
	if err := json.NewDecoder(resp2.Body).Decode(&resp2Body); err != nil {
		t.Fatalf("unmarshal second cancel response: %v", err)
	}
	if resp2Body.Error == nil {
		t.Fatal("second cancel should return error when task is deleted")
	}
	if resp2Body.Error.Code != ErrTaskNotFound.Code {
		t.Errorf("expected error code %d, got %d", ErrTaskNotFound.Code, resp2Body.Error.Code)
	}
	if resp2Body.Result != nil {
		t.Fatalf("second cancel should not return result, got: %+v", resp2Body.Result)
	}
}

// TestIssue1076_PushConfigGetMissing tests that missing push config returns
// InvalidParams instead of ErrTaskNotFound. #1076
func TestIssue1076_PushConfigGetMissing(t *testing.T) {
	handler := NewTaskHandler(".", nil, nil)
	cfg := ServerConfig{
		Port:   0,
		APIKey: "test-key",
	}
	srv := NewServer(cfg, handler)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()

	// Try to get a non-existent push config.
	body, _ := json.Marshal(JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`"1"`),
		Method:  "tasks/pushNotificationConfig/get",
		Params: func(v interface{}) json.RawMessage {
			b, _ := json.Marshal(v)
			return json.RawMessage(b)
		}(map[string]string{"id": "nonexistent"}),
	})
	r, _ := http.NewRequest("POST", srv.Endpoint()+"/", bytes.NewReader(body))
	r.Header.Set("X-API-Key", "test-key")
	r.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("request failed with status %d", resp.StatusCode)
	}

	var respBody JSONRPCResponse
	if err := json.NewDecoder(resp.Body).Decode(&respBody); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if respBody.Error == nil {
		t.Fatal("should return error for missing push config")
	}

	// Should use InvalidParams (-32602), not ErrTaskNotFound (-32001).
	if respBody.Error.Code != -32602 {
		t.Errorf("expected error code -32602 (InvalidParams), got %d", respBody.Error.Code)
	}

	// Error message should mention push config not found.
	if !strings.Contains(respBody.Error.Message, "Invalid params") {
		t.Errorf("expected Invalid params message, got: %s", respBody.Error.Message)
	}

	// Data field should contain detailed error.
	if respBody.Error.Data == "" {
		t.Error("expected Data field with push config not found message")
	} else {
		dataStr := respBody.Error.Data
		if !strings.Contains(dataStr, "push config not found") {
			t.Errorf("expected 'push config not found' in Data, got: %s", dataStr)
		}
		if !strings.Contains(dataStr, "nonexistent") {
			t.Errorf("expected config ID 'nonexistent' in Data, got: %s", dataStr)
		}
	}
}

// TestIssue1076_PushConfigGetExisting tests that existing push config is returned.
func TestIssue1076_PushConfigGetExisting(t *testing.T) {
	handler := NewTaskHandler(".", nil, nil)
	cfg := ServerConfig{
		Port:   0,
		APIKey: "test-key",
	}
	srv := NewServer(cfg, handler)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()

	// Create a push config directly in the server's config map.
	testCfg := PushNotificationConfig{
		ID:     "test-config",
		TaskID: "task-123",
		URL:    "https://example.com/callback",
	}
	srv.pushMu.Lock()
	srv.pushConfigs[testCfg.ID] = testCfg
	srv.pushMu.Unlock()

	// Get the config.
	body, _ := json.Marshal(JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`"1"`),
		Method:  "tasks/pushNotificationConfig/get",
		Params: func(v interface{}) json.RawMessage {
			b, _ := json.Marshal(v)
			return json.RawMessage(b)
		}(map[string]string{"id": "test-config"}),
	})
	r, _ := http.NewRequest("POST", srv.Endpoint()+"/", bytes.NewReader(body))
	r.Header.Set("X-API-Key", "test-key")
	r.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("request failed with status %d", resp.StatusCode)
	}

	var respBody JSONRPCResponse
	if err := json.NewDecoder(resp.Body).Decode(&respBody); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if respBody.Error != nil {
		t.Fatalf("should not return error: %+v", respBody.Error)
	}

	// Verify the returned config.
	resultBytes, _ := json.Marshal(respBody.Result)
	var gotCfg PushNotificationConfig
	if err := json.Unmarshal(resultBytes, &gotCfg); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	if gotCfg.ID != testCfg.ID {
		t.Errorf("expected ID %s, got %s", testCfg.ID, gotCfg.ID)
	}
	if gotCfg.TaskID != testCfg.TaskID {
		t.Errorf("expected TaskID %s, got %s", testCfg.TaskID, gotCfg.TaskID)
	}
	if gotCfg.URL != testCfg.URL {
		t.Errorf("expected URL %s, got %s", testCfg.URL, gotCfg.URL)
	}
}
