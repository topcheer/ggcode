package main

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/topcheer/ggcode/internal/tunnel"
)

type stubShareInviteRefresher struct {
	info  *tunnel.SessionInfo
	err   error
	calls int
}

func (s *stubShareInviteRefresher) RefreshInvite(context.Context) (*tunnel.SessionInfo, error) {
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	return s.info, nil
}

func TestRefreshShareInviteUsesRefreshedInfo(t *testing.T) {
	want := &tunnel.SessionInfo{
		ConnectURL: "https://gateway.ggcode.dev/share#fresh-token",
		Token:      "fresh-token",
	}
	sess := &stubShareInviteRefresher{info: want}

	got, err := refreshShareInvite(context.Background(), sess)
	if err != nil {
		t.Fatalf("refresh share invite: %v", err)
	}
	if sess.calls != 1 {
		t.Fatalf("expected RefreshInvite to be called once, got %d", sess.calls)
	}
	if got != want {
		t.Fatalf("expected refreshed session info pointer, got %#v", got)
	}
}

func TestRefreshShareInvitePropagatesErrors(t *testing.T) {
	wantErr := errors.New("refresh failed")
	sess := &stubShareInviteRefresher{err: wantErr}

	_, err := refreshShareInvite(context.Background(), sess)
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected %v, got %v", wantErr, err)
	}
	if sess.calls != 1 {
		t.Fatalf("expected RefreshInvite to be called once, got %d", sess.calls)
	}
}

func TestRefreshShareInviteRejectsNilSession(t *testing.T) {
	_, err := refreshShareInvite(context.Background(), nil)
	if err == nil {
		t.Fatal("expected nil session error")
	}
}

func TestForwardTunnelUserMessagePreservesClientMessageID(t *testing.T) {
	app := &App{}
	sess := tunnel.NewSession(tunnel.DefaultRelayURL)
	broker := tunnel.NewBroker(sess)
	broker.Stop()

	var events []tunnel.GatewayMessage
	broker.SetEventRecorder(func(ev tunnel.GatewayMessage) {
		events = append(events, ev)
	})

	app.forwardTunnelUserMessage(broker, tunnel.MessageData{
		Text:      "hello from mobile",
		MessageID: "user-9-123",
	})

	if len(events) != 1 {
		t.Fatalf("expected one tunnel event, got %d", len(events))
	}
	if events[0].Type != tunnel.EventUserMessage {
		t.Fatalf("expected %q event, got %q", tunnel.EventUserMessage, events[0].Type)
	}
	var data tunnel.MessageData
	if err := json.Unmarshal(events[0].Data, &data); err != nil {
		t.Fatalf("unmarshal user_message data: %v", err)
	}
	if data.Text != "hello from mobile" {
		t.Fatalf("unexpected user text %q", data.Text)
	}
	if data.MessageID != "user-9-123" {
		t.Fatalf("expected client message id to round-trip, got %q", data.MessageID)
	}
}

func TestForwardTunnelUserMessageRejectsNonUserClientMessageID(t *testing.T) {
	app := &App{}
	sess := tunnel.NewSession(tunnel.DefaultRelayURL)
	broker := tunnel.NewBroker(sess)
	broker.Stop()

	var events []tunnel.GatewayMessage
	broker.SetEventRecorder(func(ev tunnel.GatewayMessage) {
		events = append(events, ev)
	})

	app.forwardTunnelUserMessage(broker, tunnel.MessageData{
		Text:      "hello from mobile",
		MessageID: "ev-000123",
	})

	if len(events) != 1 {
		t.Fatalf("expected one tunnel event, got %d", len(events))
	}
	var data tunnel.MessageData
	if err := json.Unmarshal(events[0].Data, &data); err != nil {
		t.Fatalf("unmarshal user_message data: %v", err)
	}
	if data.MessageID != "" {
		t.Fatalf("expected invalid client id to be dropped, got %q", data.MessageID)
	}
}
