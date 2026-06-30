package tool

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

// mockIMManager implements IMManager for testing.
type mockIMManager struct {
	snapshot           IMSnapshot
	muted              map[string]bool
	disabled           map[string]bool
	emitErr            error
	sendDirectErr      error
	lastSendEvent      IMOutboundEvent
	lastSendAdapter    string
	muteErr            error
	unmuteErr          error
	disableErr         error
	enableErr          error
	otherInstances     bool
	healthyAfterAction bool // when true, waitForHealthy will succeed after unmute/enable
}

func (m *mockIMManager) Snapshot() IMSnapshot { return m.snapshot }
func (m *mockIMManager) MuteBinding(name string) error {
	if m.muteErr != nil {
		return m.muteErr
	}
	if m.muted == nil {
		m.muted = make(map[string]bool)
	}
	m.muted[name] = true
	return nil
}
func (m *mockIMManager) UnmuteBinding(name string) error {
	if m.unmuteErr != nil {
		return m.unmuteErr
	}
	if m.muted == nil {
		m.muted = make(map[string]bool)
	}
	delete(m.muted, name)
	// Simulate adapter becoming healthy after unmute
	if m.healthyAfterAction {
		for i := range m.snapshot.Adapters {
			if m.snapshot.Adapters[i].Name == name {
				m.snapshot.Adapters[i].Healthy = true
				m.snapshot.Adapters[i].Status = "connected"
			}
		}
	}
	return nil
}
func (m *mockIMManager) DisableBinding(name string) error {
	if m.disableErr != nil {
		return m.disableErr
	}
	if m.disabled == nil {
		m.disabled = make(map[string]bool)
	}
	m.disabled[name] = true
	return nil
}
func (m *mockIMManager) EnableBinding(name string) error {
	if m.enableErr != nil {
		return m.enableErr
	}
	if m.disabled == nil {
		m.disabled = make(map[string]bool)
	}
	delete(m.disabled, name)
	// Simulate adapter becoming healthy after enable
	if m.healthyAfterAction {
		for i := range m.snapshot.Adapters {
			if m.snapshot.Adapters[i].Name == name {
				m.snapshot.Adapters[i].Healthy = true
				m.snapshot.Adapters[i].Status = "connected"
			}
		}
	}
	return nil
}
func (m *mockIMManager) IsBindingMuted(name string) bool {
	return m.muted[name]
}
func (m *mockIMManager) IsBindingDisabled(name string) bool {
	return m.disabled[name]
}
func (m *mockIMManager) Emit(ctx context.Context, event IMOutboundEvent) error {
	return m.emitErr
}
func (m *mockIMManager) SendDirect(ctx context.Context, adapter string, event IMOutboundEvent) error {
	m.lastSendAdapter = adapter
	m.lastSendEvent = event
	return m.sendDirectErr
}
func (m *mockIMManager) OtherInstancesHaveActiveChannels() bool {
	return m.otherInstances
}

func TestIMTool_NoManager(t *testing.T) {
	tool := IMTool{}
	result, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"status"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error result when no manager")
	}
}

func TestIMTool_Status(t *testing.T) {
	mgr := &mockIMManager{
		snapshot: IMSnapshot{
			CurrentBindings: []IMChannelBinding{
				{Adapter: "tg-bot", Platform: "telegram", ChannelID: "12345", Muted: false},
				{Adapter: "dc-bot", Platform: "discord", ChannelID: "67890", Muted: true},
			},
			Adapters: []IMAdapterState{
				{Name: "tg-bot", Platform: "telegram", Healthy: true, Status: "connected"},
				{Name: "dc-bot", Platform: "discord", Healthy: true, Status: "connected"},
			},
		},
	}
	tool := IMTool{Manager: mgr}
	result, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"status"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result: %s", result.Content)
	}
	if !contains(result.Content, "tg-bot") {
		t.Errorf("expected 'tg-bot' in output, got: %s", result.Content)
	}
	if !contains(result.Content, "dc-bot") {
		t.Errorf("expected 'dc-bot' in output, got: %s", result.Content)
	}
}

func TestIMTool_StatusEmpty(t *testing.T) {
	mgr := &mockIMManager{snapshot: IMSnapshot{}}
	tool := IMTool{Manager: mgr}
	result, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"status"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Content != "No IM adapters configured for this workspace." {
		t.Errorf("expected empty message, got: %s", result.Content)
	}
}

func TestIMTool_StatusMultiInstanceWarning(t *testing.T) {
	mgr := &mockIMManager{
		otherInstances: true,
		snapshot: IMSnapshot{
			CurrentBindings: []IMChannelBinding{
				{Adapter: "tg-bot", Platform: "telegram", ChannelID: "12345"},
			},
			Adapters: []IMAdapterState{
				{Name: "tg-bot", Platform: "telegram", Healthy: true, Status: "connected"},
			},
		},
	}
	tool := IMTool{Manager: mgr}
	result, _ := tool.Execute(context.Background(), json.RawMessage(`{"action":"status"}`))
	if !contains(result.Content, "Other instances") {
		t.Errorf("expected multi-instance warning, got: %s", result.Content)
	}
}

func TestIMTool_Mute(t *testing.T) {
	mgr := &mockIMManager{}
	tool := IMTool{Manager: mgr}

	result, _ := tool.Execute(context.Background(), json.RawMessage(`{"action":"mute"}`))
	if !result.IsError {
		t.Error("expected error when adapter is empty")
	}

	result, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"mute","adapter":"tg-bot"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Errorf("unexpected error: %s", result.Content)
	}
	if !mgr.muted["tg-bot"] {
		t.Error("expected tg-bot to be muted")
	}
}

func TestIMTool_MuteError(t *testing.T) {
	mgr := &mockIMManager{muteErr: errors.New("adapter not found")}
	tool := IMTool{Manager: mgr}
	result, _ := tool.Execute(context.Background(), json.RawMessage(`{"action":"mute","adapter":"tg-bot"}`))
	if !result.IsError {
		t.Error("expected error result on mute failure")
	}
}

func TestIMTool_Unmute(t *testing.T) {
	mgr := &mockIMManager{muted: map[string]bool{"tg-bot": true}}
	tool := IMTool{Manager: mgr}
	result, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"unmute","adapter":"tg-bot"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Errorf("unexpected error: %s", result.Content)
	}
	if mgr.muted["tg-bot"] {
		t.Error("expected tg-bot to be unmuted")
	}
}

func TestIMTool_DisableEnable(t *testing.T) {
	mgr := &mockIMManager{}
	tool := IMTool{Manager: mgr}

	result, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"disable","adapter":"dc-bot"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Errorf("unexpected error: %s", result.Content)
	}
	if !mgr.disabled["dc-bot"] {
		t.Error("expected dc-bot to be disabled")
	}

	result, err = tool.Execute(context.Background(), json.RawMessage(`{"action":"enable","adapter":"dc-bot"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Errorf("unexpected error: %s", result.Content)
	}
	if mgr.disabled["dc-bot"] {
		t.Error("expected dc-bot to be enabled")
	}
}

// --- send tests ---

func TestIMTool_SendDirect(t *testing.T) {
	mgr := &mockIMManager{
		snapshot: IMSnapshot{
			CurrentBindings: []IMChannelBinding{
				{Adapter: "tg-bot", Platform: "telegram", ChannelID: "12345"},
			},
			Adapters: []IMAdapterState{
				{Name: "tg-bot", Platform: "telegram", Healthy: true, Status: "connected"},
			},
		},
	}
	tool := IMTool{Manager: mgr}

	// Missing adapter
	result, _ := tool.Execute(context.Background(), json.RawMessage(`{"action":"send","message":"hello"}`))
	if !result.IsError {
		t.Error("expected error when adapter is empty")
	}

	// Missing message
	result, _ = tool.Execute(context.Background(), json.RawMessage(`{"action":"send","adapter":"tg-bot"}`))
	if !result.IsError {
		t.Error("expected error when message is empty")
	}

	// Success - adapter healthy
	result, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"send","adapter":"tg-bot","message":"hello world"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Errorf("unexpected error: %s", result.Content)
	}
	if mgr.lastSendAdapter != "tg-bot" {
		t.Errorf("expected send to tg-bot, got %q", mgr.lastSendAdapter)
	}
	if mgr.lastSendEvent.Text != "hello world" {
		t.Errorf("expected 'hello world', got %q", mgr.lastSendEvent.Text)
	}
}

func TestIMTool_SendMutedNoAutoStart(t *testing.T) {
	mgr := &mockIMManager{
		muted: map[string]bool{"tg-bot": true},
		snapshot: IMSnapshot{
			CurrentBindings: []IMChannelBinding{
				{Adapter: "tg-bot", Platform: "telegram", ChannelID: "12345", Muted: true},
			},
			Adapters: []IMAdapterState{
				{Name: "tg-bot", Platform: "telegram", Healthy: false, Status: "disconnected"},
			},
		},
	}
	tool := IMTool{Manager: mgr}
	result, _ := tool.Execute(context.Background(), json.RawMessage(`{"action":"send","adapter":"tg-bot","message":"hello"}`))
	if !result.IsError {
		t.Error("expected error when sending via muted adapter without auto_start")
	}
	if !contains(result.Content, "auto_start") {
		t.Errorf("error should mention auto_start, got: %s", result.Content)
	}
}

func TestIMTool_SendMutedAutoStart(t *testing.T) {
	mgr := &mockIMManager{
		muted:              map[string]bool{"tg-bot": true},
		healthyAfterAction: true,
		snapshot: IMSnapshot{
			CurrentBindings: []IMChannelBinding{
				{Adapter: "tg-bot", Platform: "telegram", ChannelID: "12345", Muted: true},
			},
			Adapters: []IMAdapterState{
				{Name: "tg-bot", Platform: "telegram", Healthy: false, Status: "disconnected"},
			},
		},
	}
	tool := IMTool{Manager: mgr}
	result, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"send","adapter":"tg-bot","message":"hello","auto_start":true}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Errorf("unexpected error: %s", result.Content)
	}
	if mgr.lastSendAdapter != "tg-bot" {
		t.Errorf("expected send to tg-bot after auto-start, got %q", mgr.lastSendAdapter)
	}
}

func TestIMTool_SendDisabledAutoStart(t *testing.T) {
	mgr := &mockIMManager{
		disabled:           map[string]bool{"tg-bot": true},
		healthyAfterAction: true,
		snapshot: IMSnapshot{
			DisabledBindings: []IMChannelBinding{
				{Adapter: "tg-bot", Platform: "telegram", ChannelID: "12345"},
			},
			Adapters: []IMAdapterState{
				{Name: "tg-bot", Platform: "telegram", Healthy: false, Status: "disconnected"},
			},
		},
	}
	tool := IMTool{Manager: mgr}
	result, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"send","adapter":"tg-bot","message":"hi","auto_start":true}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Errorf("unexpected error: %s", result.Content)
	}
	if mgr.lastSendAdapter != "tg-bot" {
		t.Errorf("expected send after enable, got %q", mgr.lastSendAdapter)
	}
}

func TestIMTool_SendAutoStartMultiInstanceConflict(t *testing.T) {
	mgr := &mockIMManager{
		muted:          map[string]bool{"tg-bot": true},
		otherInstances: true, // another instance has active channels
		snapshot: IMSnapshot{
			CurrentBindings: []IMChannelBinding{
				{Adapter: "tg-bot", Platform: "telegram", ChannelID: "12345", Muted: true},
			},
		},
	}
	tool := IMTool{Manager: mgr}
	result, _ := tool.Execute(context.Background(), json.RawMessage(`{"action":"send","adapter":"tg-bot","message":"hello","auto_start":true}`))
	if !result.IsError {
		t.Error("expected error when auto_start with multi-instance conflict")
	}
	if !contains(result.Content, "another instance") {
		t.Errorf("error should mention another instance, got: %s", result.Content)
	}
}

func TestIMTool_SendNoBinding(t *testing.T) {
	mgr := &mockIMManager{}
	tool := IMTool{Manager: mgr}
	result, _ := tool.Execute(context.Background(), json.RawMessage(`{"action":"send","adapter":"tg-bot","message":"hello"}`))
	if !result.IsError {
		t.Error("expected error when adapter has no binding")
	}
}

func TestIMTool_SendSendError(t *testing.T) {
	mgr := &mockIMManager{
		snapshot: IMSnapshot{
			CurrentBindings: []IMChannelBinding{
				{Adapter: "tg-bot", ChannelID: "12345"},
			},
			Adapters: []IMAdapterState{
				{Name: "tg-bot", Healthy: true, Status: "connected"},
			},
		},
		sendDirectErr: errors.New("network error"),
	}
	tool := IMTool{Manager: mgr}
	result, _ := tool.Execute(context.Background(), json.RawMessage(`{"action":"send","adapter":"tg-bot","message":"hello"}`))
	if !result.IsError {
		t.Error("expected error when SendDirect fails")
	}
}

func TestIMTool_UnknownAction(t *testing.T) {
	mgr := &mockIMManager{}
	tool := IMTool{Manager: mgr}
	result, _ := tool.Execute(context.Background(), json.RawMessage(`{"action":"frobnicate"}`))
	if !result.IsError {
		t.Error("expected error for unknown action")
	}
}
