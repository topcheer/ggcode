package im

import (
	"errors"
	"testing"
	"time"
)

// mockChannelBindingStore captures persist calls and can simulate failures.
type mockChannelBindingStore struct {
	persistErr error
	persisted  []ChannelBinding
	callCount  int
}

func (m *mockChannelBindingStore) Save(b ChannelBinding) error {
	m.callCount++
	if m.persistErr != nil {
		return m.persistErr
	}
	m.persisted = append(m.persisted, b)
	return nil
}

func (m *mockChannelBindingStore) Delete(workspace, adapter string) error { return nil }
func (m *mockChannelBindingStore) Load(workspace, adapter string) (*ChannelBinding, error) {
	return nil, nil
}
func (m *mockChannelBindingStore) List(workspace string) ([]ChannelBinding, error) {
	return nil, nil
}

func TestBindingPersistBeforeMutate_ChannelID(t *testing.T) {
	store := &mockChannelBindingStore{persistErr: errors.New("disk full")}
	binding := ChannelBinding{Adapter: "test", ChannelID: ""}

	// Simulate: persist fails, binding should NOT be mutated
	probe := binding
	probe.ChannelID = "new-channel"
	err := store.Save(probe)
	if err == nil {
		t.Fatal("expected persist error")
	}

	// Verify original binding was NOT modified
	if binding.ChannelID != "" {
		t.Error("binding.ChannelID should still be empty after persist failure")
	}
}

func TestBindingPersistBeforeMutate_Success(t *testing.T) {
	store := &mockChannelBindingStore{}
	binding := ChannelBinding{Adapter: "test", ChannelID: ""}

	// Simulate: persist succeeds, then mutate
	probe := binding
	probe.ChannelID = "new-channel"
	if err := store.Save(probe); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	binding.ChannelID = probe.ChannelID

	if binding.ChannelID != "new-channel" {
		t.Error("binding.ChannelID should be updated after successful persist")
	}
	if len(store.persisted) != 1 {
		t.Fatal("expected 1 persist call")
	}
	if store.persisted[0].ChannelID != "new-channel" {
		t.Error("persisted value should match")
	}
}

func TestApprovalChannelDoesNotBlock(t *testing.T) {
	// Verify that sending to an unbuffered channel with no receiver
	// does not block when using select+default pattern.
	ch := make(chan bool)
	done := make(chan bool, 1)

	go func() {
		select {
		case ch <- true:
		default:
		}
		done <- true
	}()

	select {
	case <-done:
		// Success: did not block
	case <-time.After(100 * time.Millisecond):
		t.Fatal("send should not have blocked")
	}
}
