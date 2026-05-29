//go:build !integration

package tunnel

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestShareSessionEndpointConvertsWebsocketURL(t *testing.T) {
	got, err := shareSessionEndpoint("wss://relay.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if want := "https://relay.example.com/share/session"; got != want {
		t.Fatalf("shareSessionEndpoint() = %q, want %q", got, want)
	}
}

func TestRequestIssuedShareSession(t *testing.T) {
	authExp := time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC)
	renewExp := authExp.Add(24 * time.Hour)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != shareSessionPath {
			t.Fatalf("path = %s, want %s", r.URL.Path, shareSessionPath)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(relayIssuedShareSessionResponse{
			ProtocolVersion:  ShareProtocolV2,
			ShareMode:        ShareModeV2,
			RoomID:           "room-1",
			ServerAuthTicket: "server-connect",
			ClientAuthTicket: "client-connect",
			ServerRenewToken: "server-renew",
			AuthExpiresAt:    authExp.Format(time.RFC3339),
			RenewExpiresAt:   renewExp.Format(time.RFC3339),
			Notice:           "relay issued",
		})
	}))
	defer srv.Close()

	relayURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	server, client, err := requestIssuedShareSession(context.Background(), relayURL)
	if err != nil {
		t.Fatal(err)
	}
	if server.RoomID != "room-1" || client.RoomID != "room-1" {
		t.Fatalf("unexpected room ids: server=%q client=%q", server.RoomID, client.RoomID)
	}
	if server.AuthTicket != "server-connect" || client.AuthTicket != "client-connect" {
		t.Fatalf("unexpected auth tickets: %#v %#v", server, client)
	}
	if server.RenewToken != "server-renew" {
		t.Fatalf("server renew token = %q", server.RenewToken)
	}
	if client.RenewToken != "" {
		t.Fatalf("client renew token should be empty, got %q", client.RenewToken)
	}
	if server.CryptoKey == "" || client.CryptoKey == "" || server.CryptoKey != client.CryptoKey {
		t.Fatalf("crypto keys not shared correctly: server=%q client=%q", server.CryptoKey, client.CryptoKey)
	}
	if !server.AuthExpiresAt.Equal(authExp) || !client.RenewExpiresAt.Equal(renewExp) {
		t.Fatalf("unexpected expiry parsing: %#v %#v", server, client)
	}
}
