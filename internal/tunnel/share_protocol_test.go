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

func TestShareSessionRefreshEndpointConvertsWebsocketURL(t *testing.T) {
	got, err := shareSessionRefreshEndpoint("wss://relay.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if want := "https://relay.example.com/share/session/refresh"; got != want {
		t.Fatalf("shareSessionRefreshEndpoint() = %q, want %q", got, want)
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
	server, client, err := requestIssuedShareSession(context.Background(), relayURL, ShareRuntimeConfig{EnableV3: true})
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
	server, client, err := requestIssuedShareSession(context.Background(), relayURL, ShareRuntimeConfig{EnableV3: true})
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

func TestRefreshIssuedShareSession(t *testing.T) {
	authExp := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	renewExp := authExp.Add(24 * time.Hour)
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != shareSessionRefreshPath {
			t.Fatalf("path = %s, want %s", r.URL.Path, shareSessionRefreshPath)
		}
		var req relayRefreshShareSessionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if req.RoomID != "room-1" || req.ServerRenewToken != "server-renew" {
			t.Fatalf("unexpected refresh request: %+v", req)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(relayRefreshedShareSessionResponse{
			ProtocolVersion:  ShareProtocolV3,
			ShareMode:        ShareModeV3,
			RoomID:           "room-1",
			ClientAuthTicket: "client-connect-2",
			ServerRenewToken: "server-renew-2",
			AuthExpiresAt:    authExp.Format(time.RFC3339),
			RenewExpiresAt:   renewExp.Format(time.RFC3339),
			Notice:           "refreshed",
		})
	}))
	defer srv.Close()

	relayURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	server := testShareDescriptor(t)
	server.RoomID = "room-1"
	server.RenewToken = "server-renew"
	server.AuthTicket = "server-connect"

	updatedServer, client, err := refreshIssuedShareSession(context.Background(), relayURL, server)
	if err != nil {
		t.Fatal(err)
	}
	if requests != 1 {
		t.Fatalf("refresh requests = %d, want 1", requests)
	}
	if updatedServer.RoomID != server.RoomID || updatedServer.CryptoKey != server.CryptoKey || updatedServer.ServerPrivateKey != server.ServerPrivateKey {
		t.Fatalf("refresh should preserve server crypto material: before=%+v after=%+v", server, updatedServer)
	}
	if updatedServer.RenewToken != "server-renew-2" {
		t.Fatalf("updated server renew token = %q", updatedServer.RenewToken)
	}
	if client.AuthTicket != "client-connect-2" {
		t.Fatalf("refreshed client auth ticket = %q", client.AuthTicket)
	}
	if client.CryptoKey != "" || client.ServerPrivateKey != "" {
		t.Fatalf("public refreshed descriptor leaked private crypto material: %+v", client)
	}
	if client.ServerPublicKey != server.ServerPublicKey {
		t.Fatalf("expected public key to be preserved, got %+v", client)
	}
	if strings.Contains(client.PublicConnectURL(relayURL), "server-renew-2") {
		t.Fatalf("public connect url leaked server renew token: %s", client.PublicConnectURL(relayURL))
	}
}
