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

func TestShareSessionEndpointRejectsRemoteInsecureRelayURL(t *testing.T) {
	_, err := shareSessionEndpoint("ws://relay.example.com")
	if err == nil || !strings.Contains(err.Error(), "insecure relay URL") {
		t.Fatalf("shareSessionEndpoint() error = %v, want insecure relay URL error", err)
	}
}

func TestShareSessionEndpointAllowsPrivateInsecureRelayURL(t *testing.T) {
	got, err := shareSessionEndpoint("ws://192.168.1.5:8080")
	if err != nil {
		t.Fatal(err)
	}
	if want := "http://192.168.1.5:8080/share/session"; got != want {
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
			ProtocolVersion:  ShareProtocolV3,
			ShareMode:        ShareModeV3,
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
	server, client, err := requestIssuedShareSession(context.Background(), relayURL, ShareRuntimeConfig{EnableV2: true, EnableV3: true})
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
	if server.CryptoKey == "" {
		t.Fatalf("server crypto key should be set, got %q", server.CryptoKey)
	}
	if client.CryptoKey != "" {
		t.Fatalf("public client descriptor should omit crypto key, got %q", client.CryptoKey)
	}
	if !server.AuthExpiresAt.Equal(authExp) || !client.RenewExpiresAt.Equal(renewExp) {
		t.Fatalf("unexpected expiry parsing: %#v %#v", server, client)
	}
}

func TestRequestIssuedShareSessionV3OmitsPublicCryptoKey(t *testing.T) {
	authExp := time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC)
	renewExp := authExp.Add(24 * time.Hour)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("proto"); got != "3" {
			t.Fatalf("proto query = %q, want 3", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(relayIssuedShareSessionResponse{
			ProtocolVersion:  ShareProtocolV3,
			ShareMode:        ShareModeV3,
			RoomID:           "room-3",
			ServerAuthTicket: "server-connect",
			ClientAuthTicket: "client-connect",
			ServerRenewToken: "server-renew",
			AuthExpiresAt:    authExp.Format(time.RFC3339),
			RenewExpiresAt:   renewExp.Format(time.RFC3339),
		})
	}))
	defer srv.Close()

	relayURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	server, client, err := requestIssuedShareSession(context.Background(), relayURL, ShareRuntimeConfig{EnableV2: true, EnableV3: true})
	if err != nil {
		t.Fatal(err)
	}
	if !server.IsV3() || !client.IsV3() {
		t.Fatalf("expected v3 descriptors, got server=%+v client=%+v", server, client)
	}
	if server.CryptoKey == "" {
		t.Fatal("server crypto key should be present")
	}
	if client.CryptoKey != "" {
		t.Fatalf("client crypto key should stay out of public descriptor, got %q", client.CryptoKey)
	}
	if client.ServerPublicKey == "" || server.ServerPrivateKey == "" {
		t.Fatalf("missing key exchange material: server=%+v client=%+v", server, client)
	}
	if strings.Contains(client.PublicConnectURL(relayURL), "crypto_key=") {
		t.Fatalf("public v3 URL should not include crypto_key: %s", client.PublicConnectURL(relayURL))
	}
}
