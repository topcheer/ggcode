package main

import (
	"context"
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
